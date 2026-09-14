package workflowbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

type testSourceRepository struct{ store *workflowstore.SourceStore }

func (r testSourceRepository) GetSource(workflowID string) (workflowstore.SourceSnapshot, error) {
	return r.store.Load(workflowID)
}

func (r testSourceRepository) PublishImportedSource(ctx context.Context, raw []byte, baseRevision int64, expectedHash artifact.Digest) (workflowstore.SourceSnapshot, error) {
	if baseRevision >= 0 {
		var identity struct {
			Workflow struct {
				ID string `json:"id"`
			} `json:"workflow"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return workflowstore.SourceSnapshot{}, err
		}
		current, err := r.store.Load(identity.Workflow.ID)
		if err != nil {
			return workflowstore.SourceSnapshot{}, err
		}
		if current.Revision() != baseRevision || current.Hash() != expectedHash {
			return workflowstore.SourceSnapshot{}, workflowstore.ErrSourceConflict
		}
	}
	return r.store.Save(ctx, raw, baseRevision)
}

func TestManagerRoundTripsCanonicalSourceAndReferencedBlobs(t *testing.T) {
	ctx := context.Background()
	sourceStore, sourceBlobs := openTestWorkspace(t)
	ref, err := sourceBlobs.Put(ctx, "application/vnd.yotta.macro+json", strings.NewReader("portable payload"))
	if err != nil {
		t.Fatal(err)
	}
	source := testResourceSource("source_workflow", "Portable source", ref)
	original, err := sourceStore.Save(ctx, source, -1)
	if err != nil {
		t.Fatal(err)
	}
	sourceManager, err := New(testSourceRepository{sourceStore}, sourceBlobs)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "portable"+Extension)
	exported, err := sourceManager.Export(ctx, original.WorkflowID(), destination)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Info.SourceHash != original.Hash() || exported.Info.SourceTrust != SourceTrustUnverified ||
		exported.Info.ResourceCount != 1 || exported.Info.DependencyCount != 1 ||
		exported.Info.BlobCount != 1 || exported.Info.BlobBytes != ref.Size {
		t.Fatalf("export info = %#v", exported.Info)
	}
	fileBytes, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	bytesInfo, bundleBytes, err := sourceManager.ExportBytes(ctx, original.WorkflowID())
	if err != nil || !reflect.DeepEqual(bytesInfo, exported.Info) || !bytes.Equal(bundleBytes, fileBytes) {
		t.Fatalf("ExportBytes() info = %#v, equal = %t, error = %v", bytesInfo, bytes.Equal(bundleBytes, fileBytes), err)
	}
	inspected, err := sourceManager.Inspect(ctx, destination)
	if err != nil || inspected.WorkflowID != original.WorkflowID() {
		t.Fatalf("Inspect() = %#v, %v", inspected, err)
	}
	targetStore, targetBlobs := openTestWorkspace(t)
	targetManager, err := New(testSourceRepository{targetStore}, targetBlobs)
	if err != nil {
		t.Fatal(err)
	}
	targetManager.newID = func() string { return "imported_workflow" }
	imported, err := targetManager.Import(ctx, ImportRequest{Path: destination, Mode: ImportCopy})
	if err != nil {
		t.Fatal(err)
	}
	if imported.Source.WorkflowID() != "imported_workflow" || imported.Source.Revision() != 0 || imported.Source.Hash() == original.Hash() {
		t.Fatalf("imported source = id %q revision %d hash %q", imported.Source.WorkflowID(), imported.Source.Revision(), imported.Source.Hash())
	}
	if err := targetBlobs.Verify(ctx, ref); err != nil {
		t.Fatalf("imported blob failed verification: %v", err)
	}
}

func TestManagerReplaceRequiresExactTargetIdentity(t *testing.T) {
	ctx := context.Background()
	sources, blobs := openTestWorkspace(t)
	manager, err := New(testSourceRepository{sources}, blobs)
	if err != nil {
		t.Fatal(err)
	}
	original, err := sources.Save(ctx, testSource("source_workflow", "Replacement", blob.BlobRef{}), -1)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "replacement"+Extension)
	if _, err := manager.Export(ctx, original.WorkflowID(), archivePath); err != nil {
		t.Fatal(err)
	}
	target, err := sources.Save(ctx, testSource("target_workflow", "Old name", blob.BlobRef{}), -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Import(ctx, ImportRequest{
		Path: archivePath, Mode: ImportReplace, TargetWorkflowID: target.WorkflowID(),
		ExpectedRevision: target.Revision(), ExpectedSourceHash: artifact.Digest("sha256:" + strings.Repeat("f", 64)),
	}); !errors.Is(err, workflowstore.ErrSourceConflict) {
		t.Fatalf("wrong expected hash error = %v", err)
	}
	result, err := manager.Import(ctx, ImportRequest{
		Path: archivePath, Mode: ImportReplace, TargetWorkflowID: target.WorkflowID(),
		ExpectedRevision: target.Revision(), ExpectedSourceHash: target.Hash(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.WorkflowID() != target.WorkflowID() || result.Source.Revision() != target.Revision()+1 {
		t.Fatalf("replacement identity = %q revision %d", result.Source.WorkflowID(), result.Source.Revision())
	}
}

func TestManagerRejectsUndeclaredAndCorruptArchiveEntries(t *testing.T) {
	ctx := context.Background()
	sources, blobs := openTestWorkspace(t)
	manager, err := New(testSourceRepository{sources}, blobs)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := blobs.Put(ctx, "application/octet-stream", strings.NewReader("expected"))
	if err != nil {
		t.Fatal(err)
	}
	raw := testSource("source_workflow", "Portable source", ref)
	snapshot, err := sources.Save(ctx, raw, -1)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(Manifest{
		Format: Format, Version: Version, SourceTrust: SourceTrustUnverified,
		WorkflowID: snapshot.WorkflowID(), SourceHash: snapshot.Hash(),
		Dependencies: []schema.NodePackageDependency{}, Blobs: []blob.BlobRef{ref},
		Evidence: []EvidenceRef{},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		extra   string
		payload string
	}{
		{name: "undeclared", extra: "unexpected.json", payload: "expected"},
		{name: "traversal", extra: "../outside", payload: "expected"},
		{name: "corrupt blob", payload: "tampered"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "invalid.zip")
			entries := map[string][]byte{
				ManifestPath: manifest, SourcePath: snapshot.Artifact(), blobEntryPath(ref.Digest): []byte(test.payload),
			}
			if test.extra != "" {
				entries[test.extra] = []byte("extra")
			}
			writeTestZip(t, archivePath, entries)
			if _, err := manager.Inspect(ctx, archivePath); err == nil {
				t.Fatal("Inspect() accepted an invalid archive")
			}
		})
	}
}

func TestManagerCarriesOpaqueEvidenceWithoutClaimingTrust(t *testing.T) {
	ctx := context.Background()
	sources, blobs := openTestWorkspace(t)
	manager, err := New(testSourceRepository{sources}, blobs)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := sources.Save(
		ctx, testSource("release_workflow", "Release evidence", blob.BlobRef{}), -1,
	)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "release"+Extension)
	evidence := []EvidenceInput{
		{
			Kind: EvidencePlatformPublicationProof, MediaType: "application/vnd.yotta.publication-proof+json",
			Bytes: []byte(`{"proof":"platform"}`),
		},
		{
			Kind: EvidencePublisherAttestation, MediaType: "application/vnd.in-toto+json",
			Bytes: []byte(`{"payloadType":"application/vnd.in-toto+json"}`),
		},
	}
	exported, err := manager.ExportWithEvidence(
		ctx, snapshot.WorkflowID(), archivePath, evidence,
	)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Info.SourceTrust != SourceTrustUnverified || len(exported.Info.Evidence) != 2 ||
		exported.Info.Evidence[0].Kind != EvidencePlatformPublicationProof ||
		exported.Info.Evidence[1].Kind != EvidencePublisherAttestation {
		t.Fatalf("ExportWithEvidence() = %#v", exported.Info)
	}
	secondPath := filepath.Join(t.TempDir(), "release-reordered"+Extension)
	if _, err := manager.ExportWithEvidence(
		ctx, snapshot.WorkflowID(), secondPath,
		[]EvidenceInput{evidence[1], evidence[0]},
	); err != nil {
		t.Fatal(err)
	}
	firstBytes, firstErr := os.ReadFile(archivePath)
	secondBytes, secondErr := os.ReadFile(secondPath)
	if firstErr != nil || secondErr != nil || !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("evidence order changed archive identity: %v, %v", firstErr, secondErr)
	}
	inspected, err := manager.Inspect(ctx, archivePath)
	if err != nil || inspected.SourceTrust != SourceTrustUnverified ||
		len(inspected.Evidence) != 2 {
		t.Fatalf("Inspect() = %#v, %v", inspected, err)
	}
	entries := readTestZip(t, archivePath)
	if string(entries[evidencePath(EvidencePublisherAttestation)]) !=
		`{"payloadType":"application/vnd.in-toto+json"}` {
		t.Fatal("publisher evidence bytes changed")
	}
	entries[evidencePath(EvidencePublisherAttestation)] = []byte(`{"tampered":true}`)
	tampered := filepath.Join(t.TempDir(), "tampered"+Extension)
	writeTestZip(t, tampered, entries)
	if _, err := manager.Inspect(ctx, tampered); err == nil {
		t.Fatal("Inspect accepted tampered release evidence")
	}
	if _, err := manager.ExportWithEvidence(
		ctx, snapshot.WorkflowID(), filepath.Join(t.TempDir(), "unknown"+Extension),
		[]EvidenceInput{{Kind: "trust-policy", MediaType: "application/json", Bytes: []byte(`{}`)}},
	); err == nil {
		t.Fatal("ExportWithEvidence accepted local authority as release evidence")
	}
}

func TestManagerReadsLegacyUnsignedBundleAsUnverified(t *testing.T) {
	ctx := context.Background()
	sources, blobs := openTestWorkspace(t)
	manager, err := New(testSourceRepository{sources}, blobs)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := sources.Save(
		ctx, testSource("legacy_workflow", "Legacy unsigned", blob.BlobRef{}), -1,
	)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(struct {
		Format       string                         `json:"format"`
		Version      int                            `json:"version"`
		WorkflowID   string                         `json:"workflowId"`
		SourceHash   artifact.Digest                `json:"sourceHash"`
		Dependencies []schema.NodePackageDependency `json:"dependencies"`
		Blobs        []blob.BlobRef                 `json:"blobs"`
	}{
		Format: Format, Version: LegacyVersion, WorkflowID: snapshot.WorkflowID(),
		SourceHash: snapshot.Hash(), Dependencies: []schema.NodePackageDependency{},
		Blobs: []blob.BlobRef{},
	})
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "legacy"+Extension)
	writeTestZip(t, archivePath, map[string][]byte{
		ManifestPath: manifest, SourcePath: snapshot.Artifact(),
	})
	info, err := manager.Inspect(ctx, archivePath)
	if err != nil || info.SourceTrust != SourceTrustUnverified || len(info.Evidence) != 0 {
		t.Fatalf("Inspect legacy = %#v, %v", info, err)
	}
}

func openTestWorkspace(t *testing.T) (*workflowstore.SourceStore, *blob.Store) {
	t.Helper()
	roots, err := storage.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := catalog.Open(context.Background(), roots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foundation.Close() })
	blobs, err := blob.Open(
		roots.Objects,
		blob.Limits{MaxBlobBytes: 1 << 20, MaxTotalBytes: 8 << 20},
		foundation.Objects(),
	)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := workflowstore.OpenSourceStore(
		foundation.Workflows(),
		workflowstore.SourceStoreOptions{MaxSources: 16},
	)
	if err != nil {
		t.Fatal(err)
	}
	return sources, blobs
}

func testSource(workflowID, name string, ref blob.BlobRef) []byte {
	binding := ""
	if ref.Digest.Valid() {
		encoded, _ := json.Marshal(ref)
		binding = `"asset":{"kind":"blob","blob":` + string(encoded) + `}`
	}
	return []byte(fmt.Sprintf(`{"format":"yotta.workflow","version":"5","workflow":{"id":%q,"name":%q},"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{"id":"node_1","nodeRef":{"nodeTypeId":"https://schemas.yotta.dev/nodes/test","version":"1.0.0","semanticDigest":"sha256:%s"},"position":{"x":0,"y":0},"config":{},"bindings":{%s}}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`, workflowID, name, strings.Repeat("1", 64), binding))
}

func testResourceSource(workflowID, name string, ref blob.BlobRef) []byte {
	raw := string(testSource(workflowID, name, ref))
	encoded, _ := json.Marshal(ref)
	raw = strings.Replace(raw, `"asset":{"kind":"blob","blob":`+string(encoded)+`}`, `"asset":{"kind":"resource","resource":{"resourceId":"recording"}}`, 1)
	raw = strings.Replace(raw, `"resources":[]`, `"resources":[{"id":"recording","kind":"macro","name":"Recording","macro":{"blob":`+string(encoded)+`,"baseResolution":[1920,1080],"actionCount":1,"durationUs":1000}}]`, 1)
	raw = strings.Replace(raw, `"dependencies":[]`, `"dependencies":[{"publisherNamespace":"https://schemas.yotta.dev/packages","packageId":"https://schemas.yotta.dev/packages/test/v1","packageVersion":"1.0.0","manifestDigest":"sha256:`+strings.Repeat("2", 64)+`","nodeRefs":[{"nodeTypeId":"https://schemas.yotta.dev/nodes/test","version":"1.0.0","semanticDigest":"sha256:`+strings.Repeat("1", 64)+`"}]}]`, 1)
	return []byte(raw)
}

func writeTestZip(t *testing.T, destination string, entries map[string][]byte) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, payload := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bytes.NewReader(payload).WriteTo(writer); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func readTestZip(t *testing.T, source string) map[string][]byte {
	t.Helper()
	archive, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	entries := make(map[string][]byte, len(archive.File))
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		payload, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("read %s: %v, close: %v", entry.Name, err, closeErr)
		}
		entries[entry.Name] = payload
	}
	return entries
}
