package noderuntime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

type windowOperationsProvider struct {
	operations []string
	requests   map[string]json.RawMessage
	closed     int
}

func (provider *windowOperationsProvider) Open(_ context.Context, request resource.ProviderOpenRequest) (any, error) {
	if request.Kind != automationinstalled.KindWindow || len(request.Operations) != 1 || string(request.Config) != `{}` {
		return nil, fmt.Errorf("unexpected window operation open request: %#v", request)
	}
	operation := request.Operations[0]
	if len(request.CapabilityScope) != 0 {
		return nil, fmt.Errorf("unexpected window operation scope: %s", request.CapabilityScope)
	}
	return operation, nil
}

func (provider *windowOperationsProvider) Invoke(_ context.Context, object any, operation string, payload []byte) ([]byte, error) {
	if object != operation {
		return nil, fmt.Errorf("window operation object=%v operation=%q", object, operation)
	}
	provider.operations = append(provider.operations, operation)
	provider.requests[operation] = append([]byte(nil), payload...)
	switch operation {
	case automationinstalled.OperationGetWindowState:
		return artifact.Marshal(automationinstalled.WindowStateResponse{State: "normal", Foreground: true, X: 10, Y: 20, Width: 800, Height: 600})
	case automationinstalled.OperationWaitWindow:
		return artifact.Marshal(automationinstalled.WaitWindowResponse{Matched: true})
	default:
		return []byte(`{}`), nil
	}
}

func (provider *windowOperationsProvider) Close(context.Context, any) error {
	provider.closed++
	return nil
}

func TestDesktopWindowOperationFamilyUsesTypedRequestsOutputsAndRoutes(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	provider := &windowOperationsProvider{requests: map[string]json.RawMessage{}}
	const targetID, slot = "automation-target/window-operations", "window-operations"
	program := compilePrimitiveProgram(t, builtins, desktopWindowOperationsSource(builtins, slot))
	now := time.Date(2026, 7, 18, 23, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	targets := configuredTargetRun(t, slot, targetID, provider)
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).
		RunWithTargets(context.Background(), program, owner, targets, journal)
	if err != nil {
		t.Fatal(err)
	}
	wantOperations := fmt.Sprint([]string{
		automationinstalled.OperationMoveResizeWindow, automationinstalled.OperationGetWindowState,
		automationinstalled.OperationWaitWindow, automationinstalled.OperationSetWindowState, automationinstalled.OperationCloseWindow,
	})
	if fmt.Sprint(provider.operations) != wantOperations || provider.closed != len(provider.operations) {
		t.Fatalf("operations=%v closed=%d", provider.operations, provider.closed)
	}
	var move automationinstalled.MoveResizeWindowRequest
	if err := json.Unmarshal(provider.requests[automationinstalled.OperationMoveResizeWindow], &move); err != nil ||
		move != (automationinstalled.MoveResizeWindowRequest{X: 10, Y: 20, Width: 800, Height: 600}) {
		t.Fatalf("move request=%#v error=%v", move, err)
	}
	var wait automationinstalled.WaitWindowRequest
	if err := json.Unmarshal(provider.requests[automationinstalled.OperationWaitWindow], &wait); err != nil || wait.TimeoutMilliseconds != 250 {
		t.Fatalf("wait request=%#v error=%v", wait, err)
	}
	var stateRequest automationinstalled.SetWindowStateRequest
	if err := json.Unmarshal(provider.requests[automationinstalled.OperationSetWindowState], &stateRequest); err != nil || stateRequest.State != "minimize" {
		t.Fatalf("state request=%#v error=%v", stateRequest, err)
	}
	wants := map[string]string{"state": `"normal"`, "foreground": `true`, "x": `10`, "y": `20`, "width": `800`, "height": `600`}
	for portID, want := range wants {
		if got := string(result.NodeOutputs["state"][portID].InlineJSON()); got != want {
			t.Fatalf("window state output %s=%s want=%s", portID, got, want)
		}
	}
}

func desktopWindowOperationsSource(builtins nodes.Builtins, slot string) []byte {
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	move, _ := builtins.Definition(nodes.MoveResizeWindowNodeID)
	state, _ := builtins.Definition(nodes.GetWindowStateNodeID)
	wait, _ := builtins.Definition(nodes.WaitWindowNodeID)
	minimize, _ := builtins.Definition(nodes.MinimizeWindowNodeID)
	closeWindow, _ := builtins.Definition(nodes.CloseWindowNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-window-operations","name":"Window operations"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"move","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},"bindings":{"x":{"kind":"value","value":10},"y":{"kind":"value","value":20},"width":{"kind":"value","value":800},"height":{"kind":"value","value":600}}},
			{"id":"state","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":2,"y":0},"config":{"slot":%q},"bindings":{}},
			{"id":"wait","nodeRef":{"nodeTypeId":%q,"version":"1.1.0","semanticDigest":%q},"position":{"x":3,"y":0},"config":{"slot":%q},"bindings":{"timeout":{"kind":"value","value":250}}},
			{"id":"minimize","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":4,"y":0},"config":{"slot":%q},"bindings":{}},
			{"id":"close","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":5,"y":0},"config":{"slot":%q},"bindings":{}}
		],"edges":[
			{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"move","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"move","portId":"completed"},"to":{"nodeId":"state","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"state","portId":"completed"},"to":{"nodeId":"wait","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"wait","portId":"found"},"to":{"nodeId":"minimize","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"minimize","portId":"completed"},"to":{"nodeId":"close","portId":"in"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		move.Contract.NodeRef().NodeTypeID, move.Contract.NodeRef().SemanticDigest, slot,
		state.Contract.NodeRef().NodeTypeID, state.Contract.NodeRef().SemanticDigest, slot,
		wait.Contract.NodeRef().NodeTypeID, wait.Contract.NodeRef().SemanticDigest, slot,
		minimize.Contract.NodeRef().NodeTypeID, minimize.Contract.NodeRef().SemanticDigest, slot,
		closeWindow.Contract.NodeRef().NodeTypeID, closeWindow.Contract.NodeRef().SemanticDigest, slot))
}
