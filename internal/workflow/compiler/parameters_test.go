package compiler

import (
	"encoding/json"
	"testing"

	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestParameterSnapshotPreservesDefaultsAndIsolatesRuns(t *testing.T) {
	variables := []schema.Variable{{Name: "companion", Default: json.RawMessage(`"a"`), Parameter: &schema.Parameter{
		ID: "companion-id", Label: "Companion", Control: "select",
		Options: []schema.ParameterOption{{Label: "A", Value: json.RawMessage(`"a"`)}, {Label: "B", Value: json.RawMessage(`"b"`)}},
	}}}
	values := map[string]json.RawMessage{"companion-id": json.RawMessage(`"b"`), "removed": json.RawMessage(`123`)}
	first, diagnostics := resolveParameterValues(variables, values)
	if len(diagnostics) != 0 || string(first[0].Default) != `"b"` {
		t.Fatalf("first=%+v diagnostics=%+v", first, diagnostics)
	}
	values["companion-id"][1] = 'a'
	second, diagnostics := resolveParameterValues(variables, values)
	if len(diagnostics) != 0 || string(first[0].Default) != `"b"` || string(second[0].Default) != `"a"` || string(variables[0].Default) != `"a"` {
		t.Fatal("run snapshots leaked into another run or the source")
	}
	variables[0].Parameter.Label = "Renamed label"
	renamed, _ := resolveParameterValues(variables, map[string]json.RawMessage{"companion-id": json.RawMessage(`"b"`)})
	if string(renamed[0].Default) != `"b"` {
		t.Fatal("renaming lost saved value")
	}
	_, diagnostics = resolveParameterValues(variables, map[string]json.RawMessage{"companion-id": json.RawMessage(`"deleted-option"`)})
	if len(diagnostics) != 1 || diagnostics[0].Params["parameterId"] != "companion-id" {
		t.Fatal("invalid saved option silently fell back")
	}
}
