package authoring_test

import (
	"bytes"
	"encoding/json"
	"testing"

	runtimejsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
)

func TestGeneratedPatchSchemaUsesExactTaggedUnion(t *testing.T) {
	raw, err := authoring.GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Definitions map[string]struct {
			OneOf []json.RawMessage `json:"oneOf"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if got := len(document.Definitions["Command"].OneOf); got != 40 {
		t.Fatalf("command variants = %d", got)
	}
	if !bytes.Contains(raw, []byte(`"additionalProperties": false`)) ||
		!bytes.Contains(raw, []byte(`"const": "connect"`)) ||
		!bytes.Contains(raw, []byte(`"const": "bind-resource"`)) ||
		!bytes.Contains(raw, []byte(`"const": "add-resource"`)) ||
		!bytes.Contains(raw, []byte(`"const": "replace-resource"`)) ||
		!bytes.Contains(raw, []byte(`"const": "update-resource-metadata"`)) ||
		!bytes.Contains(raw, []byte(`"const": "remove-resource"`)) ||
		!bytes.Contains(raw, []byte(`"connect"`)) {
		t.Fatalf("schema is not an exact tagged union: %s", raw)
	}
}

func TestGeneratedPatchSchemaAcceptsSamePatchNodeHandleInConnection(t *testing.T) {
	raw, err := authoring.GenerateSchema()
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument map[string]any
	if err := json.Unmarshal(raw, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := runtimejsonschema.NewCompiler()
	resource, ok := schemaDocument["$id"].(string)
	if !ok || resource == "" {
		t.Fatal("generated authoring contract omitted $id")
	}
	if err := compiler.AddResource(resource, schemaDocument); err != nil {
		t.Fatal(err)
	}
	validator, err := compiler.Compile(resource)
	if err != nil {
		t.Fatal(err)
	}
	var request any
	if err := json.Unmarshal([]byte(`{
		"workflowId": "workflow",
		"baseRevision": 0,
		"commands": [
			{
				"kind": "add-node",
				"addNode": {
					"graphId": "main",
					"nodeTypeId": "https://schemas.yotta.dev/nodes/control/delay",
					"handle": "delay",
					"position": {"x": 0, "y": 0}
				}
			},
			{
				"kind": "set-config",
				"setConfig": {
					"graphId": "main",
					"nodeId": "$delay",
					"fieldId": "duration-milliseconds",
					"value": 1000
				}
			},
			{
				"kind": "connect",
				"connect": {
					"graphId": "main",
					"edge": {
						"channel": "exec",
						"from": {"nodeId": "run-started", "portId": "started"},
						"to": {"nodeId": "$delay", "portId": "in"}
					}
				}
			}
		]
	}`), &request); err != nil {
		t.Fatal(err)
	}
	if err := validator.Validate(request); err != nil {
		t.Fatalf("same-patch node handle rejected by authoring contract: %v", err)
	}
}
