package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestParametersFreezeBeforeRunWithoutChangingSource(t *testing.T) {
	app, sources, programs, builtins, _, _ := newTestApplication(t, time.Now(), nil)
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(ctx) })
	created, err := app.CreateSource(ctx, "Parameters")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := app.ApplyPatch(ctx, authoring.PatchRequest{WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(), Commands: []authoring.Command{{
		Kind: authoring.CommandAddStateVariable, AddStateVariable: &authoring.AddStateVariableCommand{
			Name: "person", Type: datatype.RefExpression(builtins.StringType.TypeRef()), Default: "a",
			Parameter: &schema.Parameter{ID: "person-id", Label: "Person", Control: "select", Options: []schema.ParameterOption{{Label: "A", Value: json.RawMessage(`"a"`)}, {Label: "B", Value: json.RawMessage(`"b"`)}}},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	diagnostics, err := app.SaveParameters(ctx, created.WorkflowID(), saved.Source.Revision(), map[string]json.RawMessage{"person-id": json.RawMessage(`"b"`)})
	if err != nil || schema.HasErrors(diagnostics) {
		t.Fatalf("save=%v %v", diagnostics, err)
	}
	first, err := app.StartDebugRun(ctx, appcore.StartRunRequest{WorkflowID: created.WorkflowID(), Principal: "user-1"}, nil)
	if err != nil || !first.Record.Valid() {
		t.Fatalf("start=%+v %v", first, err)
	}
	program, err := programs.Load(first.ProgramHash)
	if err != nil {
		t.Fatal(err)
	}
	frozen := program.Artifact()
	diagnostics, err = app.SaveParameters(ctx, created.WorkflowID(), saved.Source.Revision(), map[string]json.RawMessage{})
	if err != nil || schema.HasErrors(diagnostics) {
		t.Fatalf("reset=%v %v", diagnostics, err)
	}
	second, err := app.StartRun(ctx, appcore.StartRunRequest{WorkflowID: created.WorkflowID(), Principal: "user-1"})
	if err != nil || !second.Record.Valid() || first.ProgramHash == second.ProgramHash {
		t.Fatalf("second=%+v %v", second, err)
	}
	reopened, err := programs.Load(first.ProgramHash)
	if err != nil || !bytes.Equal(reopened.Artifact(), frozen) {
		t.Fatal("saved configuration mutated running program")
	}
	current, err := sources.Load(created.WorkflowID())
	if err != nil || current.Hash() != saved.Source.Hash() || first.SourceHash != current.Hash() || second.SourceHash != current.Hash() {
		t.Fatal("runtime parameters changed author defaults or source identity")
	}
	diagnostics, err = app.SaveParameters(ctx, created.WorkflowID(), saved.Source.Revision(), map[string]json.RawMessage{"person-id": json.RawMessage(`"deleted"`)})
	if err != nil || !schema.HasErrors(diagnostics) {
		t.Fatal("deleted option was silently accepted")
	}
	configuration, err := app.GetParameters(created.WorkflowID())
	if err != nil || len(configuration.Values) != 0 {
		t.Fatal("invalid update changed persisted values")
	}
}
