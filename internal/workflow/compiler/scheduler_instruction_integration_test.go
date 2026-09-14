package compiler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/nodeadapter"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/runid"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestSchedulerExecutesCountedAndCollectionRegions(t *testing.T) {
	for _, test := range []struct {
		name         string
		nodeTypeID   string
		bindingPort  string
		bindingValue string
		outputPort   string
		want         string
	}{
		{name: "counted loop", nodeTypeID: nodes.RepeatNodeID, bindingPort: "count", bindingValue: `3`, outputPort: "index", want: `2`},
		{name: "for each", nodeTypeID: nodes.ForEachNodeID, bindingPort: "items", bindingValue: `["a","b","c"]`, outputPort: "item", want: `"c"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			builtins := schedulerBuiltins(t)
			started := schedulerNodeRef(t, builtins, nodes.RunStartedNodeID)
			region := schedulerNodeRef(t, builtins, test.nodeTypeID)
			write := schedulerNodeRef(t, builtins, nodes.StateWriteNodeID)
			end := schedulerNodeRef(t, builtins, nodes.EndBranchNodeID)
			valueType := builtins.IntegerType.TypeRef()
			defaultValue := `0`
			if test.nodeTypeID == nodes.ForEachNodeID {
				valueType = builtins.StringType.TypeRef()
				defaultValue = `""`
			}
			source := []byte(fmt.Sprintf(`{
				"format":"yotta.workflow","version":"5","workflow":{"id":"wf-region","name":"Region"},
				"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
					{"id":"started","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
					{"id":"region","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},"bindings":{%q:{"kind":"value","value":%s}}},
					{"id":"write","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":2,"y":0},"config":{"variable":"value"},"bindings":{}},
					{"id":"end","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":3,"y":0},"config":{},"bindings":{}}
				],"edges":[
					{"channel":"exec","from":{"nodeId":"started","portId":"started"},"to":{"nodeId":"region","portId":"in"}},
					{"channel":"exec","from":{"nodeId":"region","portId":"body"},"to":{"nodeId":"write","portId":"in"}},
					{"channel":"exec","from":{"nodeId":"write","portId":"done"},"to":{"nodeId":"end","portId":"in"}},
					{"channel":"data","from":{"nodeId":"region","portId":%q},"to":{"nodeId":"write","portId":"value"}}
				],"inputs":[],"outputs":[]}],
				"variables":[{"name":"value","type":{"kind":"ref","ref":{"typeId":%q,"semanticDigest":%q}},"default":%s}],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
			}`, started.NodeTypeID, started.SemanticDigest, region.NodeTypeID, region.SemanticDigest,
				test.bindingPort, test.bindingValue, write.NodeTypeID, write.SemanticDigest,
				end.NodeTypeID, end.SemanticDigest, test.outputPort, valueType.TypeID, valueType.SemanticDigest, defaultValue))
			program := compileSchedulerInstructionProgram(t, builtins, source)
			execution, journal := runSchedulerInstructionProgram(t, builtins, program, compiler.ExecutorOptions{})
			got := execution.NodeOutputs["region"][test.outputPort]
			if !got.Valid() || string(got.InlineJSON()) != test.want {
				t.Fatalf("%s output = %s, want %s", test.outputPort, got.InlineJSON(), test.want)
			}
			if journal.Current().Status() != run.StatusSucceeded {
				t.Fatalf("Run status = %s", journal.Current().Status())
			}
		})
	}
}

func TestSchedulerRetriesOnlyExplicitRoutedFailure(t *testing.T) {
	builtins := schedulerBuiltins(t)
	started := schedulerNodeRef(t, builtins, nodes.RunStartedNodeID)
	retry := schedulerNodeRef(t, builtins, nodes.RetryNodeID)
	delay := schedulerNodeRef(t, builtins, nodes.DelayNodeID)
	end := schedulerNodeRef(t, builtins, nodes.EndBranchNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-retry","name":"Retry"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"started","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"retry","nodeRef":{"nodeTypeId":%q,"version":"1.1.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},"bindings":{"attempts":{"kind":"value","value":3}}},
			{"id":"delay","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":2,"y":0},"config":{},"bindings":{"duration-milliseconds":{"kind":"value","value":1}}},
			{"id":"end","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":3,"y":0},"config":{},"bindings":{}}
		],"edges":[
			{"channel":"exec","from":{"nodeId":"started","portId":"started"},"to":{"nodeId":"retry","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"retry","portId":"body"},"to":{"nodeId":"delay","portId":"in"}},
			{"channel":"error","from":{"nodeId":"delay","portId":"failed"},"to":{"nodeId":"retry","portId":"retry"}},
			{"channel":"exec","from":{"nodeId":"retry","portId":"completed"},"to":{"nodeId":"end","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"retry","portId":"exhausted"},"to":{"nodeId":"end","portId":"in"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.NodeTypeID, started.SemanticDigest, retry.NodeTypeID, retry.SemanticDigest,
		delay.NodeTypeID, delay.SemanticDigest, end.NodeTypeID, end.SemanticDigest))
	program := compileSchedulerInstructionProgram(t, builtins, source)
	for _, test := range []struct {
		name      string
		failures  int
		exhausted bool
	}{
		{"eventual success", 2, false}, {"exhausted", 3, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			waits := 0
			execution, journal := runSchedulerInstructionProgram(t, builtins, program, compiler.ExecutorOptions{
				Wait: func(context.Context, time.Duration) error {
					waits++
					if waits <= test.failures {
						return errors.New("transient wait failure")
					}
					return nil
				},
			})
			var attempt int64
			if err := json.Unmarshal(execution.NodeOutputs["retry"]["attempt"].InlineJSON(), &attempt); err != nil || waits != 3 || attempt != 3 {
				t.Fatalf("waits=%d attempt=%d error=%v", waits, attempt, err)
			}
			exhausted := 0
			for _, fact := range journal.Current().Journal() {
				if fact.Kind == run.JournalNodeStatus && fact.StatusCode == nodecontract.RetryExhaustedStatusID {
					exhausted++
				}
			}
			if (exhausted == 1) != test.exhausted || exhausted > 1 {
				t.Fatalf("exhaustion facts=%d, expected exhausted=%v", exhausted, test.exhausted)
			}
			if journal.Current().Status() != run.StatusSucceeded {
				t.Fatalf("Run status = %s", journal.Current().Status())
			}
		})
	}

}

func TestDebugStepPausesInsideCountedLoopInsteadOfRunningTheRegion(t *testing.T) {
	builtins := schedulerBuiltins(t)
	started := schedulerNodeRef(t, builtins, nodes.RunStartedNodeID)
	repeat := schedulerNodeRef(t, builtins, nodes.RepeatNodeID)
	end := schedulerNodeRef(t, builtins, nodes.EndBranchNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-debug-loop","name":"Debug Loop"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"started","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"repeat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},"bindings":{"count":{"kind":"value","value":2}}},
			{"id":"end","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":2,"y":0},"config":{},"bindings":{}}
		],"edges":[
			{"channel":"exec","from":{"nodeId":"started","portId":"started"},"to":{"nodeId":"repeat","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"repeat","portId":"body"},"to":{"nodeId":"end","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"repeat","portId":"completed"},"to":{"nodeId":"end","portId":"in"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.NodeTypeID, started.SemanticDigest, repeat.NodeTypeID, repeat.SemanticDigest, end.NodeTypeID, end.SemanticDigest))
	program := compileSchedulerInstructionProgram(t, builtins, source)
	runtime := prepareSchedulerInstructionRuntime(t, builtins, program, compiler.ExecutorOptions{})
	control, err := compiler.NewDebugController(compiler.DebugControllerOptions{StartPaused: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := runtime.executor.RunDebug(context.Background(), program, runtime.owner, runtime.journal, control)
		done <- runErr
	}()
	waitInstructionDebug(t, control, "started")
	if err := control.Step(); err != nil {
		t.Fatal(err)
	}
	waitInstructionDebug(t, control, "repeat")
	if err := control.Step(); err != nil {
		t.Fatal(err)
	}
	waitInstructionDebug(t, control, "end")
	if err := control.Step(); err != nil {
		t.Fatal(err)
	}
	waitInstructionDebug(t, control, "end")
	if err := control.Continue(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("debug loop did not complete")
	}
}

func TestDebugStepPausesForEveryRetryAttempt(t *testing.T) {
	builtins := schedulerBuiltins(t)
	started := schedulerNodeRef(t, builtins, nodes.RunStartedNodeID)
	retry := schedulerNodeRef(t, builtins, nodes.RetryNodeID)
	delay := schedulerNodeRef(t, builtins, nodes.DelayNodeID)
	end := schedulerNodeRef(t, builtins, nodes.EndBranchNodeID)
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-debug-retry","name":"Debug Retry"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"started","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"retry","nodeRef":{"nodeTypeId":%q,"version":"1.1.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},"bindings":{"attempts":{"kind":"value","value":3}}},
			{"id":"delay","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":2,"y":0},"config":{},"bindings":{"duration-milliseconds":{"kind":"value","value":1}}},
			{"id":"end","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":3,"y":0},"config":{},"bindings":{}}
		],"edges":[
			{"channel":"exec","from":{"nodeId":"started","portId":"started"},"to":{"nodeId":"retry","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"retry","portId":"body"},"to":{"nodeId":"delay","portId":"in"}},
			{"channel":"error","from":{"nodeId":"delay","portId":"failed"},"to":{"nodeId":"retry","portId":"retry"}},
			{"channel":"exec","from":{"nodeId":"retry","portId":"completed"},"to":{"nodeId":"end","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"retry","portId":"exhausted"},"to":{"nodeId":"end","portId":"in"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.NodeTypeID, started.SemanticDigest, retry.NodeTypeID, retry.SemanticDigest,
		delay.NodeTypeID, delay.SemanticDigest, end.NodeTypeID, end.SemanticDigest))
	program := compileSchedulerInstructionProgram(t, builtins, source)
	waits := 0
	runtime := prepareSchedulerInstructionRuntime(t, builtins, program, compiler.ExecutorOptions{
		Wait: func(context.Context, time.Duration) error {
			waits++
			if waits < 3 {
				return errors.New("transient wait failure")
			}
			return nil
		},
	})
	control, err := compiler.NewDebugController(compiler.DebugControllerOptions{StartPaused: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := runtime.executor.RunDebug(context.Background(), program, runtime.owner, runtime.journal, control)
		done <- runErr
	}()
	for _, nodeID := range []string{"started", "retry", "delay", "delay", "delay", "end"} {
		waitInstructionDebug(t, control, nodeID)
		if err := control.Step(); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("debug retry did not complete")
	}
	if waits != 3 {
		t.Fatalf("retry attempts = %d", waits)
	}
}

func waitInstructionDebug(t *testing.T, control *compiler.DebugController, nodeID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := control.Snapshot()
		if snapshot.Status == compiler.DebugPaused && snapshot.NodeID == nodeID {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("debugger did not pause at %q: %#v", nodeID, control.Snapshot())
}

type schedulerUnusedScriptRuntime struct{}

func (schedulerUnusedScriptRuntime) Execute(context.Context, scriptengine.Request) (scriptengine.Response, error) {
	return scriptengine.Response{}, errors.New("unexpected script execution")
}

type schedulerUnusedLogEmitter struct{}

func (schedulerUnusedLogEmitter) EmitWorkflowLog(context.Context, noderuntime.LogEntry) error {
	return errors.New("unexpected log emission")
}

func schedulerBuiltins(t *testing.T) nodes.Builtins {
	t.Helper()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	return builtins
}

func schedulerNodeRef(t *testing.T, builtins nodes.Builtins, nodeTypeID string) nodecontract.NodeRef {
	t.Helper()
	definition, ok := builtins.Definition(nodeTypeID)
	if !ok {
		t.Fatalf("node definition %q is missing", nodeTypeID)
	}
	return definition.Contract.NodeRef()
}

func compileSchedulerInstructionProgram(t *testing.T, builtins nodes.Builtins, source []byte) compiler.ProgramSnapshot {
	t.Helper()
	build, err := artifact.Sum("yotta/test/compiler-build/v1", []byte(t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(context.Background(), compiler.CompileRequest{
		SourceJSON: source,
		Catalog:    builtins.Catalog,
	})
	if err != nil || len(result.Diagnostics) != 0 {
		t.Fatalf("CompileDraft() error=%v diagnostics=%#v", err, result.Diagnostics)
	}
	program, ok := result.Program()
	if !ok {
		t.Fatal("CompileDraft did not produce a Program")
	}
	return program
}

func runSchedulerInstructionProgram(t *testing.T, builtins nodes.Builtins, program compiler.ProgramSnapshot, options compiler.ExecutorOptions) (compiler.ExecutionResult, *run.JournalWriter) {
	t.Helper()
	runtime := prepareSchedulerInstructionRuntime(t, builtins, program, options)
	execution, err := runtime.executor.Run(context.Background(), program, runtime.owner, runtime.journal)
	if err != nil {
		t.Fatal(err)
	}
	return execution, runtime.journal
}

type schedulerInstructionRuntime struct {
	executor *compiler.Executor
	owner    *run.Owner
	journal  *run.JournalWriter
}

func prepareSchedulerInstructionRuntime(t *testing.T, builtins nodes.Builtins, program compiler.ProgramSnapshot, options compiler.ExecutorOptions, configure ...func(map[string]nodeadapter.InstalledAdapter)) schedulerInstructionRuntime {
	t.Helper()
	now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	id, err := runid.New()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := capability.SealRunGrant(capability.GrantRequest{
		ProgramHash: program.Hash(), Plan: program.CapabilityPlan(), RunID: id, Principal: "test-user",
		PolicyGeneration: "policy-1", IssuedAt: now, Bindings: []capability.Binding{},
	}, builtins.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := run.NewQueuedRecord(run.QueueRequest{
		ProgramHash: program.Hash(), CatalogHash: builtins.Catalog.Hash(), CapabilityPlanDigest: program.CapabilityPlan().Digest(),
		Grant: grant, QueuedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := newCompilerIntegrationRunStore(t, builtins.Catalog, run.StoreOptions{MaxRecords: 1})
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
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, noderuntime.Dependencies{
		Script: schedulerUnusedScriptRuntime{},
		Log:    schedulerUnusedLogEmitter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Now == nil {
		options.Now = func() time.Time { return now }
	}
	if options.MonotonicNow == nil && options.Wait != nil {
		options.MonotonicNow = options.Now
	}
	for _, customize := range configure {
		customize(adapters)
	}
	return schedulerInstructionRuntime{
		executor: compiler.NewExecutor(builtins.Catalog, adapters, options), owner: owner, journal: journal,
	}
}

// Nested regions must resume the correct parent queue, including when an inner
// body routes control to an outer frame. The observations are real delay effects.
func TestSchedulerNestedRegionContinuations(t *testing.T) {
	for _, test := range []struct {
		name, target, port string
		outerCount         int
		want               []time.Duration
	}{
		{"complete", "", "", 2, []time.Duration{1, 1, 1, 2, 1, 1, 1, 2, 3}},
		{"inner break", "inner", "break", 2, []time.Duration{1, 2, 1, 2, 3}},
		{"inner continue", "inner", "continue", 2, []time.Duration{1, 1, 1, 2, 1, 1, 1, 2, 3}},
		{"outer break", "outer", "break", 2, []time.Duration{1, 3}},
		{"outer continue", "outer", "continue", 2, []time.Duration{1, 1, 3}},
		{"empty outer", "", "", 0, []time.Duration{3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			builtins := schedulerBuiltins(t)
			source := nestedRegionSource(t, builtins, test.outerCount, test.target, test.port)
			program := compileSchedulerInstructionProgram(t, builtins, source)
			var observed []time.Duration
			_, journal := runSchedulerInstructionProgram(t, builtins, program, compiler.ExecutorOptions{
				Wait: func(_ context.Context, d time.Duration) error {
					observed = append(observed, d/time.Millisecond)
					return nil
				},
			})
			if !reflect.DeepEqual(observed, test.want) {
				t.Fatalf("observations = %v, want %v", observed, test.want)
			}
			if journal.Current().Status() != run.StatusSucceeded {
				t.Fatalf("status = %s", journal.Current().Status())
			}
		})
	}
}

func TestSchedulerCancellationClosesAllActiveRegionFrames(t *testing.T) {
	builtins := schedulerBuiltins(t)
	program := compileSchedulerInstructionProgram(t, builtins, nestedRegionSource(t, builtins, 2, "", ""))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := prepareSchedulerInstructionRuntime(t, builtins, program, compiler.ExecutorOptions{
		Wait: func(context.Context, time.Duration) error { cancel(); return ctx.Err() },
	})
	_, err := runtime.executor.Run(ctx, program, runtime.owner, runtime.journal)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	closed := map[string]int{}
	for _, fact := range runtime.journal.Current().Journal() {
		if fact.Kind == run.JournalNodeAttempt && fact.AttemptOutcome == run.AttemptCancelled {
			closed[fact.NodeID]++
		}
	}
	for _, id := range []string{"outer", "inner", "body"} {
		if closed[id] != 1 {
			t.Fatalf("cancelled attempts = %v, expected exactly one for %s", closed, id)
		}
	}
}

func nestedRegionSource(t *testing.T, builtins nodes.Builtins, count int, target, port string) []byte {
	t.Helper()
	node := func(id, kind string, bindings map[string]any) map[string]any {
		return map[string]any{"id": id, "nodeRef": schedulerNodeRef(t, builtins, kind), "position": map[string]int{"x": 0, "y": 0}, "config": map[string]any{}, "bindings": bindings}
	}
	value := func(port string, n int) map[string]any {
		return map[string]any{port: map[string]any{"kind": "value", "value": n}}
	}
	edge := func(from, output, to, input string) map[string]any {
		return map[string]any{"channel": "exec", "from": map[string]string{"nodeId": from, "portId": output}, "to": map[string]string{"nodeId": to, "portId": input}}
	}
	edges := []map[string]any{
		edge("started", "started", "outer", "in"), edge("outer", "body", "inner", "in"),
		edge("inner", "body", "body", "in"), edge("inner", "completed", "after-inner", "in"),
		edge("outer", "completed", "after-outer", "in"),
	}
	if target != "" {
		edges = append(edges, edge("body", "done", target, port))
	}
	source := map[string]any{
		"format": "yotta.workflow", "version": "5", "workflow": map[string]string{"id": "wf-nested", "name": "Nested"}, "revision": 0, "entryGraph": "main",
		"graphs": []any{map[string]any{"id": "main", "kind": "main", "nodes": []any{
			node("started", nodes.RunStartedNodeID, map[string]any{}), node("outer", nodes.RepeatNodeID, value("count", count)),
			node("inner", nodes.RepeatNodeID, value("count", 3)), node("body", nodes.DelayNodeID, value("duration-milliseconds", 1)),
			node("after-inner", nodes.DelayNodeID, value("duration-milliseconds", 2)), node("after-outer", nodes.DelayNodeID, value("duration-milliseconds", 3)),
		}, "edges": edges, "inputs": []any{}, "outputs": []any{}}},
		"variables": []any{}, "resources": []any{}, "targetProfileDefinitions": []any{}, "credentialRequirements": []any{}, "dependencies": []any{},
	}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPendingTimersAdvanceIndependentBranches(t *testing.T) {
	builtins := schedulerBuiltins(t)
	for _, overlap := range []bool{false, true} {
		t.Run(fmt.Sprintf("overlap=%t", overlap), func(t *testing.T) {
			program := compileSchedulerInstructionProgram(t, builtins, timerBranchSource(t, builtins, false, overlap))
			now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
			start := now
			options := compiler.ExecutorOptions{Now: func() time.Time { return now }, MonotonicNow: func() time.Time { return now }, Wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }}
			_, journal := runSchedulerInstructionProgram(t, builtins, program, options)
			var completed []string
			for _, fact := range journal.Current().Journal() {
				if fact.Kind == run.JournalNodeAttempt && fact.AttemptOutcome == run.AttemptSucceeded && fact.NodeID != "started" {
					completed = append(completed, fmt.Sprintf("%s:%d", fact.NodeID, fact.Attempt))
				}
			}
			want := []string{"after-inner:1", "after-outer:1", "body:1"}
			elapsed := 100 * time.Millisecond
			if overlap {
				want = []string{"after-inner:1", "body:1", "body:2"}
				elapsed = 110 * time.Millisecond
			}
			if !reflect.DeepEqual(completed, want) || now.Sub(start) != elapsed {
				t.Fatalf("completed=%v elapsed=%v, want %v %v", completed, now.Sub(start), want, elapsed)
			}
		})
	}
}

func TestRegionBreakCancelsPendingSiblingBeforeCompleting(t *testing.T) {
	builtins := schedulerBuiltins(t)
	program := compileSchedulerInstructionProgram(t, builtins, timerBranchSource(t, builtins, true, false))
	now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	start := now
	_, journal := runSchedulerInstructionProgram(t, builtins, program, compiler.ExecutorOptions{
		Now: func() time.Time { return now }, MonotonicNow: func() time.Time { return now }, Wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil },
	})
	cancelled := 0
	for _, fact := range journal.Current().Journal() {
		if fact.Kind == run.JournalNodeAttempt && fact.NodeID == "body" && fact.AttemptOutcome == run.AttemptCancelled {
			cancelled++
		}
	}
	if cancelled != 1 || now.Sub(start) != 15*time.Millisecond || journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("cancelled=%d elapsed=%v status=%s", cancelled, now.Sub(start), journal.Current().Status())
	}
}

func TestRunCancellationDrainsAllPendingTimers(t *testing.T) {
	builtins := schedulerBuiltins(t)
	program := compileSchedulerInstructionProgram(t, builtins, timerBranchSource(t, builtins, false, false))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 7, 17, 2, 0, 0, 0, time.UTC)
	runtime := prepareSchedulerInstructionRuntime(t, builtins, program, compiler.ExecutorOptions{
		Now: func() time.Time { return now }, MonotonicNow: func() time.Time { return now }, Wait: func(context.Context, time.Duration) error { cancel(); return nil },
	})
	_, err := runtime.executor.Run(ctx, program, runtime.owner, runtime.journal)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	terminal := map[string]run.AttemptOutcome{}
	for _, fact := range runtime.journal.Current().Journal() {
		if fact.Kind == run.JournalNodeAttempt {
			terminal[fact.NodeID] = fact.AttemptOutcome
		}
	}
	if terminal["body"] != run.AttemptCancelled || terminal["after-inner"] != run.AttemptCancelled {
		t.Fatalf("terminals=%v", terminal)
	}
	if _, exists := terminal["after-outer"]; exists {
		t.Fatal("cancelled wait executed its continuation")
	}
}

func timerBranchSource(t *testing.T, builtins nodes.Builtins, region, overlap bool) []byte {
	t.Helper()
	var source map[string]any
	if err := json.Unmarshal(nestedRegionSource(t, builtins, 2, "", ""), &source); err != nil {
		t.Fatal(err)
	}
	graph := source["graphs"].([]any)[0].(map[string]any)
	var selected []any
	durations := map[string]int{"body": 100, "after-inner": 10, "after-outer": 5}
	for _, raw := range graph["nodes"].([]any) {
		node := raw.(map[string]any)
		id := node["id"].(string)
		if id == "inner" || id == "outer" && !region {
			continue
		}
		if duration, ok := durations[id]; ok {
			node["bindings"] = map[string]any{"duration-milliseconds": map[string]any{"kind": "value", "value": duration}}
		}
		selected = append(selected, node)
	}
	graph["nodes"] = selected
	edge := func(from, out, to, in string) map[string]any {
		return map[string]any{"channel": "exec", "from": map[string]string{"nodeId": from, "portId": out}, "to": map[string]string{"nodeId": to, "portId": in}}
	}
	if region {
		graph["edges"] = []any{edge("started", "started", "outer", "in"), edge("outer", "body", "body", "in"), edge("outer", "body", "after-inner", "in"), edge("after-inner", "done", "outer", "break"), edge("outer", "completed", "after-outer", "in")}
	} else {
		next := "after-outer"
		if overlap {
			next = "body"
		}
		graph["edges"] = []any{edge("started", "started", "body", "in"), edge("started", "started", "after-inner", "in"), edge("after-inner", "done", next, "in")}
	}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestStructuredPeriodicTasks(t *testing.T) {
	builtins := schedulerBuiltins(t)
	for _, mode := range []string{"periodic", "interrupt", "stop"} {
		t.Run(mode, func(t *testing.T) {
			var source map[string]any
			if err := json.Unmarshal(timerBranchSource(t, builtins, true, false), &source); err != nil {
				t.Fatal(err)
			}
			graph := source["graphs"].([]any)[0].(map[string]any)
			id := nodes.PeriodicNodeID
			count := 3
			if mode != "periodic" {
				id = nodes.MonitorNodeID
				count = 1
			}
			ref := schedulerNodeRef(t, builtins, id)
			for _, raw := range graph["nodes"].([]any) {
				node := raw.(map[string]any)
				if node["id"] == "outer" {
					node["nodeRef"] = ref
					node["bindings"] = map[string]any{"count": map[string]any{"kind": "value", "value": count}, "interval-milliseconds": map[string]any{"kind": "value", "value": 20}}
				}
			}
			edge := func(from, out, to, in string) map[string]any {
				return map[string]any{"channel": "exec", "from": map[string]string{"nodeId": from, "portId": out}, "to": map[string]string{"nodeId": to, "portId": in}}
			}
			edges := []any{edge("started", "started", "outer", "in"), edge("outer", "tick", "after-inner", "in")}
			if mode != "periodic" {
				edges = append(edges, edge("outer", "main", "body", "in"), edge("outer", "handler", "after-outer", "in"), edge("after-inner", "done", "outer", mode))
			}
			graph["edges"] = edges
			raw, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			program := compileSchedulerInstructionProgram(t, builtins, raw)
			now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
			start := now
			result, journal := runSchedulerInstructionProgram(t, builtins, program, compiler.ExecutorOptions{Now: func() time.Time { return now }, MonotonicNow: func() time.Time { return now }, Wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }})
			want := 50 * time.Millisecond
			if mode == "interrupt" {
				want = 105 * time.Millisecond
			}
			if mode == "stop" {
				want = 10 * time.Millisecond
			}
			if now.Sub(start) != want {
				t.Fatalf("elapsed=%v want=%v", now.Sub(start), want)
			}
			if journal.Current().Status() != run.StatusSucceeded {
				t.Fatalf("status=%v", journal.Current().Status())
			}
			if mode == "periodic" && string(result.NodeOutputs["outer"]["index"].InlineJSON()) != "2" {
				t.Fatal("periodic output did not retain final tick")
			}
		})
	}
}

func TestOverduePeriodicTimerCannotStarveItsReadyBody(t *testing.T) {
	b := schedulerBuiltins(t)
	var source map[string]any
	if err := json.Unmarshal(timerBranchSource(t, b, true, false), &source); err != nil {
		t.Fatal(err)
	}
	graph := source["graphs"].([]any)[0].(map[string]any)
	for _, raw := range graph["nodes"].([]any) {
		node := raw.(map[string]any)
		if node["id"] == "outer" {
			node["nodeRef"] = schedulerNodeRef(t, b, nodes.PeriodicNodeID)
			node["bindings"] = map[string]any{"count": map[string]any{"kind": "value", "value": 3}, "interval-milliseconds": map[string]any{"kind": "value", "value": 1}}
		}
	}
	edge := func(from, out, to, in string) map[string]any {
		return map[string]any{"channel": "exec", "from": map[string]string{"nodeId": from, "portId": out}, "to": map[string]string{"nodeId": to, "portId": in}}
	}
	graph["edges"] = []any{edge("started", "started", "outer", "in"), edge("outer", "tick", "after-inner", "in")}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	program := compileSchedulerInstructionProgram(t, b, raw)
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	runtime := prepareSchedulerInstructionRuntime(t, b, program, compiler.ExecutorOptions{Now: func() time.Time { return now }, MonotonicNow: func() time.Time { now = now.Add(25 * time.Millisecond); return now }, Wait: func(_ context.Context, d time.Duration) error { now = now.Add(d); return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := runtime.executor.Run(ctx, program, runtime.owner, runtime.journal)
	if err != nil {
		t.Fatal("overdue timer starved ready scopes", err)
	}
	if string(result.NodeOutputs["outer"]["index"].InlineJSON()) != "2" {
		t.Fatal("periodic body did not finish all three iterations")
	}
}

func TestAsyncPullInputsResumeExactlyOnce(t *testing.T) {
	b := schedulerBuiltins(t)
	var source map[string]any
	if err := json.Unmarshal(timerBranchSource(t, b, false, false), &source); err != nil {
		t.Fatal(err)
	}
	graph := source["graphs"].([]any)[0].(map[string]any)
	makeNode := func(id, typ string, config, bindings map[string]any) map[string]any {
		return map[string]any{"id": id, "nodeRef": schedulerNodeRef(t, b, typ), "position": map[string]int{"x": 0, "y": 0}, "config": config, "bindings": bindings}
	}
	literal := func(value int) map[string]any { return map[string]any{"kind": "value", "value": value} }
	graph["nodes"] = []any{
		makeNode("started", nodes.RunStartedNodeID, map[string]any{}, map[string]any{}),
		makeNode("sum", nodes.IntegerAddNodeID, map[string]any{}, map[string]any{"a": literal(4), "b": literal(5)}),
		makeNode("negative", nodes.IntegerNegateNodeID, map[string]any{}, map[string]any{}),
		makeNode("write", nodes.StateWriteNodeID, map[string]any{"variable": "value"}, map[string]any{}),
	}
	edge := func(channel, from, out, to, in string) map[string]any {
		return map[string]any{"channel": channel, "from": map[string]string{"nodeId": from, "portId": out}, "to": map[string]string{"nodeId": to, "portId": in}}
	}
	graph["edges"] = []any{edge("exec", "started", "started", "write", "in"), edge("data", "sum", "result", "negative", "value"), edge("data", "negative", "result", "write", "value")}
	source["variables"] = []any{map[string]any{"name": "value", "type": map[string]any{"kind": "ref", "ref": b.IntegerType.TypeRef()}, "default": 0}}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	program := compileSchedulerInstructionProgram(t, b, raw)
	runtime := prepareSchedulerInstructionRuntime(t, b, program, compiler.ExecutorOptions{}, func(adapters map[string]nodeadapter.InstalledAdapter) {
		for _, key := range []string{"math.integer-add", "math.integer-negate"} {
			entry := adapters[key]
			entry.Blocking = true
			adapters[key] = entry
		}
	})
	result, err := runtime.executor.Run(context.Background(), program, runtime.owner, runtime.journal)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.NodeOutputs["write"]["result"].InlineJSON()); got != "-9" {
		t.Fatalf("output=%s", got)
	}
	starts := map[string]int{}
	for _, fact := range runtime.journal.Current().Journal() {
		if fact.Kind == run.JournalNodeAttempt && fact.AttemptOutcome == run.AttemptStarted {
			starts[fact.NodeID]++
		}
	}
	for _, id := range []string{"sum", "negative", "write"} {
		if starts[id] != 1 {
			t.Fatalf("%s started %d times", id, starts[id])
		}
	}
}
