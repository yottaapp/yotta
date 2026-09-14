package noderuntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodeadapter"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

type recordingScriptRuntime struct {
	request  scriptengine.Request
	failure  *scriptengine.Failure
	output   json.RawMessage
	runError error
}

func (runtime *recordingScriptRuntime) Execute(_ context.Context, request scriptengine.Request) (scriptengine.Response, error) {
	runtime.request = request
	if runtime.runError != nil {
		return scriptengine.Response{}, runtime.runError
	}
	response := scriptengine.Response{Protocol: scriptengine.Protocol, AttemptID: request.AttemptID}
	if runtime.failure != nil {
		response.Outcome = scriptengine.OutcomeFailed
		response.Failure = runtime.failure
	} else {
		response.Outcome = scriptengine.OutcomeSucceeded
		response.Output = append(json.RawMessage(nil), runtime.output...)
	}
	return response, nil
}

func TestScriptExecuteRunsThroughInjectedIsolationAndJournalsOnlyRedactedFacts(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	scriptRef := builtins.ScriptExecuteContract.NodeRef()
	sourceText := `return {answer: input.value + 1, now: Date.now()};`
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-script","name":"Script"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"script","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},
			 "config":{"source":%q,"timeoutMilliseconds":2500},"bindings":{"input":{"kind":"value","value":{"value":41}}}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"script","portId":"in"}}],
		"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest, scriptRef.NodeTypeID, scriptRef.SemanticDigest, sourceText))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 7, 16, 8, 30, 0, 123_000_000, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	isolation := &recordingScriptRuntime{output: json.RawMessage(`{"answer":42,"now":1784190600123}`)}
	adapters, err := noderuntime.Installed(builtins, noderuntime.Dependencies{Script: isolation, Log: unusedLogEmitter{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{
		Now: func() time.Time { return now }, Entropy: bytes.NewReader(bytes.Repeat([]byte{0x5a}, 32)),
	}).Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.NodeOutputs["script"]["result"].InlineJSON()); got != `{"answer":42,"now":1784190600123}` {
		t.Fatalf("script result = %s", got)
	}
	if isolation.request.Source != sourceText || string(isolation.request.Input) != `{"value":41}` ||
		isolation.request.TimeoutMillis != 2500 || isolation.request.EpochUnixMillis != now.UnixMilli() ||
		isolation.request.RandomSeed != strings.Repeat("5a", 32) || isolation.request.AttemptID == "" {
		t.Fatalf("script request = %#v", isolation.request)
	}
	var action *run.JournalEntry
	for _, fact := range journal.Current().Journal() {
		if fact.Kind == run.JournalAdapterAction && fact.NodeID == "script" {
			copy := fact
			action = &copy
		}
	}
	if action == nil || action.EffectID != nodes.ScriptExecuteEffectID || action.ActionOutcome != run.ActionSucceeded ||
		action.Summary.Counters["source_bytes"] != int64(len(sourceText)) || action.Summary.Counters["input_bytes"] != 12 ||
		action.Summary.Counters["output_bytes"] != 33 || action.Summary.Facts["source_digest"] == "" ||
		action.Summary.Facts["protocol"] != scriptengine.Protocol {
		t.Fatalf("script action = %#v", action)
	}
	encoded, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{sourceText, `{"value":41}`, `{"answer":42`} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("journal leaked script payload %q: %s", secret, encoded)
		}
	}
}

func TestScriptExecuteRoutesTypedWorkerFailure(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	isolation := &recordingScriptRuntime{failure: &scriptengine.Failure{
		Code: scriptengine.CodeGuestThrown, Message: "isolated script execution failed",
	}}
	installed, err := noderuntime.Installed(builtins, noderuntime.Dependencies{Script: isolation, Log: unusedLogEmitter{}})
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := builtins.Definition(nodes.ScriptExecuteNodeID)
	input, err := datatype.SealInlineJSON(
		builtins.Catalog, datatype.RefResolvedType(builtins.JSONType.TypeRef()), json.RawMessage(`{"value":1}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var action nodeadapter.AdapterAction
	_, runErr := installed[definition.Implementation.Entrypoint].Run(context.Background(), nodeadapter.Invocation{
		InvocationID: "attempt-1", Attempt: 1, GraphID: "main", NodeID: "script",
		Config:      map[string]any{"source": `throw new Error("secret")`, "timeoutMilliseconds": json.Number("1000")},
		Inputs:      map[string]datatype.ValueEnvelope{"input": input},
		OutputTypes: map[string]datatype.ResolvedType{"result": datatype.RefResolvedType(builtins.JSONType.TypeRef())},
		ObservedAt:  time.UnixMilli(1_700_000_000_000), ReadEntropy: func(target []byte) error {
			copy(target, bytes.Repeat([]byte{1}, len(target)))
			return nil
		},
		RecordAction: func(_ context.Context, value nodeadapter.AdapterAction) error { action = value; return nil },
	})
	var failure *nodeadapter.NodeFailure
	if !errors.As(runErr, &failure) || failure.Code != scriptengine.CodeGuestThrown || failure.Output != "failed" {
		t.Fatalf("script failure = %#v / %v", failure, runErr)
	}
	if action.Outcome != run.ActionFailed || action.ErrorCode != scriptengine.CodeGuestThrown {
		t.Fatalf("script action = %#v", action)
	}
}
