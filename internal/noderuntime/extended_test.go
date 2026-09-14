package noderuntime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestGeneratedBreakNodeCompilesReopensAndExecutesTypedFields(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := builtins.Definition(nodes.BreakPointNodeID)
	if !ok {
		t.Fatal("BreakPoint definition is missing")
	}
	ref := definition.Contract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-break-point","name":"Break point"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"break","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			"bindings":{"value":{"kind":"value","value":{"x":0.25,"y":0.75,"unit":"ratio"}}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.Version, ref.SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(
		builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(time.Second) }},
	).Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	outputs := execution.NodeOutputs["break"]
	if string(outputs["unit"].InlineJSON()) != `"ratio"` || string(outputs["x"].InlineJSON()) != `0.25` ||
		string(outputs["y"].InlineJSON()) != `0.75` {
		t.Fatalf("break outputs = %#v", outputs)
	}
	if journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("Run status = %s", journal.Current().Status())
	}
}

func TestTypedSelectResolvesFromItsConsumerAndExecutesWithoutCoercion(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	selectDefinition, _ := builtins.Definition(nodes.SelectNodeID)
	concatDefinition, _ := builtins.Definition(nodes.ConcatNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-select","name":"Select"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"select","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"condition":{"kind":"value","value":true},"when_true":{"kind":"value","value":"typed"},"when_false":{"kind":"value","value":"wrong"}}},
			{"id":"concat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},
			 "bindings":{"b":{"kind":"value","value":"!"}}}
		],"edges":[{"channel":"data","from":{"nodeId":"select","portId":"result"},"to":{"nodeId":"concat","portId":"a"}}],
		"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, selectDefinition.Contract.NodeRef().NodeTypeID, selectDefinition.Contract.NodeRef().SemanticDigest,
		concatDefinition.Contract.NodeRef().NodeTypeID, concatDefinition.Contract.NodeRef().SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	for _, view := range program.Nodes() {
		if view.ID != "select" {
			continue
		}
		for _, portID := range []string{"when_true", "when_false"} {
			resolved := view.InputTypes[portID]
			if resolved.Kind != datatype.ResolvedTypeRef || resolved.Ref == nil || *resolved.Ref != builtins.StringType.TypeRef() {
				t.Fatalf("%s effective type = %#v", portID, resolved)
			}
		}
	}
	now := time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(time.Second) }}).Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	var result string
	if err := json.Unmarshal(execution.NodeOutputs["concat"]["result"].InlineJSON(), &result); err != nil || result != "typed!" {
		t.Fatalf("result = %q, %v", result, err)
	}
}

func TestTextNodesCompileReopenAndExecuteUnicodePipeline(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	substring, _ := builtins.Definition(nodes.SubstringNodeID)
	concat, _ := builtins.Definition(nodes.ConcatNodeID)
	length, _ := builtins.Definition(nodes.LengthNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-text","name":"Text"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"substring","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"text":{"kind":"value","value":"a节点b"},"start":{"kind":"value","value":1},"length":{"kind":"value","value":2}}},
			{"id":"concat","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":1,"y":0},"config":{},
			 "bindings":{"b":{"kind":"value","value":"!"}}},
			{"id":"length","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":2,"y":0},"config":{},"bindings":{}}
		],"edges":[
			{"channel":"data","from":{"nodeId":"substring","portId":"result"},"to":{"nodeId":"concat","portId":"a"}},
			{"channel":"data","from":{"nodeId":"concat","portId":"result"},"to":{"nodeId":"length","portId":"text"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, substring.Contract.NodeRef().NodeTypeID, substring.Contract.NodeRef().Version, substring.Contract.NodeRef().SemanticDigest,
		concat.Contract.NodeRef().NodeTypeID, concat.Contract.NodeRef().Version, concat.Contract.NodeRef().SemanticDigest,
		length.Contract.NodeRef().NodeTypeID, length.Contract.NodeRef().Version, length.Contract.NodeRef().SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(
		builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(time.Second) }},
	).Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(execution.NodeOutputs["length"]["result"].InlineJSON()); got != "3" {
		t.Fatalf("text pipeline length = %s, want 3", got)
	}
	if journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("Run status = %s", journal.Current().Status())
	}
}

func TestJSONNodesCompileReopenAndExecuteCanonicalPipeline(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	parse, _ := builtins.Definition(nodes.ParseJSONNodeID)
	path, _ := builtins.Definition(nodes.JSONPathNodeID)
	stringify, _ := builtins.Definition(nodes.ToJSONNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-json","name":"JSON"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"parse","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"text":{"kind":"value","value":"{\"items\":[{\"name\":\"节点\"}]}"}}},
			{"id":"path","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":1,"y":0},"config":{},
			 "bindings":{"path":{"kind":"value","value":"$.items[0].name"}}},
			{"id":"stringify","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":2,"y":0},"config":{},"bindings":{}}
		],"edges":[
			{"channel":"data","from":{"nodeId":"parse","portId":"result"},"to":{"nodeId":"path","portId":"json"}},
			{"channel":"data","from":{"nodeId":"path","portId":"result"},"to":{"nodeId":"stringify","portId":"value"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, parse.Contract.NodeRef().NodeTypeID, parse.Contract.NodeRef().Version, parse.Contract.NodeRef().SemanticDigest,
		path.Contract.NodeRef().NodeTypeID, path.Contract.NodeRef().Version, path.Contract.NodeRef().SemanticDigest,
		stringify.Contract.NodeRef().NodeTypeID, stringify.Contract.NodeRef().Version, stringify.Contract.NodeRef().SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(
		builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(time.Second) }},
	).Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(execution.NodeOutputs["stringify"]["result"].InlineJSON(), &got); err != nil || got != `"节点"` {
		t.Fatalf("JSON pipeline result = %q, %v", got, err)
	}
	if journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("Run status = %s", journal.Current().Status())
	}
}

func TestGeometryNodesCompileReopenAndExecuteTypedPipeline(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	makePoint, _ := builtins.Definition(nodes.MakePointNodeID)
	offset, _ := builtins.Definition(nodes.OffsetPointNodeID)
	region, _ := builtins.Definition(nodes.RegionAroundPointNodeID)
	breakRegion, _ := builtins.Definition(nodes.BreakRegionNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-geometry","name":"Geometry"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"make","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"x":{"kind":"value","value":0.8},"y":{"kind":"value","value":0.1},"unit":{"kind":"value","value":"ratio"}}},
			{"id":"offset","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":1,"y":0},"config":{},
			 "bindings":{"offset_x":{"kind":"value","value":0.5},"offset_y":{"kind":"value","value":-0.5}}},
			{"id":"region","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":2,"y":0},"config":{},
			 "bindings":{"width":{"kind":"value","value":0.4},"height":{"kind":"value","value":0.2}}},
			{"id":"break","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":3,"y":0},"config":{},"bindings":{}}
		],"edges":[
			{"channel":"data","from":{"nodeId":"make","portId":"result"},"to":{"nodeId":"offset","portId":"point"}},
			{"channel":"data","from":{"nodeId":"offset","portId":"result"},"to":{"nodeId":"region","portId":"center"}},
			{"channel":"data","from":{"nodeId":"region","portId":"result"},"to":{"nodeId":"break","portId":"value"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, makePoint.Contract.NodeRef().NodeTypeID, makePoint.Contract.NodeRef().Version, makePoint.Contract.NodeRef().SemanticDigest,
		offset.Contract.NodeRef().NodeTypeID, offset.Contract.NodeRef().Version, offset.Contract.NodeRef().SemanticDigest,
		region.Contract.NodeRef().NodeTypeID, region.Contract.NodeRef().Version, region.Contract.NodeRef().SemanticDigest,
		breakRegion.Contract.NodeRef().NodeTypeID, breakRegion.Contract.NodeRef().Version, breakRegion.Contract.NodeRef().SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(
		builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(time.Second) }},
	).Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	outputs := execution.NodeOutputs["break"]
	if string(outputs["unit"].InlineJSON()) != `"ratio"` ||
		string(outputs["x"].InlineJSON()) != `0.6` ||
		string(outputs["y"].InlineJSON()) != `0` ||
		string(outputs["width"].InlineJSON()) != `0.4` ||
		string(outputs["height"].InlineJSON()) != `0.2` {
		t.Fatalf("geometry outputs = %#v", outputs)
	}
	if journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("Run status = %s", journal.Current().Status())
	}
}
