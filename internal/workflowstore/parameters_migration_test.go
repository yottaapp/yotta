package workflowstore

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestParameterMigrationReadsReleasedV1WithoutInventingInputs(t *testing.T) {
	current := currentMigrationTestSource(t)
	legacy := bytes.Replace(current, []byte(`"version":"5"`), []byte(`"version":"1"`), 1)
	migrated, changed, err := MigrateSourceArtifact(legacy)
	if err != nil || !changed {
		t.Fatalf("migration=%v %v", changed, err)
	}
	source, diagnostics := schema.ParseSource(migrated)
	if schema.HasErrors(diagnostics) || source.Version != schema.Version {
		t.Fatalf("diagnostics=%v", diagnostics)
	}
	for _, variable := range source.Variables {
		if variable.Parameter != nil {
			t.Fatal("migration invented exposure")
		}
	}
	_, changed, err = MigrateSourceArtifact(migrated)
	if err != nil || changed {
		t.Fatalf("migration not idempotent: %v %v", changed, err)
	}
}

func TestParameterBlocksMigrationReadsV2(t *testing.T) {
	current := currentMigrationTestSource(t)
	legacy := bytes.Replace(current, []byte(`"version":"5"`), []byte(`"version":"2"`), 1)
	migrated, changed, err := MigrateSourceArtifact(legacy)
	if err != nil || !changed {
		t.Fatalf("migration=%v %v", changed, err)
	}
	source, diagnostics := schema.ParseSource(migrated)
	if schema.HasErrors(diagnostics) || len(source.ParameterBlocks) != 0 {
		t.Fatalf("invalid migration %v", diagnostics)
	}
}

func TestParameterDescriptionsMigrationPreservesV3Layout(t *testing.T) {
	current := currentMigrationTestSource(t)
	source, diagnostics := schema.ParseSource(current)
	if schema.HasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	source.Version = "3"
	source.ParameterBlocks = []schema.ParameterBlock{{ID: "heading", Kind: "label", Label: "Task", Order: 2}}
	legacy, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	migrated, changed, err := MigrateSourceArtifact(legacy)
	if err != nil || !changed {
		t.Fatalf("migration=%v %v", changed, err)
	}
	restored, diagnostics := schema.ParseSource(migrated)
	if schema.HasErrors(diagnostics) || len(restored.ParameterBlocks) != 1 || restored.ParameterBlocks[0].Label != "Task" || restored.ParameterBlocks[0].Order != 2 {
		t.Fatalf("layout lost: %v %v", restored.ParameterBlocks, diagnostics)
	}
}
