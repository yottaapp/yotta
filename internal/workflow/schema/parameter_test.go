package schema

import (
	"encoding/json"
	"testing"
)

func TestParametersRejectBrokenDependenciesAndSelections(t *testing.T) {
	variables := []Variable{
		{Name: "a", Default: json.RawMessage(`true`), Parameter: &Parameter{ID: "a", Label: "A", Control: "auto", VisibleWhen: &ParameterCondition{ParameterID: "b", Equals: json.RawMessage(`true`)}}},
		{Name: "b", Default: json.RawMessage(`true`), Parameter: &Parameter{ID: "b", Label: "B", Control: "auto", VisibleWhen: &ParameterCondition{ParameterID: "a", Equals: json.RawMessage(`true`)}}},
	}
	if !HasErrors(validateParameters(variables)) {
		t.Fatal("visibility cycle accepted")
	}
	variables[1].Parameter.VisibleWhen = nil
	if HasErrors(validateParameters(variables)) {
		t.Fatal("valid visibility rejected")
	}
	p := Parameter{ID: "choice", Label: "Choice", Control: "multiselect", Required: true, Options: []ParameterOption{{Label: "A", Value: json.RawMessage(`"a"`)}}}
	for _, value := range []string{`[]`, `["a","a"]`, `["removed"]`, `"a"`} {
		if p.ValidateValue(json.RawMessage(value)) == nil {
			t.Fatalf("invalid selection accepted: %s", value)
		}
	}
	if p.ValidateValue(json.RawMessage(`["a"]`)) != nil {
		t.Fatal("valid selection rejected")
	}
}

func TestParameterBlocksDoNotDeclareVariablesAndRejectDuplicateIDs(t *testing.T) {
	source := WorkflowSource{ParameterBlocks: []ParameterBlock{{ID: "heading", Kind: "label"}, {ID: "line", Kind: "separator"}}}
	if HasErrors(validateParameterBlocks(source)) || len(source.Variables) != 0 {
		t.Fatal("presentation should not create variables")
	}
	source.ParameterBlocks[1].ID = "heading"
	if !HasErrors(validateParameterBlocks(source)) {
		t.Fatal("duplicate IDs accepted")
	}
}
