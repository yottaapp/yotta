package noderuntime_test

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"testing"
	"time"

	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestWaitStableRunsThroughExactTargetCaptureAndRecordedJournal(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 12, 8))
	for y := range 8 {
		for x := range 12 {
			frame.SetRGBA(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 12), B: 40, A: 255})
		}
	}
	provider := &templateAutomationProvider{frame: frame}
	const targetID, slot = "automation-target/observation", "observation-target"
	program := compilePrimitiveProgram(t, builtins, waitStableSource(builtins, slot))
	now := time.Date(2026, 7, 18, 14, 0, 0, 0, time.UTC)
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
	if provider.captures != 2 || provider.closed != 1 {
		t.Fatalf("capture lifecycle = captures:%d closed:%d", provider.captures, provider.closed)
	}
	if provider.captureFormat != "rgba" || provider.captureRegion == nil ||
		*provider.captureRegion != (automationinstalled.CaptureRegion{X: 0, Y: 0, Width: 1, Height: 1, Unit: "ratio"}) {
		t.Fatalf("observation capture format/region = %q / %#v", provider.captureFormat, provider.captureRegion)
	}
	if got := string(result.NodeOutputs["stable"]["changed-ratio"].InlineJSON()); got != "0" {
		t.Fatalf("changed ratio = %s", got)
	}
	for _, entry := range journal.Current().Journal() {
		if entry.Kind == run.JournalAdapterAction && entry.NodeID == "stable" {
			if entry.EffectID != nodes.WaitStableEffectID || entry.ActionOutcome != run.ActionSucceeded || entry.Summary.Counters["captures"] != 2 {
				t.Fatalf("wait-stable journal = %#v", entry)
			}
			return
		}
	}
	t.Fatal("wait-stable adapter action was not journaled")
}

func waitStableSource(builtins nodes.Builtins, slot string) []byte {
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	stable, _ := builtins.Definition(nodes.WaitStableNodeID)
	// Deadline accounting, including capture time, is covered with a fake clock
	// in automation_observation_test.go. Keep this integration budget above the
	// host scheduler quantum so it tests Target reuse and journaling, not timing.
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-wait-stable","name":"Wait Stable"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"stable","nodeRef":{"nodeTypeId":%q,"version":"1.1.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},
			 "bindings":{"region":{"kind":"default"},"threshold":{"kind":"value","value":0.02},"timeout":{"kind":"value","value":1000},"poll-interval":{"kind":"value","value":10},"grid-size":{"kind":"value","value":4},"cell-delta":{"kind":"value","value":12},"stable-duration":{"kind":"value","value":10}}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"stable","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		stable.Contract.NodeRef().NodeTypeID, stable.Contract.NodeRef().SemanticDigest, slot))
}
