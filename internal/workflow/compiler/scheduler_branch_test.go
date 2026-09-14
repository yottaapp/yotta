package compiler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/automation/inputcoord"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodeadapter"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/problem"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

type branchFixture struct {
	synchronousLeaf bool
	catalog         nodecatalog.Snapshot
	program         ProgramSnapshot
	locks           map[string]nodecatalog.ImplementationLock
	typ             datatype.ResolvedType
}

func newBranchFixture(t *testing.T) branchFixture {
	t.Helper()
	const typeID = "https://schemas.yotta.dev/types/test/branch-string/v1"
	d, err := datatype.SealDefinition(datatype.DefinitionDraft{TypeID: typeID, SchemaDialect: datatype.JSONSchemaDialect, SchemaRoot: typeID + "/schema",
		SchemaBundle:    []datatype.SchemaResource{{ID: typeID + "/schema", Schema: json.RawMessage(fmt.Sprintf(`{"$id":%q,"$schema":%q,"type":"string"}`, typeID+"/schema", datatype.JSONSchemaDialect))}},
		Representations: []datatype.RepresentationSpec{{Kind: datatype.RepresentationInlineJSON, Codec: datatype.CodecJCSV1}}})
	if err != nil {
		t.Fatal(err)
	}
	contracts := map[string]nodecontract.Contract{
		"root": schedulerContractForTest(t, "branch-root", nodecontract.ExecutionEvent, nil, []string{"started"}, nil, nil, nil),
		"done": schedulerContractForTest(t, "branch-done", nodecontract.ExecutionControl, []string{"in"}, nil, nil, nil, nil),
	}
	for _, name := range []string{"main", "leaf"} {
		id := "https://schemas.yotta.dev/nodes/test/branch-" + name
		draft := nodecontract.Draft{NodeTypeID: id, Version: "1.0.0", ConfigSchemaRoot: id + "/config",
			ConfigSchemaBundle: []datatype.SchemaResource{{ID: id + "/config", Schema: json.RawMessage(fmt.Sprintf(`{"$id":%q,"$schema":%q,"type":"object","additionalProperties":false}`, id+"/config", datatype.JSONSchemaDialect))}},
			Ports:              nodecontract.PortSet{ExecInputs: []nodecontract.SignalPort{{ID: "in"}}},
			Execution:          nodecontract.ExecutionSpec{Class: nodecontract.ExecutionEffect, Evaluation: nodecontract.EvaluationPush, Determinism: nodecontract.Deterministic, Cache: nodecontract.CacheNone, Retry: nodecontract.RetryNever, Cancellation: nodecontract.CancellationCooperative, Timeout: nodecontract.TimeoutNone, Effects: []nodecontract.EffectID{"https://schemas.yotta.dev/effects/test/branch/v1"}},
			Instruction:        nodecontract.Invoke(), ImplementationABI: []nodecontract.ABIRequirement{{Kind: nodecontract.ABIBuiltin, Version: "v1"}}}
		draft.Errors = []nodecontract.ErrorSpec{{Code: "test.branch_failed", Category: "test", Params: []nodecontract.ProblemParamSpec{{Name: "reason", Type: nodecontract.ProblemParamString}}}}
		if name == "main" {
			draft.Ports.DataOutputs = []nodecontract.DataOutputPort{{ID: "value", Type: datatype.RefExpression(d.TypeRef())}}
			for _, out := range []string{"moving", "marker", "recover", "unwired", "done"} {
				draft.Ports.ExecOutputs = append(draft.Ports.ExecOutputs, nodecontract.SignalPort{ID: out})
			}
			draft.Instruction.Invoke.Branches = []nodecontract.BranchInstruction{{Output: "moving", Coalesce: true}, {Output: "marker"}, {Output: "recover"}, {Output: "unwired"}}
		} else {
			draft.Ports.DataInputs = []nodecontract.DataInputPort{{ID: "value", Type: datatype.RefExpression(d.TypeRef()), Required: true}}
		}
		contracts[name], err = nodecontract.Seal(draft)
		if err != nil {
			t.Fatal(err)
		}
	}
	f := branchFixture{locks: map[string]nodecatalog.ImplementationLock{}, typ: datatype.RefResolvedType(d.TypeRef())}
	var bindings []nodecatalog.Binding
	for name, c := range contracts {
		lock := nodecatalog.ImplementationLock{PackageID: "https://schemas.yotta.dev/packages/test/branch/v1", ArtifactDigest: testDigest(t, name), ABI: nodecontract.ABIRequirement{Kind: nodecontract.ABIBuiltin, Version: "v1"}, Entrypoint: "branch." + name}
		f.locks[name] = lock
		bindings = append(bindings, nodecatalog.Binding{Contract: c, Implementation: lock})
	}
	f.catalog, err = nodecatalog.Seal([]datatype.Definition{d}, []capability.Definition{}, bindings, "v1")
	if err != nil {
		t.Fatal(err)
	}
	var nodes []any
	for _, id := range []string{"root", "main", "moving", "marker", "recover", "done"} {
		name := id
		if id == "moving" || id == "marker" || id == "recover" {
			name = "leaf"
		}
		nodes = append(nodes, map[string]any{"id": id, "nodeRef": contracts[name].NodeRef(), "position": map[string]int{"x": 0, "y": 0}, "config": map[string]any{}, "bindings": map[string]any{}})
	}
	var edges []any
	edge := func(channel, from, out, to, in string) {
		edges = append(edges, map[string]any{"channel": channel, "from": schema.Endpoint{NodeID: from, PortID: out}, "to": schema.Endpoint{NodeID: to, PortID: in}})
	}
	edge("exec", "root", "started", "main", "in")
	edge("exec", "main", "done", "done", "in")
	for _, id := range []string{"moving", "marker", "recover"} {
		edge("exec", "main", id, id, "in")
		edge("data", "main", "value", id, "value")
	}
	source, err := json.Marshal(map[string]any{"format": "yotta.workflow", "version": "5", "workflow": map[string]string{"id": "wf-branches", "name": "Branches"}, "revision": 0, "entryGraph": "main",
		"graphs": []any{map[string]any{"id": "main", "kind": "main", "nodes": nodes, "edges": edges, "inputs": []any{}, "outputs": []any{}}}, "variables": []any{}, "resources": []any{}, "targetProfileDefinitions": []any{}, "credentialRequirements": []any{}, "dependencies": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := New(testDigest(t, "branch-build"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: source, Catalog: f.catalog})
	if err != nil || schema.HasErrors(compiled.Diagnostics) {
		t.Fatalf("compile: %v %#v", err, compiled.Diagnostics)
	}
	var ok bool
	f.program, ok = compiled.Program()
	if !ok {
		t.Fatal("no Program")
	}
	return f
}

func (f branchFixture) outputs(value string) map[string]datatype.ValueEnvelope {
	raw, _ := json.Marshal(value)
	v, err := datatype.SealInlineJSON(f.catalog, f.typ, raw)
	if err != nil {
		panic(err)
	}
	return map[string]datatype.ValueEnvelope{"value": v}
}

func (f branchFixture) execute(t *testing.T, main, leaf nodeadapter.Adapter) (ExecutionResult, *run.JournalWriter, error) {
	t.Helper()
	owner, journal := admittedSchedulerExecution(t, f.catalog, f.program, time.Now().UTC())
	defer owner.Close(context.Background())
	recorded := func(adapter Adapter) Adapter {
		return func(ctx context.Context, i Invocation) (AdapterResult, error) {
			result, err := adapter(ctx, i)
			action := AdapterAction{EffectID: "https://schemas.yotta.dev/effects/test/branch/v1", Action: "test.branch", SummaryCode: "test.branch", Outcome: run.ActionSucceeded}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				action.Outcome = run.ActionCancelled
			} else if err != nil {
				action.Outcome, action.ErrorCode = run.ActionFailed, "test.branch_failed"
			}
			return result, errors.Join(err, i.RecordAction(context.WithoutCancel(ctx), action))
		}
	}
	adapters := schedulerAdapters(f.locks, map[string]Adapter{
		"root": func(context.Context, Invocation) (AdapterResult, error) {
			return AdapterResult{ExecOutputs: []string{"started"}}, nil
		},
		"main": recorded(main), "leaf": recorded(leaf), "done": emptyAdapter,
	})
	for _, name := range []string{"main", "leaf"} {
		a := adapters[f.locks[name].Entrypoint]
		a.Blocking = name == "main" || !f.synchronousLeaf
		a.PauseAtWait = true
		adapters[f.locks[name].Entrypoint] = a
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := NewExecutor(f.catalog, adapters, ExecutorOptions{}).Run(ctx, f.program, owner, journal)
	return result, journal, err
}

func awaitBranch(ctx context.Context, i Invocation, h nodeadapter.BranchHandle) error {
	if err := i.Await(ctx, h.Done(), 0); err != nil {
		return err
	}
	return h.Err()
}

func TestBranchSteeringContinuesWithCoalescedPrivateSnapshot(t *testing.T) {
	f := newBranchFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	var childCalls atomic.Int32
	result, journal, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		if !i.HasBranch("moving") || i.HasBranch("unwired") || i.HasBranch("done") {
			return AdapterResult{}, errors.New("incorrect frozen branch routes")
		}
		values := f.outputs("first")
		h, err := i.Branch(ctx, nodeadapter.BranchRequest{Output: "moving", Outputs: values})
		if err != nil {
			return AdapterResult{}, err
		}
		values["value"] = f.outputs("mutated")["value"]
		if err = i.Await(ctx, started, 0); err != nil {
			return AdapterResult{}, err
		}
		h2, err := i.Branch(ctx, nodeadapter.BranchRequest{Output: "moving", Outputs: f.outputs("second")})
		if err != nil {
			return AdapterResult{}, err
		}
		if h != h2 {
			return AdapterResult{}, errors.New("moving did not coalesce")
		}
		close(release) // navigation worker is still running while child waits
		if err = awaitBranch(ctx, i, h); err != nil {
			return AdapterResult{}, err
		}
		return AdapterResult{Outputs: f.outputs("final"), ExecOutputs: []string{"done"}}, nil
	}, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		childCalls.Add(1)
		close(started)
		if err := i.Await(ctx, release, 0); err != nil {
			return AdapterResult{}, err
		}
		if string(i.Inputs["value"].InlineJSON()) != `"first"` {
			return AdapterResult{}, errors.New("snapshot was overwritten")
		}
		return AdapterResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if childCalls.Load() != 1 || string(result.NodeOutputs["main"]["value"].InlineJSON()) != `"final"` {
		t.Fatalf("calls=%d result=%v", childCalls.Load(), result)
	}
	starts, ends := 0, 0
	for _, fact := range journal.Current().Journal() {
		if fact.NodeID == "main" {
			if fact.AttemptOutcome == run.AttemptStarted {
				starts++
			}
			if fact.AttemptOutcome == run.AttemptSucceeded {
				ends++
			}
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("parent attempt completed prematurely: %d/%d", starts, ends)
	}
}

func TestBranchMarkersQueueAndDrainInOrder(t *testing.T) {
	f := newBranchFixture(t)
	release := make(chan struct{})
	var got []string
	_, _, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		var handles []nodeadapter.BranchHandle
		for _, v := range []string{"a", "b", "c"} {
			h, err := i.Branch(ctx, nodeadapter.BranchRequest{Output: "marker", Outputs: f.outputs(v)})
			if err != nil {
				return AdapterResult{}, err
			}
			handles = append(handles, h)
		}
		close(release)
		for _, h := range handles {
			if err := awaitBranch(ctx, i, h); err != nil {
				return AdapterResult{}, err
			}
		}
		return AdapterResult{Outputs: f.outputs("final")}, nil
	}, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		if err := i.Await(ctx, release, 0); err != nil {
			return AdapterResult{}, err
		}
		got = append(got, string(i.Inputs["value"].InlineJSON()))
		return AdapterResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{`"a"`, `"b"`, `"c"`}) {
		t.Fatalf("markers=%v", got)
	}
}

func TestBranchActivityCancellationJoinsThenAllowsRecovery(t *testing.T) {
	f := newBranchFixture(t)
	started, cleaned := make(chan struct{}), make(chan struct{})
	var recovered atomic.Bool
	_, journal, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		activity, cancel := context.WithCancel(ctx)
		defer cancel()
		h, err := i.Branch(activity, nodeadapter.BranchRequest{Output: "moving", Outputs: f.outputs("progress-7")})
		if err != nil {
			return AdapterResult{}, err
		}
		if err = i.Await(ctx, started, 0); err != nil {
			return AdapterResult{}, err
		}
		cancel()
		if err = awaitBranch(ctx, i, h); !errors.Is(err, context.Canceled) {
			return AdapterResult{}, fmt.Errorf("activity cancel: %v", err)
		}
		select {
		case <-cleaned:
		default:
			return AdapterResult{}, errors.New("handle closed before cleanup")
		}
		h, err = i.Branch(ctx, nodeadapter.BranchRequest{Output: "recover", Outputs: f.outputs("progress-7")})
		if err != nil {
			return AdapterResult{}, err
		}
		if err = awaitBranch(ctx, i, h); err != nil {
			return AdapterResult{}, err
		}
		return AdapterResult{Outputs: f.outputs("resumed-7")}, nil
	}, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		if i.NodeID == "recover" {
			recovered.Store(true)
			return AdapterResult{}, nil
		}
		defer close(cleaned)
		close(started)
		return AdapterResult{}, i.Await(ctx, make(chan struct{}), 0)
	})
	if err != nil || !recovered.Load() || journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("err=%v recovered=%v status=%v", err, recovered.Load(), journal.Current().Status())
	}
}

func TestBranchParentFinishCancelsAndJoinsChildren(t *testing.T) {
	f := newBranchFixture(t)
	started, producerClosed := make(chan struct{}), make(chan struct{})
	var h nodeadapter.BranchHandle
	_, _, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		var err error
		h, err = i.Branch(ctx, nodeadapter.BranchRequest{Output: "moving", Outputs: f.outputs("moving")})
		if err != nil {
			return AdapterResult{}, err
		}
		if err = i.Await(ctx, started, 0); err != nil {
			return AdapterResult{}, err
		}
		return AdapterResult{Outputs: f.outputs("final"), ExecOutputs: []string{"done"}}, nil
	}, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		if err := i.Spawn(func(ctx context.Context) error { <-ctx.Done(); close(producerClosed); return ctx.Err() }); err != nil {
			return AdapterResult{}, err
		}
		close(started)
		return AdapterResult{}, i.Await(ctx, make(chan struct{}), 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.Done():
		if !errors.Is(h.Err(), context.Canceled) {
			t.Fatal(h.Err())
		}
	default:
		t.Fatal("dangling handle")
	}
	select {
	case <-producerClosed:
	default:
		t.Fatal("dangling producer")
	}
}

func TestBranchFailureReachesHandleAndFailsParent(t *testing.T) {
	f := newBranchFixture(t)
	sentinel := errors.New("child failed")
	var observed atomic.Bool
	_, journal, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		h, err := i.Branch(ctx, nodeadapter.BranchRequest{Output: "recover", Outputs: f.outputs("recover")})
		if err != nil {
			return AdapterResult{}, err
		}
		err = awaitBranch(ctx, i, h)
		observed.Store(errors.Is(err, sentinel))
		return AdapterResult{}, err
	}, func(context.Context, Invocation) (AdapterResult, error) { return AdapterResult{}, sentinel })
	if !errors.Is(err, sentinel) || !observed.Load() || journal.Current().Status() != run.StatusFailed {
		t.Fatalf("err=%v observed=%v status=%v", err, observed.Load(), journal.Current().Status())
	}
}

func TestBranchRejectsInvalidRequestsAndBoundsQueue(t *testing.T) {
	f := newBranchFixture(t)
	_, _, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		for _, request := range []nodeadapter.BranchRequest{{Output: "missing", Outputs: f.outputs("x")}, {Output: "marker"}, {Output: "marker", Outputs: map[string]datatype.ValueEnvelope{"wrong": f.outputs("x")["value"]}}} {
			if _, err := i.Branch(ctx, request); err == nil {
				return AdapterResult{}, errors.New("accepted malformed branch request")
			}
		}
		activity, cancel := context.WithCancel(ctx)
		defer cancel()
		var handles []nodeadapter.BranchHandle
		for range maxPendingBranches {
			h, err := i.Branch(activity, nodeadapter.BranchRequest{Output: "marker", Outputs: f.outputs("queued")})
			if err != nil {
				return AdapterResult{}, err
			}
			handles = append(handles, h)
		}
		if _, err := i.Branch(activity, nodeadapter.BranchRequest{Output: "marker", Outputs: f.outputs("overflow")}); err == nil {
			return AdapterResult{}, errors.New("accepted unbounded branches")
		}
		cancel()
		for _, h := range handles {
			if err := awaitBranch(ctx, i, h); !errors.Is(err, context.Canceled) {
				return AdapterResult{}, fmt.Errorf("queued cancellation: %v", err)
			}
		}
		// Cancellation releases the budget and an unwired declared branch is a no-op.
		h, err := i.Branch(ctx, nodeadapter.BranchRequest{Output: "unwired", Outputs: f.outputs("empty")})
		if err != nil {
			return AdapterResult{}, err
		}
		if err := awaitBranch(ctx, i, h); err != nil {
			return AdapterResult{}, err
		}
		return AdapterResult{Outputs: f.outputs("final")}, nil
	}, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		return AdapterResult{}, i.Await(ctx, make(chan struct{}), 0)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBranchCooperatingOwnerHasIndependentPauseLifecycle(t *testing.T) {
	f := newBranchFixture(t)
	f.synchronousLeaf = true // Even synchronous Invoke must retain composition.
	var coordinator inputcoord.Coordinator
	var parent *inputcoord.Owner
	_, _, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		parent = inputcoord.FromContext(ctx)
		lease, err := coordinator.Acquire(ctx, "desktop", parent)
		if err != nil {
			return AdapterResult{}, err
		}
		defer lease.Release()
		h, err := i.Branch(ctx, nodeadapter.BranchRequest{Output: "moving", Outputs: f.outputs("moving"), CooperativeInput: true})
		if err != nil {
			return AdapterResult{}, err
		}
		if err = awaitBranch(ctx, i, h); err != nil {
			return AdapterResult{}, err
		}
		return AdapterResult{Outputs: f.outputs("final")}, nil
	}, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		child := inputcoord.FromContext(ctx)
		if child == parent || !inputcoord.IsCooperative(ctx) {
			return AdapterResult{}, errors.New("branch reused lifecycle owner or lost cooperation")
		}
		producerDone := make(chan error, 1)
		if err := i.Spawn(func(producerCtx context.Context) error {
			var err error
			if !inputcoord.IsCooperative(producerCtx) || inputcoord.FromContext(producerCtx) != child {
				err = errors.New("scope producer lost cooperative context")
			}
			producerDone <- err
			return err
		}); err != nil {
			return AdapterResult{}, err
		}
		select {
		case err := <-producerDone:
			if err != nil {
				return AdapterResult{}, err
			}
		case <-ctx.Done():
			return AdapterResult{}, ctx.Err()
		}
		lease, err := coordinator.Acquire(ctx, "desktop", child)
		if err != nil {
			return AdapterResult{}, err
		}
		defer lease.Release()
		child.Freeze()
		defer child.Thaw()
		probe, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		parentLease, err := coordinator.Acquire(probe, "desktop", parent)
		if err != nil {
			return AdapterResult{}, fmt.Errorf("child froze navigation: %w", err)
		}
		parentLease.Release()
		other, stop := context.WithCancel(ctx)
		stop()
		if _, err := coordinator.Acquire(other, "desktop", inputcoord.NewOwner()); !errors.Is(err, context.Canceled) {
			return AdapterResult{}, errors.New("unrelated owner bypassed coordination")
		}
		return AdapterResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBranchCooperationPropagatesToNestedScopesOnly(t *testing.T) {
	f := newBranchFixture(t)
	owner, _ := admittedSchedulerExecution(t, f.catalog, f.program, time.Now().UTC())
	defer owner.Close(context.Background())
	root := &scheduler{executor: &Executor{monotonicNow: time.Now}, owner: owner, result: emptyExecutionResult()}
	g := newExecutionGroup(root)
	child, err := g.spawn(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	child.cooperativeInput = true
	child.inputOwner = inputcoord.NewCooperatingOwner(root.inputOwner)
	child.inputOwner.SetWake(g.wake)
	nested, err := g.spawn(child, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := g.spawn(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, s := range g.scopes {
			_ = s.jobs.close()
		}
	}()
	if nested.inputOwner == child.inputOwner || nested.inputOwner == root.inputOwner || !nested.cooperativeInput || !inputcoord.IsCooperative(inputcoord.WithOwner(context.Background(), nested.inputOwner)) {
		t.Fatal("nested scopes lost family or shared pause owner")
	}
	if sibling.cooperativeInput || inputcoord.IsCooperative(inputcoord.WithOwner(context.Background(), sibling.inputOwner)) {
		t.Fatal("cooperation leaked to sibling")
	}
	g.pause(nested)
	if child.paused || root.paused || sibling.paused {
		t.Fatal("nested pause froze siblings")
	}
	g.resume(nested)
}

func TestBranchCannotPublishChildPortAsFinalOutput(t *testing.T) {
	f := newBranchFixture(t)
	_, _, err := f.execute(t, func(context.Context, Invocation) (AdapterResult, error) {
		return AdapterResult{Outputs: f.outputs("invalid"), ExecOutputs: []string{"moving"}}, nil
	}, emptyAdapter)
	if err == nil {
		t.Fatal("accepted branch as a terminal signal")
	}
}

func TestBranchErrorCanBeMappedToParentNodeFailure(t *testing.T) {
	f := newBranchFixture(t)
	cause := errors.New("use a paused marker")
	_, journal, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		h, err := i.Branch(ctx, nodeadapter.BranchRequest{Output: "moving", Outputs: f.outputs("moving")})
		if err != nil {
			return AdapterResult{}, err
		}
		if err = awaitBranch(ctx, i, h); err != nil {
			return AdapterResult{}, &nodeadapter.NodeFailure{Code: "test.branch_failed", Params: problem.Must(map[string]any{"reason": "cooperative_input_requires_paused_marker"}), Cause: err}
		}
		return AdapterResult{Outputs: f.outputs("final")}, nil
	}, func(context.Context, Invocation) (AdapterResult, error) { return AdapterResult{}, cause })
	if !errors.Is(err, cause) {
		t.Fatal(err)
	}
	found := false
	for _, fact := range journal.Current().Journal() {
		if fact.NodeID == "main" && fact.AttemptOutcome == run.AttemptFailed {
			found = true
			if fact.ErrorCode != "test.branch_failed" || string(fact.ErrorParams) != `{"reason":"cooperative_input_requires_paused_marker"}` {
				t.Fatalf("lost mapped node failure: %+v", fact)
			}
		}
	}
	if !found {
		t.Fatal("missing parent failure fact")
	}
}

func TestBranchCancellationCannotHideCleanupFailure(t *testing.T) {
	f := newBranchFixture(t)
	started := make(chan struct{})
	cleanupFailure := errors.New("input cleanup failed")
	_, journal, err := f.execute(t, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		activity, cancel := context.WithCancel(ctx)
		defer cancel()
		h, err := i.Branch(activity, nodeadapter.BranchRequest{Output: "moving", Outputs: f.outputs("moving")})
		if err != nil {
			return AdapterResult{}, err
		}
		if err := i.Await(ctx, started, 0); err != nil {
			return AdapterResult{}, err
		}
		cancel()
		if err := awaitBranch(ctx, i, h); !errors.Is(err, context.Canceled) || !errors.Is(err, cleanupFailure) {
			return AdapterResult{}, fmt.Errorf("lost cancellation/cleanup cause: %v", err)
		}
		// Even a caller which ignores cancellation must not erase cleanup error.
		return AdapterResult{Outputs: f.outputs("final")}, nil
	}, func(ctx context.Context, i Invocation) (AdapterResult, error) {
		close(started)
		err := i.Await(ctx, make(chan struct{}), 0)
		return AdapterResult{}, errors.Join(err, cleanupFailure)
	})
	if !errors.Is(err, cleanupFailure) || journal.Current().Status() != run.StatusFailed {
		t.Fatalf("err=%v status=%v", err, journal.Current().Status())
	}
}

func TestBranchProgramPinsContractAndCompilerBuild(t *testing.T) {
	f := newBranchFixture(t)
	build := testDigest(t, "branch-build")
	if _, err := OpenProgram(f.program.Artifact(), f.catalog, testConfigValidators(), build); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(f.program.Artifact(), f.catalog, testConfigValidators(), testDigest(t, "different-compiler")); err == nil {
		t.Fatal("cache crossed compiler build identity")
	}
	var doc programDocument
	if err := json.Unmarshal(f.program.Artifact(), &doc); err != nil {
		t.Fatal(err)
	}
	for n := range doc.Body.Graphs[0].Nodes {
		node := &doc.Body.Graphs[0].Nodes[n]
		if node.ID == "main" {
			node.Instruction.Invoke.Branches[0].Coalesce = false
		}
	}
	forged, err := sealProgram(doc.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), f.catalog, testConfigValidators(), build); err == nil {
		t.Fatal("rehashed Program changed frozen branch semantics")
	}
}
