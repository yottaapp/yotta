package noderuntime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestCollectionTypeVariablesFreezeIntoProgramAndExecute(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	split, _ := builtins.Definition(nodes.SplitNodeID)
	get, _ := builtins.Definition(nodes.ListGetNodeID)
	concat, _ := builtins.Definition(nodes.ConcatNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-collection","name":"Collection"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"split","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"text":{"kind":"value","value":"a,节点"},"separator":{"kind":"value","value":","}}},
			{"id":"get","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},
			 "bindings":{"index":{"kind":"value","value":1}}},
			{"id":"concat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":2,"y":0},"config":{},
			 "bindings":{"b":{"kind":"value","value":"!"}}}
		],"edges":[
			{"channel":"data","from":{"nodeId":"split","portId":"result"},"to":{"nodeId":"get","portId":"list"}},
			{"channel":"data","from":{"nodeId":"get","portId":"result"},"to":{"nodeId":"concat","portId":"a"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, split.Contract.NodeRef().NodeTypeID, split.Contract.NodeRef().SemanticDigest,
		get.Contract.NodeRef().NodeTypeID, get.Contract.NodeRef().SemanticDigest,
		concat.Contract.NodeRef().NodeTypeID, concat.Contract.NodeRef().SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	views := map[string]compiler.NodeView{}
	for _, view := range program.Nodes() {
		views[view.ID] = view
	}
	stringType := datatype.RefResolvedType(builtins.StringType.TypeRef())
	if got := views["split"].OutputTypes["result"]; got.Kind != datatype.ResolvedTypeList || got.Element == nil || got.Element.Ref == nil || *got.Element.Ref != builtins.StringType.TypeRef() {
		t.Fatalf("split effective output = %#v", got)
	}
	if got := views["get"].OutputTypes["result"]; got.Ref == nil || *got.Ref != *stringType.Ref {
		t.Fatalf("get effective output = %#v", got)
	}

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
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
	if err := json.Unmarshal(execution.NodeOutputs["concat"]["result"].InlineJSON(), &result); err != nil || result != "节点!" {
		t.Fatalf("concat result = %q, %v", result, err)
	}
}

func TestUnresolvedCollectionVariableFailsAtCompileBoundary(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	length, _ := builtins.Definition(nodes.ListLengthNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-unresolved","name":"Unresolved"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"length","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, length.Contract.NodeRef().NodeTypeID, length.Contract.NodeRef().SemanticDigest))
	build, err := artifact.Sum("yotta/test/compiler-build/v1", []byte(t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(context.Background(), compiler.CompileRequest{SourceJSON: source, Catalog: builtins.Catalog})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, diagnostic := range compiled.Diagnostics {
		if diagnostic.Code == compiler.CodeUnresolvedType {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %#v", compiled.Diagnostics)
	}
}
