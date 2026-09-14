package noderuntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/httpegress"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestHTTPGetUsesConfiguredTargetAndRedactsRequestJournal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/status" || request.URL.Query().Get("token") != "private-value" || request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" {
			t.Errorf("request = %s, headers = %#v", request.URL.String(), request.Header)
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Set-Cookie", "secret=value")
		_, _ = response.Write([]byte(`{"ready":true}`))
	}))
	defer server.Close()

	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := httpegress.SealProfile(httpegress.ProfileDraft{Origin: server.URL, ResponseByteLimit: 4096, TimeoutMilliseconds: 5000})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := httpegress.NewProvider(profile)
	if err != nil {
		t.Fatal(err)
	}
	const targetID, slot = "http-target/test", "http-test"
	program := compilePrimitiveProgram(t, builtins, httpSource(t, builtins))
	now := time.Date(2026, 7, 16, 16, 0, 0, 0, time.UTC)
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
	if got := string(result.NodeOutputs["fetch"]["status"].InlineJSON()); got != "200" {
		t.Fatalf("status = %s", got)
	}
	if got := string(result.NodeOutputs["fetch"]["body"].InlineJSON()); got != `"{\"ready\":true}"` {
		t.Fatalf("body = %s", got)
	}
	if got := string(result.NodeOutputs["fetch"]["content-type"].InlineJSON()); got != `"application/json"` {
		t.Fatalf("content type = %s", got)
	}
	for _, entry := range journal.Current().Journal() {
		if entry.Kind != run.JournalAdapterAction || entry.NodeID != "fetch" {
			continue
		}
		if entry.ActionOutcome != run.ActionSucceeded || entry.Summary.Counters["status_code"] != 200 || entry.Summary.Facts["path_digest"] == "" {
			t.Fatalf("action = %#v", entry)
		}
		raw, _ := json.Marshal(entry)
		if bytes.Contains(raw, []byte("/status")) || bytes.Contains(raw, []byte("private-value")) || bytes.Contains(raw, []byte("ready")) {
			t.Fatalf("journal leaked request or response: %s", raw)
		}
		return
	}
	t.Fatal("HTTP adapter action was not journaled")
}

func httpSource(t *testing.T, builtins nodes.Builtins) []byte {
	t.Helper()
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	fetch, _ := builtins.Definition(nodes.HTTPGetNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-http","name":"HTTP"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"fetch","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":"http-test"},
			 "bindings":{"path":{"kind":"value","value":"/status"},"query":{"kind":"value","value":{"token":["private-value"]}}}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"fetch","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest, fetch.Contract.NodeRef().NodeTypeID, fetch.Contract.NodeRef().SemanticDigest))
}
