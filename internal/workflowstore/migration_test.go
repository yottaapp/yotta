package workflowstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestSourceMigrationPlanRunsOnlyRegisteredVersionChain(t *testing.T) {
	plan, err := newSourceMigrationPlan(sourceContract{Format: schema.Format, Version: "3"}, []sourceMigrationStep{
		{
			From:  sourceContract{Format: schema.Format, Version: "1"},
			To:    sourceContract{Format: schema.Format, Version: "2"},
			Apply: rewriteSourceVersion("2"),
		},
		{
			From:  sourceContract{Format: schema.Format, Version: "2"},
			To:    sourceContract{Format: schema.Format, Version: "3"},
			Apply: rewriteSourceVersion("3"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"format":"yotta.workflow","version":"1","kept":true}`)
	migrated, changed, err := plan.Migrate(original)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || bytes.Equal(migrated, original) {
		t.Fatalf("migration result changed=%v artifact=%s", changed, migrated)
	}
	var document struct {
		Version string `json:"version"`
		Kept    bool   `json:"kept"`
	}
	if err := json.Unmarshal(migrated, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "3" || !document.Kept {
		t.Fatalf("migrated document = %#v", document)
	}
}

func TestSourceMigrationPlanRejectsUnavailableAndConfusedMigrations(t *testing.T) {
	current := sourceContract{Format: schema.Format, Version: schema.Version}
	plan, err := newSourceMigrationPlan(current, nil)
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"format":"yotta.workflow","version":"0"}`)
	if _, _, err := plan.Migrate(original); !errors.Is(err, errSourceMigrationUnavailable) {
		t.Fatalf("unregistered migration error = %v", err)
	}

	confused, err := newSourceMigrationPlan(current, []sourceMigrationStep{{
		From:  sourceContract{Format: schema.Format, Version: "0"},
		To:    current,
		Apply: rewriteSourceVersion("9.9"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := confused.Migrate(original); err == nil {
		t.Fatal("migration output with an undeclared version was accepted")
	}
}

func TestCurrentSourceMigrationPlanDoesNotRepairDevelopmentArtifacts(t *testing.T) {
	plan, err := currentSourceMigrationPlan()
	if err != nil {
		t.Fatal(err)
	}
	developmentArtifact := []byte(`{"format":"yotta.workflow","version":"3.1"}`)
	if _, _, err := plan.Migrate(developmentArtifact); !errors.Is(err, errSourceMigrationUnavailable) {
		t.Fatalf("development artifact migration error = %v", err)
	}
}

func TestOpenSourceStoreDurablyPublishesRegisteredMigration(t *testing.T) {
	roots, err := storage.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := catalog.Open(context.Background(), roots)
	if err != nil {
		t.Fatal(err)
	}
	repository := foundation.Workflows()
	current := currentMigrationTestSource(t)
	legacy := bytes.Replace(current, []byte(`"version":"5"`), []byte(`"version":"0"`), 1)
	legacyHash, err := artifact.Sum("yotta/test/legacy-workflow-source/v1", legacy)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC)
	if err := repository.Commit(context.Background(), -1, catalog.WorkflowSourceRecord{
		WorkflowID: "wf-migrate", Name: "Migrate", Revision: 0, Hash: legacyHash,
		Format: schema.Format, Version: "0", Artifact: legacy, CreatedAt: now, UpdatedAt: now,
	}, nil); err != nil {
		t.Fatal(err)
	}
	plan, err := newSourceMigrationPlan(sourceContract{Format: schema.Format, Version: schema.Version}, []sourceMigrationStep{{
		From:  sourceContract{Format: schema.Format, Version: "0"},
		To:    sourceContract{Format: schema.Format, Version: schema.Version},
		Apply: rewriteSourceVersion(schema.Version),
	}})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openSourceStore(repository, SourceStoreOptions{MaxSources: 4, Now: func() time.Time { return now.Add(time.Hour) }}, plan)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := opened.Load("wf-migrate")
	if err != nil || snapshot.Revision() != 0 || snapshot.Hash() == legacyHash {
		t.Fatalf("migrated snapshot = %#v, %v", snapshot, err)
	}
	persisted, found, err := repository.Get(context.Background(), "wf-migrate")
	if err != nil || !found || persisted.Version != schema.Version || persisted.Hash != snapshot.Hash() ||
		!bytes.Equal(persisted.Artifact, snapshot.Artifact()) {
		t.Fatalf("persisted migration = %#v, found=%v, err=%v", persisted, found, err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedFoundation, err := catalog.Open(context.Background(), roots)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedFoundation.Close()
	reopened, err := OpenSourceStore(reopenedFoundation.Workflows(), SourceStoreOptions{MaxSources: 4})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := reopened.Load("wf-migrate")
	if err != nil || reloaded.Hash() != snapshot.Hash() || reloaded.Revision() != 0 {
		t.Fatalf("reopened migration = %#v, %v", reloaded, err)
	}
}

func currentMigrationTestSource(t *testing.T) []byte {
	t.Helper()
	raw := []byte(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-migrate","name":"Migrate"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[],"edges":[],"inputs":[],"outputs":[]}],
		"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`)
	_, canonical, _, diagnostics, err := schema.CanonicalSource(raw)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("build migration fixture: diagnostics=%#v, err=%v", diagnostics, err)
	}
	return canonical
}

func rewriteSourceVersion(version string) sourceMigration {
	return func(raw []byte) ([]byte, error) {
		var document map[string]json.RawMessage
		if err := json.Unmarshal(raw, &document); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(version)
		if err != nil {
			return nil, err
		}
		document["version"] = encoded
		return json.Marshal(document)
	}
}
