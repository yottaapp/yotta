package compiler

import (
	"context"
	"fmt"
	"testing"

	"github.com/yottaapp/yotta/internal/nodes"
)

func TestCompilerRejectsRunRootOutsideTheEntryGraph(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	started, ok := builtins.Definition(nodes.RunStartedNodeID)
	if !ok {
		t.Fatal("RunStarted definition is missing")
	}
	ref := started.Contract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-run-root-placement","name":"Run root placement"},
		"revision":0,"entryGraph":"main","graphs":[
			{"id":"main","kind":"main","nodes":[],"edges":[],"inputs":[],"outputs":[]},
			{"id":"child","kind":"subgraph","nodes":[
				{"id":"child-root","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}}
			],"edges":[],"inputs":[],"outputs":[]}
		],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.Version, ref.SemanticDigest))
	result, err := New(testDigest(t, "run-root-placement"), builtins.ConfigValidators).CompileDraft(
		context.Background(), CompileRequest{SourceJSON: source, Catalog: builtins.Catalog},
	)
	if err != nil || !hasDiagnostic(result.Diagnostics, CodeInvalidInstructionPlacement) {
		t.Fatalf("run root placement diagnostics=%#v err=%v", result.Diagnostics, err)
	}
	if _, ok := result.Program(); ok {
		t.Fatal("compiler produced a Program with a subgraph run root")
	}
}
