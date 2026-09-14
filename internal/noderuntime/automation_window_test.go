package noderuntime_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/nodeadapter"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

type automationWindowProvider struct {
	invoked int
	closed  int
	err     error
}

func (provider *automationWindowProvider) Open(_ context.Context, request resource.ProviderOpenRequest) (any, error) {
	if request.Kind != automationinstalled.KindWindow || fmt.Sprint(request.Operations) != "[activate]" || string(request.Config) != `{}` || len(request.CapabilityScope) != 0 {
		return nil, fmt.Errorf("unexpected automation window open request: %#v", request)
	}
	return automationinstalled.OperationActivate, nil
}

func (provider *automationWindowProvider) Invoke(_ context.Context, object any, operation string, payload []byte) ([]byte, error) {
	if object != automationinstalled.OperationActivate || operation != automationinstalled.OperationActivate || string(payload) != `{}` {
		return nil, errors.New("unexpected automation window operation")
	}
	provider.invoked++
	if provider.err != nil {
		return nil, provider.err
	}
	return []byte(`{}`), nil
}

func (provider *automationWindowProvider) Close(context.Context, any) error {
	provider.closed++
	return nil
}

func TestActivateWindowUsesConfiguredTargetAndJournal(t *testing.T) {
	provider := &automationWindowProvider{}
	journal, err := executeWindowActivation(t, provider)
	if err != nil {
		t.Fatal(err)
	}
	if provider.invoked != 1 || provider.closed != 1 {
		t.Fatalf("invoked=%d closed=%d", provider.invoked, provider.closed)
	}
	actions := 0
	for _, entry := range journal.Current().Journal() {
		if entry.Kind == run.JournalAdapterAction {
			actions++
			if entry.Action != "automation.activate-window" || entry.Summary.Code != "automation.activate-window" || entry.ActionOutcome != run.ActionSucceeded {
				t.Fatalf("action = %#v", entry)
			}
		}
	}
	if actions != 1 {
		t.Fatalf("actions=%d", actions)
	}
}

func TestActivateWindowFailureIsNotReportedAsCompleted(t *testing.T) {
	provider := &automationWindowProvider{err: &automationinstalled.Failure{Code: automationinstalled.CodeWindowFailed, Cause: errors.New("focus denied")}}
	journal, err := executeWindowActivation(t, provider)
	var failure *nodeadapter.NodeFailure
	if !errors.As(err, &failure) || failure.Code != automationinstalled.CodeWindowFailed || failure.Output != "failed" {
		t.Fatalf("error=%v", err)
	}
	for _, entry := range journal.Current().Journal() {
		if entry.Kind == run.JournalAdapterAction && entry.ActionOutcome == run.ActionSucceeded {
			t.Fatalf("failed activation recorded success: %#v", entry)
		}
	}
}

func executeWindowActivation(t *testing.T, provider *automationWindowProvider) (*run.JournalWriter, error) {
	t.Helper()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	const targetID, slot = "automation-target/test", "automation-test"
	program := compilePrimitiveProgram(t, builtins, automationWindowSource(t, builtins, slot))
	now := time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	targets := configuredTargetRun(t, slot, targetID, provider)
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).
		RunWithTargets(context.Background(), program, owner, targets, journal)
	return journal, runErr
}

func automationWindowSource(t *testing.T, builtins nodes.Builtins, slot string) []byte {
	t.Helper()
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	activate, _ := builtins.Definition(nodes.ActivateWindowNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-automation-window","name":"Automation Window"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"activate","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},"bindings":{}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"activate","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		activate.Contract.NodeRef().NodeTypeID, activate.Contract.NodeRef().SemanticDigest, slot))
}
