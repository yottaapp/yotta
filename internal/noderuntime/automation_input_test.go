package noderuntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

type automationInputProvider struct {
	requests []automationinstalled.TypeTextRequest
	closed   int
}

type heldInputProvider struct {
	operations []string
	closed     int
}

type pointerPositionProvider struct{ closed int }

func (provider *pointerPositionProvider) Open(_ context.Context, request resource.ProviderOpenRequest) (any, error) {
	if request.Kind != automationinstalled.KindInput || fmt.Sprint(request.Operations) != "[pointer-position]" {
		return nil, fmt.Errorf("unexpected pointer position open request: %#v", request)
	}
	return &struct{}{}, nil
}
func (provider *pointerPositionProvider) Invoke(_ context.Context, _ any, operation string, payload []byte) ([]byte, error) {
	if operation != automationinstalled.OperationPointerPosition || string(payload) != `{}` {
		return nil, fmt.Errorf("unexpected pointer position invocation: %s %s", operation, payload)
	}
	return []byte(`{"point":{"unit":"ratio","x":0.25,"y":0.75}}`), nil
}
func (provider *pointerPositionProvider) Close(context.Context, any) error {
	provider.closed++
	return nil
}

func (provider *heldInputProvider) Open(_ context.Context, request resource.ProviderOpenRequest) (any, error) {
	if request.Kind != automationinstalled.KindHeldInput || fmt.Sprint(request.Operations) != "[hold-button hold-keys release-held]" ||
		string(request.Config) != `{}` || len(request.CapabilityScope) != 0 {
		return nil, fmt.Errorf("unexpected held input open request: %#v", request)
	}
	return &struct{}{}, nil
}

func (provider *heldInputProvider) Invoke(_ context.Context, _ any, operation string, _ []byte) ([]byte, error) {
	provider.operations = append(provider.operations, operation)
	return []byte(`{}`), nil
}

func (provider *heldInputProvider) Close(context.Context, any) error {
	provider.closed++
	return nil
}

func (provider *automationInputProvider) Open(_ context.Context, request resource.ProviderOpenRequest) (any, error) {
	if request.Kind != automationinstalled.KindInput || fmt.Sprint(request.Operations) != "[type-text]" || string(request.Config) != `{}` || len(request.CapabilityScope) != 0 {
		return nil, fmt.Errorf("unexpected automation input open request: %#v", request)
	}
	return request.Operations[0], nil
}

func (provider *automationInputProvider) Invoke(_ context.Context, object any, operation string, payload []byte) ([]byte, error) {
	if object != automationinstalled.OperationTypeText || operation != automationinstalled.OperationTypeText {
		return nil, errors.New("unexpected automation input operation")
	}
	var request automationinstalled.TypeTextRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	provider.requests = append(provider.requests, request)
	return []byte(`{}`), nil
}

func (provider *automationInputProvider) Close(context.Context, any) error {
	provider.closed++
	return nil
}

func TestAutomationInputUsesInstalledTargetAndRedactsJournal(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	provider := &automationInputProvider{}
	const targetID, slot = "automation-target/test", "automation-test"
	program := compilePrimitiveProgram(t, builtins, automationInputSource(t, builtins, slot))
	now := time.Date(2026, 7, 16, 19, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	targets := configuredTargetRun(t, slot, targetID, provider)
	result, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).RunWithTargets(context.Background(), program, owner, targets, journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.NodeOutputs["type"]) != 0 || len(provider.requests) != 1 || provider.requests[0].Text != "private text 节点" || provider.closed != 1 {
		t.Fatalf("outputs=%#v requests=%#v closed=%d", result.NodeOutputs["type"], provider.requests, provider.closed)
	}
	actions := 0
	for _, entry := range journal.Current().Journal() {
		if entry.Kind != run.JournalAdapterAction {
			continue
		}
		actions++
		raw, _ := json.Marshal(entry)
		if bytes.Contains(raw, []byte("private text")) || bytes.Contains(raw, []byte("节点")) {
			t.Fatalf("journal leaked typed text: %s", raw)
		}
	}
	if actions != 1 {
		t.Fatalf("automation actions = %d", actions)
	}
}

func TestHeldInputLeaseCrossesNodesAndIsClosedByRun(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	provider := &heldInputProvider{}
	const targetID, slot = "automation-target/held", "automation-held"
	program := compilePrimitiveProgram(t, builtins, heldInputSource(t, builtins, slot))
	now := time.Date(2026, 7, 18, 22, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	targets := configuredTargetRun(t, slot, targetID, provider)
	result, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).RunWithTargets(context.Background(), program, owner, targets, journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.NodeOutputs["hold"]) != 0 || fmt.Sprint(provider.operations) != "[hold-keys release-held]" || provider.closed != 1 {
		t.Fatalf("outputs=%#v operations=%v closed=%d", result.NodeOutputs["hold"], provider.operations, provider.closed)
	}
}

func TestGetPointerPositionProducesReusableNormalizedPoint(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	provider := &pointerPositionProvider{}
	const targetID, slot = "automation-target/pointer", "automation-pointer"
	program := compilePrimitiveProgram(t, builtins, pointerPositionSource(t, builtins, slot))
	now := time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).
		RunWithTargets(context.Background(), program, owner, configuredTargetRun(t, slot, targetID, provider), journal)
	if err != nil {
		t.Fatal(err)
	}
	point := result.NodeOutputs["position"]["point"].InlineJSON()
	if string(point) != `{"unit":"ratio","x":0.25,"y":0.75}` || provider.closed != 1 {
		t.Fatalf("point=%s closed=%d", point, provider.closed)
	}
}

func automationInputSource(t *testing.T, builtins nodes.Builtins, slot string) []byte {
	t.Helper()
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	typeText, _ := builtins.Definition(nodes.TypeTextNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-automation-input","name":"Automation Input"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"type","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},
			 "bindings":{"text":{"kind":"value","value":"private text 节点"}}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"type","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		typeText.Contract.NodeRef().NodeTypeID, typeText.Contract.NodeRef().SemanticDigest, slot))
}

func heldInputSource(t *testing.T, builtins nodes.Builtins, slot string) []byte {
	t.Helper()
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	hold, _ := builtins.Definition(nodes.HoldKeysNodeID)
	release, _ := builtins.Definition(nodes.ReleaseHeldInputNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-held-input","name":"Held Input"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"hold","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},"bindings":{"keys":{"kind":"value","value":["CTRL"]}}},
			{"id":"release","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":2,"y":0},"config":{"slot":%q},"bindings":{}}
		],"edges":[
			{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"hold","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"hold","portId":"completed"},"to":{"nodeId":"release","portId":"in"}},
			{"channel":"data","from":{"nodeId":"hold","portId":"held"},"to":{"nodeId":"release","portId":"held"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		hold.Contract.NodeRef().NodeTypeID, hold.Contract.NodeRef().SemanticDigest, slot,
		release.Contract.NodeRef().NodeTypeID, release.Contract.NodeRef().SemanticDigest, slot))
}

func pointerPositionSource(t *testing.T, builtins nodes.Builtins, slot string) []byte {
	t.Helper()
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	position, _ := builtins.Definition(nodes.GetPointerPositionNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-pointer-position","name":"Pointer Position"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"position","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},"bindings":{}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"position","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		position.Contract.NodeRef().NodeTypeID, position.Contract.NodeRef().SemanticDigest, slot))
}
