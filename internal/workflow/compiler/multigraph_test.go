package compiler

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"github.com/yottaapp/yotta/internal/nodecontract"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestCompilerExpandsTypedSubgraphIntoTheOnlySchedulerPath(t *testing.T) {
	root := schedulerContractForTest(t, "graph-root", nodecontract.ExecutionEvent, nil, []string{"next"}, nil, nil, nil)
	step := schedulerContractForTest(t, "graph-step", nodecontract.ExecutionControl, []string{"in"}, []string{"done"}, nil, nil, nil)
	sink := schedulerContractForTest(t, "graph-sink", nodecontract.ExecutionControl, []string{"in"}, nil, nil, nil, nil)
	contracts := []nodecontract.Contract{root, step, sink}
	locks := make(map[string]nodecatalog.ImplementationLock, len(contracts))
	bindings := make([]nodecatalog.Binding, 0, len(contracts))
	for _, contract := range contracts {
		id := contract.NodeRef().NodeTypeID
		lock := nodecatalog.ImplementationLock{PackageID: "https://schemas.yotta.dev/packages/multigraph-test/v1", ArtifactDigest: testDigest(t, id), ABI: nodecontract.ABIRequirement{Kind: nodecontract.ABIBuiltin, Version: "v1"}, Entrypoint: id}
		locks[id] = lock
		bindings = append(bindings, nodecatalog.Binding{Contract: contract, Implementation: lock})
	}
	catalog, err := nodecatalog.Seal([]datatype.Definition{}, []capability.Definition{}, bindings, "v1")
	if err != nil {
		t.Fatal(err)
	}
	ref := func(contract nodecontract.Contract) string {
		value := contract.NodeRef()
		return fmt.Sprintf(`{"nodeTypeId":%q,"version":%q,"semanticDigest":%q}`, value.NodeTypeID, value.Version, value.SemanticDigest)
	}
	raw := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-multigraph","name":"Multigraph"},"revision":0,"entryGraph":"main",
		"graphs":[
			{"id":"main","kind":"main","nodes":[
				{"id":"root","nodeRef":%s,"position":{"x":0,"y":0},"config":{},"bindings":{}},
				{"id":"sink","nodeRef":%s,"position":{"x":400,"y":0},"config":{},"bindings":{}}
			],"calls":[{"id":"call","graphId":"child","position":{"x":200,"y":0},"bindings":{}}],"edges":[
				{"channel":"exec","from":{"nodeId":"root","portId":"next"},"to":{"nodeId":"call","portId":"in"}},
				{"channel":"exec","from":{"nodeId":"call","portId":"done"},"to":{"nodeId":"sink","portId":"in"}}
			],"inputs":[],"outputs":[]},
			{"id":"child","name":"Child","kind":"subgraph","nodes":[
				{"id":"step","nodeRef":%s,"position":{"x":0,"y":0},"config":{},"bindings":{}}
			],"edges":[],"inputs":[],"outputs":[],"entries":[{"nodeId":"step","portId":"in"}],"exits":[{"id":"done","channel":"exec","endpoint":{"nodeId":"step","portId":"done"}}]}
		],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`, ref(root), ref(sink), ref(step)))
	build := testDigest(t, "multigraph-build")
	compiled, err := New(build, testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: raw, Catalog: catalog})
	if err != nil || schema.HasErrors(compiled.Diagnostics) {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("missing Program")
	}
	views := program.Nodes()
	if len(views) != 3 {
		t.Fatalf("Program nodes=%#v", views)
	}
	foundChild := false
	for _, view := range views {
		if view.ID == "step" {
			foundChild = slices.Equal(view.GraphPath, []string{"main", "call", "child"})
		}
	}
	if !foundChild {
		t.Fatalf("child location=%#v", views)
	}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	owner, journal := admittedSchedulerExecution(t, catalog, program, now)
	defer owner.Close(context.Background())
	adapters := map[string]InstalledAdapter{}
	for _, contract := range contracts {
		id := contract.NodeRef().NodeTypeID
		outputs := []string(nil)
		if id == root.NodeRef().NodeTypeID {
			outputs = []string{"next"}
		} else if id == step.NodeRef().NodeTypeID {
			outputs = []string{"done"}
		}
		selected := append([]string(nil), outputs...)
		adapters[id] = InstalledAdapter{Implementation: locks[id], Run: func(context.Context, Invocation) (AdapterResult, error) {
			return AdapterResult{Outputs: map[string]datatype.ValueEnvelope{}, ExecOutputs: selected}, nil
		}}
	}
	executor := NewExecutor(catalog, adapters, ExecutorOptions{Now: func() time.Time { return now }})
	if _, err := executor.Run(context.Background(), program, owner, journal); err != nil {
		t.Fatal(err)
	}
	childJournaled := false
	for _, entry := range journal.Current().Journal() {
		if entry.Kind == run.JournalNodeAttempt && entry.NodeID == "step" && slices.Equal(entry.GraphPath, []string{"main", "call", "child"}) {
			childJournaled = true
		}
	}
	if !childJournaled {
		t.Fatal("subgraph execution omitted source graph path")
	}
	if _, err := OpenProgram(program.Artifact(), catalog, testConfigValidators(), build); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedSubgraphCallsShareStaticCapabilityRequirementWithoutLosingRuntimeIdentity(t *testing.T) {
	requirement := capability.Requirement{ID: "target"}
	entries, err := deduplicatePlanEntries([]capability.PlanEntry{
		{GraphID: "child", NodeID: "step", Requirement: requirement},
		{GraphID: "child", NodeID: "step", Requirement: requirement},
	})
	if err != nil || len(entries) != 1 {
		t.Fatalf("deduplicated entries=%#v err=%v", entries, err)
	}
	first, err := runValueID("run", []string{"main", "left", "child"}, "step", "value", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runValueID("run", []string{"main", "right", "child"}, "step", "value", 1)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("repeated subgraph call sites produced the same Run Value identity")
	}
}
