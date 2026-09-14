package compiler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/problem"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/runid"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestExecutorSchedulesOnlySelectedExecRouteAndPersistsStatus(t *testing.T) {
	catalog, contracts, locks := schedulerCatalogForTest(t)
	program := compileSchedulerProgram(t, catalog, contracts, true)
	now := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	owner, journal := admittedSchedulerExecution(t, catalog, program, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	calls := map[string]int{}
	adapters := schedulerAdapters(locks, map[string]Adapter{
		"source": func(ctx context.Context, invocation Invocation) (AdapterResult, error) {
			calls["source"]++
			if invocation.Trigger != nil {
				t.Fatal("event root received a synthetic trigger")
			}
			if err := invocation.EmitStatus(ctx, "test.progress", map[string]int64{"percent": 50}); err != nil {
				return AdapterResult{}, err
			}
			return AdapterResult{ExecOutputs: []string{"right"}}, nil
		},
		"left": func(context.Context, Invocation) (AdapterResult, error) {
			calls["left"]++
			return AdapterResult{}, nil
		},
		"right": func(_ context.Context, invocation Invocation) (AdapterResult, error) {
			calls["right"]++
			if invocation.Trigger == nil || invocation.Trigger.Channel != schema.EdgeExec || invocation.Trigger.InputPort != "in" || invocation.Trigger.From.PortID != "right" {
				t.Fatalf("right trigger = %#v", invocation.Trigger)
			}
			return AdapterResult{}, nil
		},
		"handler": func(context.Context, Invocation) (AdapterResult, error) {
			calls["handler"]++
			return AdapterResult{}, nil
		},
	})
	executor := NewExecutor(catalog, adapters, ExecutorOptions{Now: func() time.Time { return now.Add(2 * time.Second) }})
	if _, err := executor.Run(context.Background(), program, owner, journal); err != nil {
		t.Fatal(err)
	}
	if calls["source"] != 1 || calls["right"] != 1 || calls["left"] != 0 || calls["handler"] != 0 {
		t.Fatalf("adapter calls = %#v", calls)
	}
	facts := journal.Current().Journal()
	if journal.Current().Status() != run.StatusSucceeded || len(facts) != 5 || facts[1].Kind != run.JournalNodeStatus ||
		facts[1].StatusCode != "test.progress" || facts[1].StatusCategory != nodecontract.StatusProgress {
		t.Fatalf("journal = %#v", facts)
	}
}

func TestExecutorRoutesStructuredNodeFailureAndFailsWhenUnhandled(t *testing.T) {
	for _, routed := range []bool{true, false} {
		t.Run(fmt.Sprintf("routed=%t", routed), func(t *testing.T) {
			catalog, contracts, locks := schedulerCatalogForTest(t)
			program := compileSchedulerProgram(t, catalog, contracts, routed)
			now := time.Date(2026, 7, 15, 6, 30, 0, 0, time.UTC)
			owner, journal := admittedSchedulerExecution(t, catalog, program, now)
			t.Cleanup(func() { _ = owner.Close(context.Background()) })
			handled := false
			adapters := schedulerAdapters(locks, map[string]Adapter{
				"source": func(context.Context, Invocation) (AdapterResult, error) {
					return AdapterResult{}, &NodeFailure{Code: "test.event-failed", Output: "failed", Params: problem.Must(map[string]any{"reason": "fixture"}), Cause: errors.New("provider detail")}
				},
				"left":  emptyAdapter,
				"right": emptyAdapter,
				"handler": func(_ context.Context, invocation Invocation) (AdapterResult, error) {
					handled = true
					failure := invocation.Trigger.Failure
					if invocation.Trigger.Channel != schema.EdgeError || invocation.Trigger.InputPort != "in" || failure == nil ||
						failure.Code != "test.event-failed" || failure.Category != "test" || failure.SourceNodeID != "source" || failure.SourcePortID != "failed" || failure.Attempt != 1 || string(failure.Params.Bytes()) != `{"reason":"fixture"}` {
						t.Fatalf("routed failure = %#v", invocation.Trigger)
					}
					return AdapterResult{}, nil
				},
			})
			executor := NewExecutor(catalog, adapters, ExecutorOptions{Now: func() time.Time { return now.Add(2 * time.Second) }})
			_, runErr := executor.Run(context.Background(), program, owner, journal)
			if routed {
				if runErr != nil || !handled || journal.Current().Status() != run.StatusSucceeded {
					t.Fatalf("routed Run: err=%v handled=%t status=%s", runErr, handled, journal.Current().Status())
				}
				facts := journal.Current().Journal()
				if len(facts) != 4 || facts[1].AttemptOutcome != run.AttemptRouted || facts[1].ErrorCode != "test.event-failed" || string(facts[1].ErrorParams) != `{"reason":"fixture"}` {
					t.Fatalf("routed journal = %#v", facts)
				}
			} else if runErr == nil || handled || journal.Current().Status() != run.StatusFailed {
				t.Fatalf("unhandled Run: err=%v handled=%t status=%s", runErr, handled, journal.Current().Status())
			}
		})
	}
}

func TestSchedulerPropagatesVolatilityThroughPureDataDependencies(t *testing.T) {
	graph := programGraph{
		ID: "main",
		Nodes: []programNode{
			{ID: "observed", Inputs: map[string]inputPlan{}, Execution: nodecontract.ExecutionSpec{Cache: nodecontract.CacheNone}},
			{ID: "derived", Inputs: map[string]inputPlan{"value": {Kind: inputEdge, From: schema.Endpoint{NodeID: "observed", PortID: "result"}}}, Execution: nodecontract.ExecutionSpec{Cache: nodecontract.CachePerRun}},
			{ID: "stable", Inputs: map[string]inputPlan{}, Execution: nodecontract.ExecutionSpec{Cache: nodecontract.CachePerRun}},
		},
		DataOrder: []string{"observed", "derived", "stable"},
	}
	scheduler := newScheduler(nil, &graph, nil, nil, nil, nil)
	if !scheduler.volatile["observed"] || !scheduler.volatile["derived"] || scheduler.volatile["stable"] {
		t.Fatalf("volatile classification = %#v", scheduler.volatile)
	}
}

func TestExecutionReachabilityRequiresARealRootAndFollowsSignalRoutes(t *testing.T) {
	push := nodecontract.ExecutionSpec{Class: nodecontract.ExecutionControl, Evaluation: nodecontract.EvaluationPush}
	event := nodecontract.ExecutionSpec{Class: nodecontract.ExecutionEvent, Evaluation: nodecontract.EvaluationPush}
	graph := programGraph{
		Nodes: []programNode{
			{ID: "root", Execution: event, Ports: nodecontract.PortSet{ExecOutputs: []nodecontract.SignalPort{{ID: "started"}}}},
			{ID: "branch", Execution: push, Ports: nodecontract.PortSet{ExecInputs: []nodecontract.SignalPort{{ID: "in"}}}},
		},
		SignalRoutes: []programSignalRoute{{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "root", PortID: "started"}, To: schema.Endpoint{NodeID: "branch", PortID: "in"}}},
		DataOrder:    []string{"root", "branch"},
	}
	roots, reachable := executionReachability(graph)
	if len(roots) != 1 || roots[0] != "root" || !reachable["branch"] {
		t.Fatalf("roots=%#v reachable=%#v", roots, reachable)
	}
	graph.Nodes = graph.Nodes[1:]
	graph.SignalRoutes = nil
	graph.DataOrder = []string{"branch"}
	roots, reachable = executionReachability(graph)
	if len(roots) != 0 || reachable["branch"] {
		t.Fatalf("rootless roots=%#v reachable=%#v", roots, reachable)
	}
}

func emptyAdapter(context.Context, Invocation) (AdapterResult, error) { return AdapterResult{}, nil }

func schedulerCatalogForTest(t *testing.T) (nodecatalog.Snapshot, map[string]nodecontract.Contract, map[string]nodecatalog.ImplementationLock) {
	t.Helper()
	contracts := map[string]nodecontract.Contract{
		"source": schedulerContractForTest(t, "source", nodecontract.ExecutionEvent, nil, []string{"left", "right"}, []string{"failed"},
			[]nodecontract.ErrorSpec{{Code: "test.event-failed", Category: "test", RetryHint: false, Params: []nodecontract.ProblemParamSpec{{Name: "reason", Type: nodecontract.ProblemParamString, Required: true}}}},
			[]nodecontract.StatusEventSpec{{Code: "test.progress", Category: nodecontract.StatusProgress}}),
		"left":    schedulerContractForTest(t, "left", nodecontract.ExecutionControl, []string{"in"}, nil, nil, nil, nil),
		"right":   schedulerContractForTest(t, "right", nodecontract.ExecutionControl, []string{"in"}, nil, nil, nil, nil),
		"handler": schedulerContractForTest(t, "handler", nodecontract.ExecutionControl, []string{"in"}, nil, nil, nil, nil),
	}
	locks := make(map[string]nodecatalog.ImplementationLock, len(contracts))
	bindings := make([]nodecatalog.Binding, 0, len(contracts))
	for name, contract := range contracts {
		lock := nodecatalog.ImplementationLock{
			PackageID: "https://schemas.yotta.dev/packages/scheduler-test/v1", ArtifactDigest: testDigest(t, "scheduler-"+name),
			ABI: nodecontract.ABIRequirement{Kind: nodecontract.ABIBuiltin, Version: "v1"}, Entrypoint: "scheduler." + name,
		}
		locks[name] = lock
		bindings = append(bindings, nodecatalog.Binding{Contract: contract, Implementation: lock})
	}
	catalog, err := nodecatalog.Seal([]datatype.Definition{}, []capability.Definition{}, bindings, "v1")
	if err != nil {
		t.Fatal(err)
	}
	return catalog, contracts, locks
}

func schedulerContractForTest(t *testing.T, name string, class nodecontract.ExecutionClass, execInputs, execOutputs, errorOutputs []string, errorsList []nodecontract.ErrorSpec, statuses []nodecontract.StatusEventSpec) nodecontract.Contract {
	t.Helper()
	nodeID := "https://schemas.yotta.dev/nodes/scheduler-test/" + name
	configID := nodeID + "/config"
	toPorts := func(ids []string) []nodecontract.SignalPort {
		result := make([]nodecontract.SignalPort, len(ids))
		for index, id := range ids {
			result[index] = nodecontract.SignalPort{ID: id}
		}
		return result
	}
	contract, err := nodecontract.Seal(nodecontract.Draft{Version: "1.0.0",
		NodeTypeID: nodeID, ConfigSchemaRoot: configID,
		ConfigSchemaBundle: []datatype.SchemaResource{{ID: configID, Schema: json.RawMessage(fmt.Sprintf(`{"$id":%q,"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`, configID))}},
		Ports: nodecontract.PortSet{
			DataInputs: []nodecontract.DataInputPort{}, DataOutputs: []nodecontract.DataOutputPort{},
			ExecInputs: toPorts(execInputs), ExecOutputs: toPorts(execOutputs), ErrorOutputs: toPorts(errorOutputs),
		},
		Execution: nodecontract.ExecutionSpec{
			Class: class, Effects: []nodecontract.EffectID{}, Determinism: nodecontract.Deterministic,
			Evaluation: nodecontract.EvaluationPush, Cache: nodecontract.CacheNone, Retry: nodecontract.RetryNever,
			Cancellation: nodecontract.CancellationCooperative, Timeout: nodecontract.TimeoutNone,
		},
		Instruction:            nodecontract.Invoke(),
		CapabilityRequirements: []capability.Requirement{}, Errors: errorsList, StatusEvents: statuses,
		ImplementationABI: []nodecontract.ABIRequirement{{Kind: nodecontract.ABIBuiltin, Version: "v1"}},
		Authoring:         nodecontract.Authoring{Tags: []string{"test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func compileSchedulerProgram(t *testing.T, catalog nodecatalog.Snapshot, contracts map[string]nodecontract.Contract, includeErrorRoute bool) ProgramSnapshot {
	t.Helper()
	ref := func(name string) nodecontract.NodeRef { return contracts[name].NodeRef() }
	edges := []string{
		`{"channel":"exec","from":{"nodeId":"source","portId":"left"},"to":{"nodeId":"left","portId":"in"}}`,
		`{"channel":"exec","from":{"nodeId":"source","portId":"right"},"to":{"nodeId":"right","portId":"in"}}`,
	}
	if includeErrorRoute {
		edges = append(edges, `{"channel":"error","from":{"nodeId":"source","portId":"failed"},"to":{"nodeId":"handler","portId":"in"}}`)
	}
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-scheduler","name":"Scheduler"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"source","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"left","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},"bindings":{}},
			{"id":"right","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":1},"config":{},"bindings":{}},
			{"id":"handler","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":2},"config":{},"bindings":{}}
		],"edges":[%s],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref("source").NodeTypeID, ref("source").SemanticDigest, ref("left").NodeTypeID, ref("left").SemanticDigest,
		ref("right").NodeTypeID, ref("right").SemanticDigest, ref("handler").NodeTypeID, ref("handler").SemanticDigest,
		joinStrings(edges)))
	compiled, err := New(testDigest(t, "scheduler-build"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: source, Catalog: catalog})
	if err != nil || schema.HasErrors(compiled.Diagnostics) {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("missing scheduler Program")
	}
	return program
}

func admittedSchedulerExecution(t *testing.T, catalog nodecatalog.Snapshot, program ProgramSnapshot, now time.Time) (*run.Owner, *run.JournalWriter) {
	t.Helper()
	id, err := runid.New()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := capability.SealRunGrant(capability.GrantRequest{
		ProgramHash: program.Hash(), Plan: program.CapabilityPlan(), RunID: id, Principal: "test-user",
		PolicyGeneration: "policy-1", IssuedAt: now, Bindings: []capability.Binding{},
	}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := run.NewQueuedRecord(run.QueueRequest{
		ProgramHash: program.Hash(), CatalogHash: catalog.Hash(), CapabilityPlanDigest: program.CapabilityPlan().Digest(),
		Grant: grant, QueuedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := newCompilerRunStore(t, catalog, run.StoreOptions{MaxRecords: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(context.Background(), queued); err != nil {
		t.Fatal(err)
	}
	running, err := queued.Start(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), queued.Digest(), running); err != nil {
		t.Fatal(err)
	}
	journal, err := store.OpenJournal(grant.RunID())
	if err != nil {
		t.Fatal(err)
	}
	owner, err := run.NewOwner(context.Background(), grant, map[string]run.InstalledProvider{}, resource.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return owner, journal
}

func schedulerAdapters(locks map[string]nodecatalog.ImplementationLock, adapters map[string]Adapter) map[string]InstalledAdapter {
	result := make(map[string]InstalledAdapter, len(adapters))
	for name, adapter := range adapters {
		lock := locks[name]
		result[lock.Entrypoint] = InstalledAdapter{Implementation: lock, Run: adapter}
	}
	return result
}

func joinStrings(values []string) string {
	result := ""
	for index, value := range values {
		if index != 0 {
			result += ","
		}
		result += value
	}
	return result
}
