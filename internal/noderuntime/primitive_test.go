package noderuntime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestPrimitiveInlineAdaptersCompileAndExecuteNominalValues(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	add, _ := builtins.Definition(nodes.AddNodeID)
	greater, _ := builtins.Definition(nodes.GreaterThanNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-primitives","name":"Primitives"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"add","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"a":{"kind":"value","value":2},"b":{"kind":"value","value":3}}},
			{"id":"greater","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},
			 "bindings":{"b":{"kind":"value","value":4}}}
		],"edges":[{"channel":"data","from":{"nodeId":"add","portId":"result"},"to":{"nodeId":"greater","portId":"a"}}],
		"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, add.Contract.NodeRef().NodeTypeID, add.Contract.NodeRef().SemanticDigest, greater.Contract.NodeRef().NodeTypeID, greater.Contract.NodeRef().SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
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
	var got bool
	if err := json.Unmarshal(execution.NodeOutputs["greater"]["result"].InlineJSON(), &got); err != nil || !got {
		t.Fatalf("greater result = %t, %v", got, err)
	}
	if journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("Run status = %s", journal.Current().Status())
	}
}

func TestPrimitiveUnrepresentableResultPersistsDeclaredTerminalFailureWithoutInventedPort(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	add, _ := builtins.Definition(nodes.AddNodeID)
	ref := add.Contract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-overflow","name":"Overflow"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"add","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			"bindings":{"a":{"kind":"value","value":9007199254740991},"b":{"kind":"value","value":9007199254740991}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(time.Second) }}).Run(context.Background(), program, owner, journal)
	failure, failed := journal.Current().Failure()
	if runErr == nil || journal.Current().Status() != run.StatusFailed || !failed || failure.Code != "math.result_not_representable" {
		t.Fatalf("overflow Run error=%v record=%#v", runErr, journal.Current())
	}
	if len(add.Contract.Machine().Ports.ErrorOutputs) != 0 {
		t.Fatal("pure arithmetic invented an error control port")
	}
}

func compilePrimitiveProgram(t *testing.T, builtins nodes.Builtins, source []byte) compiler.ProgramSnapshot {
	t.Helper()
	build, err := artifact.Sum("yotta/test/compiler-build/v1", []byte(t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(context.Background(), compiler.CompileRequest{SourceJSON: source, Catalog: builtins.Catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile=%v diagnostics=%#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("compiler did not produce a Program")
	}
	opened, err := compiler.OpenProgram(program.Artifact(), builtins.Catalog, builtins.ConfigValidators, build)
	if err != nil {
		t.Fatalf("reopen Program: %v", err)
	}
	return opened
}
