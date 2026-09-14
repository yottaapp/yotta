package workflow_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/appbootstrap"
	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/services/workflow"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	bundle "github.com/yottaapp/yotta/internal/workflowbundle"
	"github.com/yottaapp/yotta/internal/workflowstore"
	publicbundle "github.com/yottaapp/yotta/pkg/workflowbundle"
)

func legacyRegistryBundle(t *testing.T, current []byte) ([]byte, []byte, artifact.Digest) {
	t.Helper()
	var source map[string]any
	if err := json.Unmarshal(current, &source); err != nil {
		t.Fatal(err)
	}
	source["version"] = "4"
	delete(source, "targets")
	delete(source, "targetDefaults")
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	raw, hash, err := schema.CanonicalSourceArtifact(raw)
	if err != nil {
		t.Fatal(err)
	}
	manifest := bundle.Manifest{Format: bundle.Format, Version: 2, SourceTrust: bundle.SourceTrustUnverified, Evidence: []bundle.EvidenceRef{}, WorkflowID: source["workflow"].(map[string]any)["id"].(string), SourceHash: hash, Dependencies: []schema.NodePackageDependency{}, Blobs: []blob.BlobRef{}}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for name, data := range map[string][]byte{bundle.SourcePath: raw, bundle.ManifestPath: encoded} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes(), raw, hash
}

func TestRegistryOldBundleInstallAndKnownMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	runtime := workflowRuntime(t, time.Now())
	if err := runtime.Application.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(ctx)
	source, err := runtime.Application.CreateSource(ctx, "Legacy")
	if err != nil {
		t.Fatal(err)
	}
	archive, legacy, originalHash := legacyRegistryBundle(t, source.Artifact())
	info, _, err := bundle.InspectBytes(ctx, archive)
	if err != nil {
		t.Fatal(err)
	}
	if info.PublishedSourceHash != originalHash || info.SourceHash == originalHash {
		t.Fatalf("identities: %+v", info)
	}
	registry := &registryClientFake{bundle: archive, releaseVersion: "1.0.0", published: publicbundle.Info{WorkflowID: source.WorkflowID(), SourceHash: string(originalHash)}}
	consumer := workflowRuntime(t, time.Now())
	if err := consumer.Application.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer consumer.Close(ctx)
	path := filepath.Join(t.TempDir(), "registry-installations.json")
	service, err := workflow.NewService(consumer.Application, workflow.WithBundleManager(consumer.Bundles), workflow.WithRegistryClient(registry), workflow.WithRegistryState(path))
	if err != nil {
		t.Fatal(err)
	}
	installed, err := service.InstallRegistryWorkflow(ctx, "release-1")
	if err != nil {
		t.Fatal(err)
	}
	values, err := consumer.Application.GetParameters(installed.WorkflowID)
	if err != nil || len(values.Values) != 0 {
		t.Fatalf("import leaked overrides: %+v %v", values, err)
	}

	// Bootstrap an installation created by the old app. Its migration must
	// persist the exact baseline transition through the production DataRoot seam.
	var baselinePath string
	upgraded := workflowRuntimeConfigured(t, time.Now(), func(config *appbootstrap.Config) {
		baselinePath = filepath.Join(config.DataRoot, "registry-installations.json")
		now := time.Now().UTC()
		if err := config.WorkflowRepository.Commit(ctx, -1, catalog.WorkflowSourceRecord{WorkflowID: source.WorkflowID(), Name: "Legacy", Revision: 0, Hash: originalHash, Format: schema.Format, Version: "4", Artifact: legacy, CreatedAt: now, UpdatedAt: now}, nil); err != nil {
			t.Fatal(err)
		}
		records := map[string]workflow.RegistryInstallation{source.WorkflowID(): {WorkflowID: source.WorkflowID(), ReleaseID: "release-1", ReleaseVersion: "1.0.0", SourceHash: string(originalHash)}}
		raw, _ := json.Marshal(records)
		if err := os.MkdirAll(config.DataRoot, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baselinePath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	})
	if err := upgraded.Application.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close(ctx)
	service, err = workflow.NewService(upgraded.Application, workflow.WithBundleManager(upgraded.Bundles), workflow.WithRegistryClient(registry), workflow.WithRegistryState(baselinePath))
	if err != nil {
		t.Fatal(err)
	}
	beforeRead, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := upgraded.Application.GetSource(source.WorkflowID())
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		records, err := service.RegistryInstallations()
		if err != nil || len(records) != 1 || records[0].SourceHash != string(migrated.Hash()) {
			t.Fatalf("read migration baseline: %+v %v", records, err)
		}
		afterRead, err := os.ReadFile(baselinePath)
		if err != nil || !bytes.Equal(beforeRead, afterRead) {
			t.Fatalf("read modified installation records: %v", err)
		}
	}
	registry.releaseVersion = "2.0.0"
	if _, err := service.InstallRegistryWorkflow(ctx, "release-2"); err != nil {
		t.Fatalf("unmodified migrated upgrade: %v", err)
	}
	current, err := upgraded.Application.GetSource(source.WorkflowID())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateSourceMetadata(current.WorkflowID(), current.Revision(), workflow.UpdateSourceMetadataRequest{Name: "Real local edit"}); err != nil {
		t.Fatal(err)
	}
	registry.releaseVersion = "3.0.0"
	if _, err := service.InstallRegistryWorkflow(ctx, "release-3"); apperr.From(err).ID != "workflow.registry.local_changes" {
		t.Fatalf("real edit accepted: %v", err)
	}
}

func TestLocalCloneCopiesParametersButBundleImportDoesNot(t *testing.T) {
	ctx := context.Background()
	var dataRoot string
	runtime := workflowRuntimeConfigured(t, time.Now(), func(config *appbootstrap.Config) { dataRoot = config.DataRoot })
	if err := runtime.Application.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(ctx)
	source, err := runtime.Application.CreateSource(ctx, "Configured")
	if err != nil {
		t.Fatal(err)
	}
	patched, err := runtime.Application.ApplyPatch(ctx, authoring.PatchRequest{WorkflowID: source.WorkflowID(), BaseRevision: source.Revision(), Commands: []authoring.Command{{Kind: authoring.CommandAddStateVariable, AddStateVariable: &authoring.AddStateVariableCommand{Name: "quantity", Type: datatype.RefExpression(runtime.Builtins.StringType.TypeRef()), Default: "author", Parameter: &schema.Parameter{ID: "quantity", Label: "Quantity", Control: "auto"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]json.RawMessage{"quantity": json.RawMessage(`"local"`), "@target/workflow-default": json.RawMessage(`""`)}
	ds, err := runtime.Application.SaveParameters(ctx, source.WorkflowID(), patched.Source.Revision(), expected)
	if err != nil || schema.HasErrors(ds) {
		t.Fatalf("save: %v %v", ds, err)
	}
	// Cloning preserves local selections even when that machine target is
	// temporarily unavailable; package import must still be empty.
	localStore, err := workflowstore.OpenParameterStore(filepath.Join(dataRoot, "workflow-parameters"))
	if err != nil {
		t.Fatal(err)
	}
	expected["@target/workflow-default"] = json.RawMessage(`"local-game"`)
	if err := localStore.Save(source.WorkflowID(), expected); err != nil {
		t.Fatal(err)
	}
	service, err := workflow.NewService(runtime.Application, workflow.WithBundleManager(runtime.Bundles))
	if err != nil {
		t.Fatal(err)
	}
	clone, err := service.CloneSource(ctx, source.WorkflowID())
	if err != nil {
		t.Fatal(err)
	}
	copied, err := runtime.Application.GetParameters(clone.WorkflowID)
	if err != nil || len(copied.Values) != 2 || string(copied.Values["quantity"]) != `"local"` || string(copied.Values["@target/workflow-default"]) != `"local-game"` {
		t.Fatalf("clone: %+v %v", copied, err)
	}
	archive := filepath.Join(t.TempDir(), "copy.yotta-workflow")
	if _, err := runtime.Bundles.Export(ctx, source.WorkflowID(), archive); err != nil {
		t.Fatal(err)
	}
	imported, err := runtime.Bundles.Import(ctx, bundle.ImportRequest{Path: archive, Mode: bundle.ImportCopy})
	if err != nil {
		t.Fatal(err)
	}
	clean, err := runtime.Application.GetParameters(imported.Source.WorkflowID())
	if err != nil || len(clean.Values) != 0 {
		t.Fatalf("bundle imported local config: %+v %v", clean, err)
	}
	original, err := runtime.Application.GetSource(source.WorkflowID())
	if err != nil || original.Hash() != patched.Source.Hash() {
		t.Fatal("clone changed author source")
	}
}
