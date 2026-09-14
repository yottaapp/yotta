package compiler

import (
	"context"
	"fmt"
	"testing"

	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestCompileAllowsDisconnectedDraftNodesBesideReachableExecution(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	end, _ := builtins.Definition(nodes.EndBranchNodeID)
	concat, _ := builtins.Definition(nodes.ConcatNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"draft","name":"Draft"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","name":"Main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"config":{},"bindings":{},"position":{"x":0,"y":0}},
			{"id":"end","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"config":{},"bindings":{},"position":{"x":200,"y":0}},
			{"id":"draft-node","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"config":{},"bindings":{},"position":{"x":0,"y":200}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"end","portId":"in"}}],"inputs":[],"outputs":[]}],
		"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`,
		started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().Version, started.Contract.NodeRef().SemanticDigest,
		end.Contract.NodeRef().NodeTypeID, end.Contract.NodeRef().Version, end.Contract.NodeRef().SemanticDigest,
		concat.Contract.NodeRef().NodeTypeID, concat.Contract.NodeRef().Version, concat.Contract.NodeRef().SemanticDigest,
	))

	result, err := New(testDigest(t, "disconnected draft"), builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: source,
		Catalog:    builtins.Catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	program, ok := result.Program()
	if !ok {
		t.Fatalf("disconnected draft blocked Program: %#v", result.Diagnostics)
	}
	if schema.HasErrors(result.Diagnostics) {
		t.Fatalf("disconnected draft emitted blocking diagnostics: %#v", result.Diagnostics)
	}
	if len(result.Diagnostics) != 2 {
		t.Fatalf("disconnected draft diagnostics = %#v", result.Diagnostics)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code != CodeMissingInputBinding ||
			diagnostic.Severity != schema.SeverityWarning ||
			diagnostic.NodeID != "draft-node" {
			t.Fatalf("disconnected draft diagnostic = %#v", diagnostic)
		}
	}
	nodes := program.Nodes()
	if len(nodes) != 2 {
		t.Fatalf("Program nodes = %#v", nodes)
	}
	for _, node := range nodes {
		if node.ID == "draft-node" {
			t.Fatalf("disconnected draft leaked into Program: %#v", node)
		}
	}
}

func TestCompileLogWithConfiguredMessageDoesNotRequireDataInput(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	logNode, _ := builtins.Definition(nodes.LogNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"configured-log","name":"Configured log"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","name":"Main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"config":{},"bindings":{},"position":{"x":0,"y":0}},
			{"id":"log","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"config":{"message":"test","level":"info"},"bindings":{},"position":{"x":200,"y":0}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"log","portId":"in"}}],"inputs":[],"outputs":[]}],
		"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`,
		started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().Version, started.Contract.NodeRef().SemanticDigest,
		logNode.Contract.NodeRef().NodeTypeID, logNode.Contract.NodeRef().Version, logNode.Contract.NodeRef().SemanticDigest,
	))

	build := testDigest(t, "configured log")
	result, err := New(build, builtins.ConfigValidators).CompileDraft(
		context.Background(),
		CompileRequest{SourceJSON: source, Catalog: builtins.Catalog},
	)
	if err != nil {
		t.Fatal(err)
	}
	program, ok := result.Program()
	if !ok {
		t.Fatalf("configured log blocked Program: %#v", result.Diagnostics)
	}
	if _, err := OpenProgram(program.Artifact(), builtins.Catalog, builtins.ConfigValidators, build); err != nil {
		t.Fatalf("configured log Program did not reopen: %v", err)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == CodeUnresolvedType {
			t.Fatalf("configured log left message type unresolved: %#v", diagnostic)
		}
	}
}

func TestCompileLogResolvesConnectedObservableMessage(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	concat, _ := builtins.Definition(nodes.ConcatNodeID)
	logNode, _ := builtins.Definition(nodes.LogNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"connected-log","name":"Connected log"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","name":"Main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"config":{},"bindings":{},"position":{"x":0,"y":0}},
			{"id":"concat","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"config":{},"bindings":{"a":{"kind":"value","value":"hel"},"b":{"kind":"value","value":"lo"}},"position":{"x":0,"y":100}},
			{"id":"log","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"config":{"message":"fallback","level":"info"},"bindings":{},"position":{"x":200,"y":0}}
		],"edges":[
			{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"log","portId":"in"}},
			{"channel":"data","from":{"nodeId":"concat","portId":"result"},"to":{"nodeId":"log","portId":"message"}}
		],"inputs":[],"outputs":[]}],
		"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`,
		started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().Version, started.Contract.NodeRef().SemanticDigest,
		concat.Contract.NodeRef().NodeTypeID, concat.Contract.NodeRef().Version, concat.Contract.NodeRef().SemanticDigest,
		logNode.Contract.NodeRef().NodeTypeID, logNode.Contract.NodeRef().Version, logNode.Contract.NodeRef().SemanticDigest,
	))

	result, err := New(testDigest(t, "connected log"), builtins.ConfigValidators).CompileDraft(
		context.Background(),
		CompileRequest{SourceJSON: source, Catalog: builtins.Catalog},
	)
	if err != nil {
		t.Fatal(err)
	}
	program, ok := result.Program()
	if !ok {
		t.Fatalf("connected log blocked Program: %#v", result.Diagnostics)
	}
	for _, node := range program.Nodes() {
		if node.ID != "log" {
			continue
		}
		resolved, ok := node.InputTypes["message"]
		if !ok || resolved.Ref == nil || resolved.Ref.TypeID != nodes.StringTypeID {
			t.Fatalf("connected log message type = %#v", resolved)
		}
		return
	}
	t.Fatal("connected log node is missing from Program")
}
