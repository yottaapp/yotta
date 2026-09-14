package authoring_test

import (
	"errors"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"testing"
)

func TestWorkflowTargetEditsPreserveReferencesAndDefault(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "activate" })
	if err != nil {
		t.Fatal(err)
	}
	targets := []schema.WorkflowTarget{{ID: "game", Name: "Game", Kind: "automation", Default: true}, {ID: "other", Name: "Other", Kind: "automation"}}
	first, err := engine.Apply(emptySource(), []authoring.Command{{Kind: authoring.CommandSetWorkflowTargets, SetWorkflowTargets: &authoring.SetWorkflowTargetsCommand{Targets: targets}}, {Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ActivateWindowNodeID}}, {Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "activate", FieldID: "slot", Value: "other"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Apply(first.Source, []authoring.Command{{Kind: authoring.CommandSetWorkflowTargets, SetWorkflowTargets: &authoring.SetWorkflowTargetsCommand{Targets: targets[:1]}}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "REFERENCE_IN_USE" {
		t.Fatalf("deleted used target: %v", err)
	}
	if len(first.Source.Targets) != 2 {
		t.Fatal("failed patch mutated source")
	}
	targets[0].Default = false
	targets[1].Default = true
	targets[1].Name = "Renamed"
	next, err := engine.Apply(first.Source, []authoring.Command{{Kind: authoring.CommandSetWorkflowTargets, SetWorkflowTargets: &authoring.SetWorkflowTargetsCommand{Targets: targets}}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := schema.TargetDefaultSlot(next.Source, "target"); got != "other" {
		t.Fatalf("default: %s", got)
	}
	if next.Source.Graphs[0].Nodes[0].Config["slot"] != "other" {
		t.Fatal("rename rewrote reference")
	}
}
