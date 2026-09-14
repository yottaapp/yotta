package noderuntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestRecordedRandomAndTimeUseHostFactsAndPersistActions(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	randomDefinition, _ := builtins.Definition(nodes.RandomIntegerNodeID)
	timeDefinition, _ := builtins.Definition(nodes.ObserveTimeNodeID)
	randomRef, timeRef := randomDefinition.Contract.NodeRef(), timeDefinition.Contract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-observations","name":"Observations"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"random","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"minimum":{"kind":"value","value":10},"maximum":{"kind":"value","value":20},"distribution":{"kind":"value","value":"uniform"}}},
			{"id":"time","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},"bindings":{}}
		],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, randomRef.NodeTypeID, randomRef.SemanticDigest, timeRef.NodeTypeID, timeRef.SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{
		Now: func() time.Time { return now.Add(2 * time.Second) }, Entropy: bytes.NewReader(append(make([]byte, 7), 11)),
	}).Run(context.Background(), program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	var randomValue, observedAt int64
	if err := json.Unmarshal(execution.NodeOutputs["random"]["result"].InlineJSON(), &randomValue); err != nil || randomValue != 10 {
		t.Fatalf("random result=%d err=%v", randomValue, err)
	}
	if err := json.Unmarshal(execution.NodeOutputs["time"]["result"].InlineJSON(), &observedAt); err != nil || observedAt != now.Add(2*time.Second).UnixMilli() {
		t.Fatalf("time result=%d err=%v", observedAt, err)
	}
	actions := 0
	for _, fact := range journal.Current().Journal() {
		if fact.Kind != run.JournalAdapterAction {
			continue
		}
		actions++
		if fact.ActionOutcome != run.ActionSucceeded || (fact.EffectID != nodes.RandomSampleEffectID && fact.EffectID != nodes.TimeObserveEffectID) {
			t.Fatalf("recorded action = %#v", fact)
		}
	}
	if actions != 2 || journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("actions=%d run=%#v", actions, journal.Current())
	}
}

func TestEntropyFailureIsADeclaredRecordedRunFailure(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition, _ := builtins.Definition(nodes.RandomBooleanNodeID)
	ref := definition.Contract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-entropy-failure","name":"Entropy failure"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"random","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			"bindings":{"probability":{"kind":"value","value":0.5}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest))
	program := compilePrimitiveProgram(t, builtins, source)
	now := time.Date(2026, 7, 15, 12, 30, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{
		Now: func() time.Time { return now }, Entropy: failingReader{},
	}).Run(context.Background(), program, owner, journal)
	failure, ok := journal.Current().Failure()
	if runErr == nil || !ok || failure.Code != nodes.RandomEntropyUnavailableCode || journal.Current().Status() != run.StatusFailed {
		t.Fatalf("runErr=%v failure=%#v record=%#v", runErr, failure, journal.Current())
	}
	found := false
	for _, fact := range journal.Current().Journal() {
		if fact.Kind == run.JournalAdapterAction && fact.ActionOutcome == run.ActionFailed && fact.ErrorCode == nodes.RandomEntropyUnavailableCode {
			found = true
		}
	}
	if !found {
		t.Fatal("entropy failure was not journaled as the declared adapter action")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy offline") }
