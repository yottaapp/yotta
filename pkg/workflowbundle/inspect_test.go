package workflowbundle_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	internalbundle "github.com/yottaapp/yotta/internal/workflowbundle"
	"github.com/yottaapp/yotta/pkg/workflowbundle"
)

func TestInspectExposesPortableBundleFacts(t *testing.T) {
	rawSource := []byte(`{"format":"yotta.workflow","version":"5","workflow":{"id":"published_workflow","name":"整理照片"},"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`)
	_, canonical, sourceHash, diagnostics, err := schema.CanonicalSource(rawSource)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("CanonicalSource() diagnostics = %#v, error = %v", diagnostics, err)
	}
	manifest, err := json.Marshal(internalbundle.Manifest{
		Format: internalbundle.Format, Version: internalbundle.Version,
		SourceTrust: internalbundle.SourceTrustUnverified,
		WorkflowID:  "published_workflow", SourceHash: sourceHash,
		Dependencies: []schema.NodePackageDependency{}, Blobs: []blob.BlobRef{},
		Evidence: []internalbundle.EvidenceRef{},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := writeBundle(t, manifest, canonical)
	info, err := workflowbundle.Inspect(context.Background(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if info.WorkflowID != "published_workflow" || info.Name != "整理照片" ||
		info.SourceHash != string(sourceHash) || info.SourceTrust != "unverified" ||
		info.Dependencies == nil || len(info.Dependencies) != 0 {
		t.Fatalf("Inspect() = %#v", info)
	}
}

func TestInspectRejectsInvalidArchive(t *testing.T) {
	if _, err := workflowbundle.Inspect(context.Background(), []byte("not a bundle")); err == nil {
		t.Fatal("Inspect accepted an invalid archive")
	}
}

func writeBundle(t *testing.T, manifest, source []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for name, raw := range map[string][]byte{
		internalbundle.ManifestPath: manifest,
		internalbundle.SourcePath:   source,
	} {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := strings.NewReader(string(raw)).WriteTo(writer); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
