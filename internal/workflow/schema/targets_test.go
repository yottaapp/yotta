package schema

import (
	"strings"
	"testing"
)

func TestWorkflowTargetsValidateIdentityAndDefault(t *testing.T) {
	for _, targets := range [][]WorkflowTarget{
		{{ID: "game", Name: "Game", Kind: "automation"}},
		{{ID: "game", Name: "Game", Kind: "automation", Default: true}, {ID: "game", Name: "Other", Kind: "automation"}},
		{{ID: "game", Name: "Game", Kind: "automation", Default: true}, {ID: "launcher", Name: "Launcher", Kind: "configured-application", Default: true}},
		{{ID: "bad/id", Name: "Game", Kind: "automation", Default: true}},
		{{ID: "game", Name: " ", Kind: "automation", Default: true}},
		{{ID: "game", Name: "Game", Kind: "", Default: true}},
		{{ID: "game", Name: "Game", Kind: "automation", Description: strings.Repeat("x", 4097), Default: true}},
	} {
		if err := ValidateWorkflowTargets(targets); err == nil {
			t.Fatalf("accepted invalid targets: %+v", targets)
		}
	}
	source := WorkflowSource{Targets: []WorkflowTarget{{ID: "game", Name: "Game", Kind: "automation", Default: true}, {ID: "launcher", Name: "Launcher", Kind: "configured-application"}}}
	if err := SetTargetDefault(&source, "target", "launcher"); err != nil {
		t.Fatal(err)
	}
	if source.Targets[0].Default || !source.Targets[1].Default {
		t.Fatalf("default flags: %+v", source.Targets)
	}
	if got, _ := TargetDefaultSlot(source, "target"); got != "launcher" {
		t.Fatalf("default: %q", got)
	}
	if err := SetTargetDefault(&source, "target", "physical-slot"); err == nil {
		t.Fatal("accepted undeclared target")
	}
	if err := ClearTargetDefault(&source, "target"); err == nil {
		t.Fatal("cleared mandatory default identity")
	}
}

func TestWorkflowTargetDefaultCannotDisagreeWithInheritedSlot(t *testing.T) {
	raw := strings.Replace(validSource31ForTest(), `"variables":[]`, `"targets":[{"id":"game","name":"Game","kind":"automation","default":true}],"targetDefaults":[{"target":"target","slot":"other"}],"variables":[]`, 1)
	if _, diagnostics := ParseSource([]byte(raw)); !HasErrors(diagnostics) {
		t.Fatal("accepted contradictory default")
	}
	raw = strings.Replace(raw, `"slot":"other"`, `"slot":"game"`, 1)
	if _, diagnostics := ParseSource([]byte(raw)); HasErrors(diagnostics) {
		t.Fatalf("rejected coherent defaults: %+v", diagnostics)
	}
}
