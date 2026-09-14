package compiler

import (
	"testing"

	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestEffectiveNodeConfigResolvesWorkflowDefaultAndNodeOverride(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := builtins.Catalog.Lookup(nodes.ClickPointerNodeID)
	if !ok {
		t.Fatal("click pointer node is missing")
	}
	machine := entry.Contract.Machine()
	defaults := []schema.TargetDefault{{Target: "target", Slot: "workflow-target"}}

	inherited, err := effectiveNodeConfig(defaults, schema.Node{Config: map[string]any{}}, machine)
	if err != nil || inherited["slot"] != "workflow-target" {
		t.Fatalf("inherited=%+v err=%v", inherited, err)
	}
	if len(machine.ConfiguredTargets) == 0 || inherited[machine.ConfiguredTargets[0].SlotConfigKey] != "workflow-target" {
		t.Fatalf("configured targets=%+v config=%+v", machine.ConfiguredTargets, inherited)
	}

	overridden, err := effectiveNodeConfig(defaults, schema.Node{Config: map[string]any{"slot": "node-target"}}, machine)
	if err != nil || overridden["slot"] != "node-target" {
		t.Fatalf("overridden=%+v err=%v", overridden, err)
	}

	missing, err := effectiveNodeConfig(nil, schema.Node{Config: map[string]any{}}, machine)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := missing["slot"]; ok {
		t.Fatalf("missing config unexpectedly contains target slot: %+v", missing)
	}
}

func TestApplicationInheritsUnifiedWorkflowTarget(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := builtins.Catalog.Lookup("https://schemas.yotta.dev/nodes/application/launch")
	if !ok {
		t.Fatal("launch node missing")
	}
	config, err := effectiveNodeConfig([]schema.TargetDefault{{Target: "target", Slot: "launcher"}}, schema.Node{Config: map[string]any{}}, entry.Contract.Machine())
	if err != nil || config["slot"] != "launcher" {
		t.Fatalf("application default: %+v %v", config, err)
	}
	config, err = effectiveNodeConfig([]schema.TargetDefault{{Target: "target", Slot: "launcher"}}, schema.Node{Config: map[string]any{"slot": "second-app"}}, entry.Contract.Machine())
	if err != nil || config["slot"] != "second-app" {
		t.Fatalf("application override: %+v %v", config, err)
	}
}
