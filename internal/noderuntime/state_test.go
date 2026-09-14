package noderuntime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestStateReadIsBoundFromProgramStateAndJournaledAsAnEffect(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := builtins.Definition(nodes.StateReadNodeID)
	nodeRef, typeRef := definition.Contract.NodeRef(), builtins.StringType.TypeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-state-read","name":"State read"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"read","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},
			"config":{"variable":"message"},"bindings":{}
		}],"edges":[],"inputs":[],"outputs":[]}],
		"variables":[{"name":"message","type":{"kind":"ref","ref":{"typeId":%q,"semanticDigest":%q}},"default":"ready"}],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, nodeRef.NodeTypeID, nodeRef.SemanticDigest, typeRef.TypeID, typeRef.SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 7, 15, 14, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).
		Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := json.Unmarshal(execution.NodeOutputs["read"]["result"].InlineJSON(), &value); err != nil || value != "ready" {
		t.Fatalf("state value=%q err=%v", value, err)
	}
	found := false
	for _, fact := range journal.Current().Journal() {
		if fact.Kind == run.JournalAdapterAction && fact.EffectID == nodes.StateReadEffectID && fact.ActionOutcome == run.ActionSucceeded {
			found = true
		}
	}
	if !found {
		t.Fatal("state read did not persist its declared effect")
	}
}

func TestStateWriteSupportsEveryProjectedInitialType(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := nodeauthoring.Project(nodeauthoring.Input{
		Catalog: builtins.Catalog, Types: builtins.Types, Capabilities: builtins.Capabilities,
		Contracts: builtins.Contracts, GeneratorVersion: nodes.GeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	write, _ := builtins.Definition(nodes.StateWriteNodeID)
	type stateCase struct {
		name, typeJSON, initial string
	}
	cases := make([]stateCase, 0, len(builtins.Types)+1)
	for _, definition := range builtins.Types {
		projected, ok := projection.Type(definition.TypeRef().TypeID)
		if !ok || len(projected.StateInitial) == 0 {
			continue
		}
		ref := definition.TypeRef()
		cases = append(cases, stateCase{
			name: ref.TypeID,
			typeJSON: fmt.Sprintf(
				`{"kind":"ref","ref":{"typeId":%q,"semanticDigest":%q}}`,
				ref.TypeID,
				ref.SemanticDigest,
			),
			initial: string(projected.StateInitial),
		})
	}
	keyCode := builtins.KeyCodeType.TypeRef()
	cases = append(cases, stateCase{
		name: "key-chord",
		typeJSON: fmt.Sprintf(
			`{"kind":"list","element":{"kind":"ref","ref":{"typeId":%q,"semanticDigest":%q}}}`,
			keyCode.TypeID,
			keyCode.SemanticDigest,
		),
		initial: `["CTRL","S"]`,
	})
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(fmt.Sprintf(`{
				"format":"yotta.workflow","version":"5","workflow":{"id":"wf-state-matrix","name":"State matrix"},
				"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
					{"id":"started","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
					{"id":"write","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},
					 "config":{"variable":"value"},"bindings":{"value":{"kind":"value","value":%s}}}
				],"edges":[
					{"channel":"exec","from":{"nodeId":"started","portId":"started"},"to":{"nodeId":"write","portId":"in"}}
				],"inputs":[],"outputs":[]}],
				"variables":[{"name":"value","type":%s,"default":%s}],
				"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
			}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
				write.Contract.NodeRef().NodeTypeID, write.Contract.NodeRef().SemanticDigest,
				test.initial, test.typeJSON, test.initial))
			program := compilePrimitiveProgram(t, builtins, source)
			now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
			_, owner, journal := admittedExecution(t, builtins, program, nil, now)
			t.Cleanup(func() { _ = owner.Close(context.Background()) })
			adapters, err := noderuntime.Installed(builtins, testDependencies())
			if err != nil {
				t.Fatal(err)
			}
			execution, err := compiler.NewExecutor(
				builtins.Catalog,
				adapters,
				compiler.ExecutorOptions{Now: func() time.Time { return now }},
			).Run(context.Background(), program, owner, journal)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(execution.NodeOutputs["write"]["result"].InlineJSON()); got != test.initial {
				t.Fatalf("written state = %s, want %s", got, test.initial)
			}
		})
	}
}

func TestStateIncrementSupportsIntegerAndNumberSlots(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	increment, _ := builtins.Definition(nodes.StateIncrementNodeID)
	for _, test := range []struct {
		name, initial, delta, want string
		ref                        datatype.TypeRef
	}{
		{name: "integer", initial: "2", delta: "3", want: "5", ref: builtins.IntegerType.TypeRef()},
		{name: "number", initial: "1.5", delta: "0.25", want: "1.75", ref: builtins.NumberType.TypeRef()},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(fmt.Sprintf(`{
				"format":"yotta.workflow","version":"5","workflow":{"id":"wf-state-increment","name":"State increment"},
				"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
					{"id":"started","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
					{"id":"increment","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},
					 "config":{"variable":"value"},"bindings":{"delta":{"kind":"value","value":%s}}}
				],"edges":[
					{"channel":"exec","from":{"nodeId":"started","portId":"started"},"to":{"nodeId":"increment","portId":"in"}}
				],"inputs":[],"outputs":[]}],
				"variables":[{"name":"value","type":{"kind":"ref","ref":{"typeId":%q,"semanticDigest":%q}},"default":%s}],
				"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
			}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
				increment.Contract.NodeRef().NodeTypeID, increment.Contract.NodeRef().SemanticDigest,
				test.delta, test.ref.TypeID, test.ref.SemanticDigest, test.initial))
			program := compilePrimitiveProgram(t, builtins, source)
			now := time.Date(2026, 7, 24, 15, 30, 0, 0, time.UTC)
			_, owner, journal := admittedExecution(t, builtins, program, nil, now)
			t.Cleanup(func() { _ = owner.Close(context.Background()) })
			adapters, err := noderuntime.Installed(builtins, testDependencies())
			if err != nil {
				t.Fatal(err)
			}
			execution, err := compiler.NewExecutor(
				builtins.Catalog,
				adapters,
				compiler.ExecutorOptions{Now: func() time.Time { return now }},
			).Run(context.Background(), program, owner, journal)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(execution.NodeOutputs["increment"]["result"].InlineJSON()); got != test.want {
				t.Fatalf("incremented state = %s, want %s", got, test.want)
			}
		})
	}
}
