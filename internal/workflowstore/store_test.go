package workflowstore_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

func TestSourceStoreRequiresExplicitRevisionCASAndReopensCanonicalArtifact(t *testing.T) {
	repository := testWorkflowRepository(t)
	store := openTestSourceStore(t, repository, 2)
	created, err := store.Save(context.Background(), concatSource(t, 0, "a", "b"), -1)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkflowID() != "wf-store" || created.Revision() != 0 || !created.Hash().Valid() || strings.Contains(string(created.Artifact()), "\n") {
		t.Fatalf("created Source = %#v, %s", created, created.Artifact())
	}
	if _, err := store.Save(context.Background(), concatSource(t, 1, "x", "y"), -1); !errors.Is(err, workflowstore.ErrSourceConflict) {
		t.Fatalf("stale create error = %v", err)
	}
	updated, err := store.Save(context.Background(), concatSource(t, 1, "x", "y"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision() != 1 || updated.Hash() == created.Hash() {
		t.Fatalf("updated Source = %#v", updated)
	}
	loaded, err := store.Load("wf-store")
	if err != nil || loaded.Hash() != updated.Hash() {
		t.Fatalf("Load = %#v, %v", loaded, err)
	}
	reopened := openTestSourceStore(t, repository, 2)
	loaded, err = reopened.Load("wf-store")
	if err != nil || loaded.Revision() != 1 {
		t.Fatalf("reopened Load = %#v, %v", loaded, err)
	}
}

func TestSourceStoreRejectsInvalidAndExternallyChangedSources(t *testing.T) {
	repository := testWorkflowRepository(t)
	store := openTestSourceStore(t, repository, 2)
	if _, err := store.Save(context.Background(), []byte(`{"format":"yotta.workflow","version":"5"}`), -1); err == nil {
		t.Fatal("legacy Source was accepted")
	}
	if _, err := store.Save(context.Background(), concatSource(t, 0, "a", "b"), -1); err != nil {
		t.Fatal(err)
	}
	raw := concatSource(t, 1, "x", "y")
	document, canonical, digest, diagnostics, err := schema.CanonicalSource(raw)
	if err != nil || len(diagnostics) != 0 {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), 0, catalog.WorkflowSourceRecord{
		WorkflowID: document.Workflow.ID, Name: document.Workflow.Name, Revision: document.Revision,
		Hash: digest, Format: document.Format, Version: document.Version,
		Artifact: canonical, UpdatedAt: time.Now().UTC(),
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("wf-store"); !errors.Is(err, workflowstore.ErrSourceChanged) {
		t.Fatalf("external change error = %v", err)
	}
}

func TestSourceStoreDeleteRequiresExactRevisionAndHash(t *testing.T) {
	repository := testWorkflowRepository(t)
	store := openTestSourceStore(t, repository, 2)
	created, err := store.Save(context.Background(), concatSource(t, 0, "a", "b"), -1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), created.WorkflowID(), created.Revision()+1, created.Hash()); !errors.Is(err, workflowstore.ErrSourceConflict) {
		t.Fatalf("stale revision delete = %v", err)
	}
	wrongHash, err := artifact.Sum("yotta/test/wrong-source/v1", []byte("wrong"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), created.WorkflowID(), created.Revision(), wrongHash); !errors.Is(err, workflowstore.ErrSourceConflict) {
		t.Fatalf("stale hash delete = %v", err)
	}
	if err := store.Delete(context.Background(), created.WorkflowID(), created.Revision(), created.Hash()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(created.WorkflowID()); !errors.Is(err, workflowstore.ErrSourceNotFound) {
		t.Fatalf("Load after delete = %v", err)
	}
	reopened := openTestSourceStore(t, repository, 2)
	if len(reopened.List()) != 0 {
		t.Fatalf("reopened sources = %#v", reopened.List())
	}
}

func TestSourceStoreIsolatesRepairsAndDeletesOneCorruptSource(t *testing.T) {
	repository := testWorkflowRepository(t)
	corrupt := []byte(`{"format":"yotta.workflow","version":"5",`)
	recoveryID := testDigest(t, "corrupt-workflow-source")
	err := repository.PutQuarantine(context.Background(), catalog.WorkflowQuarantineRecord{
		ID: recoveryID, OriginalName: "wf-store.json", Reason: "invalid JSON",
		Artifact: corrupt, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened := openTestSourceStore(t, repository, 2)
	if _, err := reopened.Load("wf-store"); !errors.Is(err, workflowstore.ErrSourceNotFound) {
		t.Fatalf("isolated Source load = %v", err)
	}
	recoveries := reopened.ListRecoveries()
	if len(recoveries) != 1 || recoveries[0].OriginalName != "wf-store.json" || string(recoveries[0].Artifact()) != string(corrupt) || recoveries[0].Reason == "" {
		t.Fatalf("recoveries = %#v", recoveries)
	}
	reopenedAgain := openTestSourceStore(t, repository, 2)
	if len(reopenedAgain.ListRecoveries()) != 1 {
		t.Fatalf("reopen isolated Source = %#v", reopenedAgain)
	}
	replacement := concatSource(t, 0, "a", "b")
	repaired, err := reopenedAgain.RepairRecovery(context.Background(), recoveries[0].ID, replacement)
	if err != nil || repaired.WorkflowID() != "wf-store" || len(reopenedAgain.ListRecoveries()) != 0 {
		t.Fatalf("repair = %#v, %v, recoveries=%#v", repaired, err, reopenedAgain.ListRecoveries())
	}
	if loaded, err := reopenedAgain.Load("wf-store"); err != nil || loaded.Hash() != repaired.Hash() {
		t.Fatalf("load repaired Source = %#v, %v", loaded, err)
	}

	deleteID := testDigest(t, "delete-corrupt-workflow-source")
	if err := repository.PutQuarantine(context.Background(), catalog.WorkflowQuarantineRecord{
		ID: deleteID, OriginalName: "other.json", Reason: "invalid JSON",
		Artifact: corrupt, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	deleteStore := openTestSourceStore(t, repository, 2)
	deleteRecoveries := deleteStore.ListRecoveries()
	if len(deleteRecoveries) != 1 {
		t.Fatalf("delete recoveries = %#v", deleteRecoveries)
	}
	if err := deleteStore.DeleteRecovery(context.Background(), deleteRecoveries[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(deleteStore.ListRecoveries()) != 0 {
		t.Fatalf("recovery was not deleted: %#v", deleteStore.ListRecoveries())
	}
}

func TestProgramStorePersistsOnlyStrictContentAddressedPrograms(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	build := testDigest(t, "workflowstore compiler")
	compiled, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(context.Background(), compiler.CompileRequest{SourceJSON: concatSource(t, 0, "a", "b"), Catalog: builtins.Catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile = %v, %#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("missing Program")
	}
	root := t.TempDir()
	store, err := workflowstore.OpenProgramStore(root, builtins.Catalog, builtins.ConfigValidators, build, workflowstore.ProgramStoreOptions{MaxPrograms: 2, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), program); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), program); err != nil {
		t.Fatalf("idempotent Put = %v", err)
	}
	loaded, err := store.Load(program.Hash())
	if err != nil || loaded.Hash() != program.Hash() || string(loaded.Artifact()) != string(program.Artifact()) {
		t.Fatalf("Load = %s, %v", loaded.Hash(), err)
	}
	cachePath := programCachePath(t, root, program.Hash())
	if err := os.WriteFile(cachePath, []byte("corrupt derived cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(program.Hash()); !errors.Is(err, workflowstore.ErrProgramNotFound) {
		t.Fatalf("corrupt cache Load = %v", err)
	}
	if err := store.Put(context.Background(), program); err != nil {
		t.Fatalf("rebuild corrupt cache = %v", err)
	}
	reopened, err := workflowstore.OpenProgramStore(root, builtins.Catalog, builtins.ConfigValidators, build, workflowstore.ProgramStoreOptions{MaxPrograms: 2, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Load(program.Hash()); err != nil {
		t.Fatal(err)
	}
}

func TestProgramStoreDropsDerivedArtifactsFromAnotherCompilerBuild(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	firstBuild := testDigest(t, "workflowstore compiler first")
	compiled, err := compiler.New(firstBuild, builtins.ConfigValidators).CompileDraft(context.Background(), compiler.CompileRequest{
		SourceJSON: concatSource(t, 0, "a", "b"), Catalog: builtins.Catalog,
	})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile = %v, %#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("missing Program")
	}
	root := t.TempDir()
	store, err := workflowstore.OpenProgramStore(root, builtins.Catalog, builtins.ConfigValidators, firstBuild, workflowstore.ProgramStoreOptions{MaxPrograms: 2, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), program); err != nil {
		t.Fatal(err)
	}

	secondBuild := testDigest(t, "workflowstore compiler second")
	reopened, err := workflowstore.OpenProgramStore(root, builtins.Catalog, builtins.ConfigValidators, secondBuild, workflowstore.ProgramStoreOptions{MaxPrograms: 2, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("reopen after compiler upgrade = %v", err)
	}
	if listed, err := reopened.List(); err != nil || len(listed) != 0 {
		t.Fatalf("stale derived Programs = %#v, %v", listed, err)
	}
}

func TestProgramStoreEvictsLeastRecentlyUsedByCountAndByteQuota(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	build := testDigest(t, "workflowstore LRU compiler")
	compile := func(a, b string) compiler.ProgramSnapshot {
		t.Helper()
		result, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(
			context.Background(),
			compiler.CompileRequest{SourceJSON: concatSource(t, 0, a, b), Catalog: builtins.Catalog},
		)
		if err != nil || len(result.Diagnostics) != 0 {
			t.Fatalf("compile %q/%q = %v, %#v", a, b, err, result.Diagnostics)
		}
		program, ok := result.Program()
		if !ok {
			t.Fatal("missing Program")
		}
		return program
	}
	first := compile("first", "one")
	second := compile("second", "two")
	third := compile("third", "three")
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	store, err := workflowstore.OpenProgramStore(
		t.TempDir(), builtins.Catalog, builtins.ConfigValidators, build,
		workflowstore.ProgramStoreOptions{
			MaxPrograms: 2, MaxBytes: 4 << 20,
			Now: func() time.Time {
				now = now.Add(time.Minute)
				return now
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(first.Hash()); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(second.Hash()); !errors.Is(err, workflowstore.ErrProgramNotFound) {
		t.Fatalf("least recently used Program remained: %v", err)
	}
	if _, err := store.Load(first.Hash()); err != nil {
		t.Fatalf("recently touched Program was evicted: %v", err)
	}
	if _, err := store.Load(third.Hash()); err != nil {
		t.Fatalf("new Program was not cached: %v", err)
	}

	byteStore, err := workflowstore.OpenProgramStore(
		t.TempDir(), builtins.Catalog, builtins.ConfigValidators, build,
		workflowstore.ProgramStoreOptions{
			MaxPrograms: 8,
			MaxBytes:    int64(len(first.Artifact()) + len(second.Artifact()) - 1),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := byteStore.Put(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := byteStore.Put(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := byteStore.Load(first.Hash()); !errors.Is(err, workflowstore.ErrProgramNotFound) {
		t.Fatalf("byte quota did not evict oldest Program: %v", err)
	}
}

func testWorkflowRepository(t *testing.T) *catalog.WorkflowRepository {
	t.Helper()
	roots, err := storage.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := catalog.Open(context.Background(), roots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := foundation.Close(); err != nil {
			t.Errorf("close Catalog = %v", err)
		}
	})
	return foundation.Workflows()
}

func openTestSourceStore(t *testing.T, repository *catalog.WorkflowRepository, maximum int) *workflowstore.SourceStore {
	t.Helper()
	store, err := workflowstore.OpenSourceStore(repository, workflowstore.SourceStoreOptions{MaxSources: maximum})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func programCachePath(t *testing.T, root string, hash artifact.Digest) string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(root, entry.Name(), strings.TrimPrefix(hash.String(), "sha256:")+".json")
		}
	}
	t.Fatal("Program cache generation was not created")
	return ""
}

func concatSource(t *testing.T, revision int, a, b string) []byte {
	t.Helper()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	ref := builtins.ConcatContract.NodeRef()
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-store","name":"Store"},
		"revision":%d,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"concat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"a":{"kind":"value","value":%q},"b":{"kind":"value","value":%q}}}
		],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, revision, ref.NodeTypeID, ref.SemanticDigest, a, b))
}

func testDigest(t *testing.T, label string) artifact.Digest {
	t.Helper()
	digest, err := artifact.Sum("yotta/test/workflowstore/v1", []byte(label))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
