package compiler

import (
	"context"
	"fmt"
	"testing"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/nodes"
)

func FuzzCompileDraft(f *testing.F) {
	build, _ := artifact.Sum("yotta/test/v1", []byte("compiler"))
	builtins, err := nodes.Build()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(fuzzSource(builtins.ConcatContract.NodeRef()))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = New(build, builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{SourceJSON: raw, Catalog: builtins.Catalog})
	})
}

func FuzzOpenProgram(f *testing.F) {
	build, _ := artifact.Sum("yotta/test/v1", []byte("compiler"))
	builtins, err := nodes.Build()
	if err != nil {
		f.Fatal(err)
	}
	compiled, err := New(build, builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{SourceJSON: fuzzSource(builtins.ConcatContract.NodeRef()), Catalog: builtins.Catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		f.Fatalf("seed compile: diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, _ := compiled.Program()
	f.Add(program.Artifact())
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = OpenProgram(raw, builtins.Catalog, builtins.ConfigValidators, build)
	})
}

func fuzzSource(ref nodecontract.NodeRef) []byte {
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"fuzz","name":"Fuzz"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"concat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},
			"config":{},"bindings":{"a":{"kind":"value","value":"a"},"b":{"kind":"value","value":"b"}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest))
}
