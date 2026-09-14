package noderuntime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/navigationpath"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestPathNodesCompileAndPreserveTypedPointIdentity(t *testing.T) {
	b, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	reference := navigationpath.Reference{Kind: "world", Frame: "test/map", Unit: "raw", AxisHeading: 90, AxisSign: 1}
	points := []navigationpath.Point{{ID: "a", Name: "Entrance", X: 1, Y: 2}, {ID: "b", X: 3, Y: 4}, {ID: "c", Name: "Destination", X: 5, Y: 6}}
	node := func(id, op string, values map[string]any) map[string]any {
		definition, ok := b.Definition(nodes.PathNodePrefix + op)
		if !ok {
			t.Fatal(op)
		}
		bindings := map[string]any{}
		for k, v := range values {
			bindings[k] = map[string]any{"kind": "value", "value": v}
		}
		return map[string]any{"id": id, "nodeRef": definition.Contract.NodeRef(), "position": map[string]int{"x": 0, "y": 0}, "config": map[string]any{}, "bindings": bindings}
	}
	edge := func(from, to, in string) map[string]any {
		return map[string]any{"channel": "data", "from": map[string]string{"nodeId": from, "portId": "result"}, "to": map[string]string{"nodeId": to, "portId": in}}
	}
	source := map[string]any{"format": "yotta.workflow", "version": "5", "workflow": map[string]string{"id": "path-test", "name": "Path test"}, "revision": 0, "entryGraph": "main", "graphs": []any{map[string]any{"id": "main", "kind": "main", "nodes": []any{
		node("make", "make-path", map[string]any{"reference": reference, "points": points}),
		node("reverse", "reverse-path", map[string]any{}), node("get", "path-point", map[string]any{"index": 0}),
	}, "edges": []any{edge("make", "reverse", "path"), edge("reverse", "get", "path")}, "inputs": []any{}, "outputs": []any{}}}, "variables": []any{}, "resources": []any{}, "targetProfileDefinitions": []any{}, "credentialRequirements": []any{}, "dependencies": []any{}}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	program := compilePrimitiveProgram(t, b, raw)
	now := time.Now().UTC()
	_, owner, journal := admittedExecution(t, b, program, nil, now)
	defer owner.Close(context.Background())
	adapters, err := noderuntime.Installed(b, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(b.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(time.Second) }}).Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	var got navigationpath.Point
	if err := json.Unmarshal(execution.NodeOutputs["get"]["point"].InlineJSON(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "c" || got.Name != "Destination" || got.X != 5 || got.Z != nil {
		t.Fatalf("point lost identity or invented altitude: %+v", got)
	}
}
