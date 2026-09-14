package noderuntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/appcontrol"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

type applicationProvider struct{ operations []string }

func (p *applicationProvider) Open(_ context.Context, request resource.ProviderOpenRequest) (any, error) {
	if request.Kind != appcontrol.KindApplication || len(request.Operations) != 1 || len(request.Config) != 2 {
		return nil, fmt.Errorf("unexpected application open request: %#v", request)
	}
	p.operations = append(p.operations, "open:"+request.Operations[0])
	return request.Operations[0], nil
}
func (p *applicationProvider) Invoke(_ context.Context, object any, operation string, payload []byte) ([]byte, error) {
	if object != operation || string(payload) != `{}` {
		return nil, fmt.Errorf("unexpected application invocation")
	}
	p.operations = append(p.operations, "invoke:"+operation)
	if operation == appcontrol.OperationLaunch {
		return artifact.Marshal(appcontrol.LaunchResponse{ProcessID: 4242})
	}
	return artifact.Marshal(appcontrol.TerminateResponse{TerminatedCount: 2})
}
func (*applicationProvider) Close(context.Context, any) error { return nil }

func TestApplicationLifecycleUsesInstalledTargetAndRedactsJournal(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	provider := &applicationProvider{}
	const targetID, slot = "configured-application/test", "application-test"
	program := compilePrimitiveProgram(t, builtins, applicationSource(t, builtins, slot))
	now := time.Date(2026, 7, 16, 18, 0, 0, 0, time.UTC)
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
	if got := string(result.NodeOutputs["stop"]["terminated-count"].InlineJSON()); got != "2" {
		t.Fatalf("terminated-count = %s", got)
	}
	if fmt.Sprint(provider.operations) != "[open:launch invoke:launch open:terminate invoke:terminate]" {
		t.Fatalf("operations = %v", provider.operations)
	}
	actions := 0
	for _, entry := range journal.Current().Journal() {
		if entry.Kind != run.JournalAdapterAction {
			continue
		}
		actions++
		raw, _ := json.Marshal(entry)
		if bytes.Contains(raw, []byte("C:\\Program Files")) || bytes.Contains(raw, []byte("--secret")) || bytes.Contains(raw, []byte("4242")) {
			t.Fatalf("journal leaked application identity: %s", raw)
		}
	}
	if actions != 2 {
		t.Fatalf("application actions = %d", actions)
	}
}

func applicationSource(t *testing.T, builtins nodes.Builtins, slot string) []byte {
	t.Helper()
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	launch, _ := builtins.Definition(nodes.LaunchApplicationNodeID)
	terminate, _ := builtins.Definition(nodes.TerminateApplicationNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-application","name":"Application"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"launch","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},"bindings":{}},
			{"id":"stop","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":2,"y":0},"config":{"slot":%q},"bindings":{}}
		],"edges":[
			{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"launch","portId":"in"}},
			{"channel":"exec","from":{"nodeId":"launch","portId":"completed"},"to":{"nodeId":"stop","portId":"in"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		launch.Contract.NodeRef().NodeTypeID, launch.Contract.NodeRef().SemanticDigest, slot,
		terminate.Contract.NodeRef().NodeTypeID, terminate.Contract.NodeRef().SemanticDigest, slot))
}
