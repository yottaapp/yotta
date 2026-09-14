package noderuntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"strings"
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

type dualColorBarProvider struct {
	frames       [][]byte
	frameIndex   int
	currentFrame []byte
	requests     []automationinstalled.CaptureRequest
	keys         []automationinstalled.PressKeysRequest
}

func (provider *dualColorBarProvider) Open(_ context.Context, request resource.ProviderOpenRequest) (any, error) {
	switch request.Kind {
	case automationinstalled.KindCapture:
		return automationinstalled.KindCapture, nil
	case automationinstalled.KindInput:
		if fmt.Sprint(request.Operations) != "[press-keys]" {
			return nil, fmt.Errorf("unexpected input operations: %v", request.Operations)
		}
		return automationinstalled.KindInput, nil
	default:
		return nil, fmt.Errorf("unexpected resource kind %q", request.Kind)
	}
}

func (provider *dualColorBarProvider) Invoke(_ context.Context, object any, operation string, payload []byte) ([]byte, error) {
	switch object {
	case automationinstalled.KindCapture:
		switch operation {
		case automationinstalled.OperationCapture:
			var request automationinstalled.CaptureRequest
			if err := json.Unmarshal(payload, &request); err != nil || request.Region == nil || request.Format != automationinstalled.CaptureFormatRGBA {
				return nil, errors.New("unexpected regional capture request")
			}
			provider.requests = append(provider.requests, request)
			provider.currentFrame = provider.frames[min(provider.frameIndex, len(provider.frames)-1)]
			provider.frameIndex++
			return artifact.Marshal(automationinstalled.CaptureResponse{MediaType: automationinstalled.CaptureMediaTypeRGBA, Size: int64(len(provider.currentFrame)), Width: 160, Height: 40})
		case automationinstalled.OperationReadCapture:
			var request automationinstalled.CaptureRangeRequest
			if err := json.Unmarshal(payload, &request); err != nil || request.Offset < 0 || request.Length <= 0 || request.Offset+request.Length > int64(len(provider.currentFrame)) {
				return nil, errors.New("unexpected capture range")
			}
			return append([]byte(nil), provider.currentFrame[request.Offset:request.Offset+request.Length]...), nil
		}
	case automationinstalled.KindInput:
		if operation != automationinstalled.OperationPressKeys {
			return nil, errors.New("unexpected input operation")
		}
		var request automationinstalled.PressKeysRequest
		if err := json.Unmarshal(payload, &request); err != nil {
			return nil, err
		}
		provider.keys = append(provider.keys, request)
		return []byte(`{}`), nil
	}
	return nil, errors.New("unexpected dual color bar invocation")
}

func (*dualColorBarProvider) Close(context.Context, any) error { return nil }

func TestControlDualColorBarRunsFreshRegionalFramesInsideOneRecordedNode(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	right := dualColorBarFrame(63)
	left := dualColorBarFrame(77)
	blank := image.NewRGBA(image.Rect(0, 0, 160, 40))
	provider := &dualColorBarProvider{frames: [][]byte{right.Pix, left.Pix, blank.Pix}}
	const targetID, slot = "automation-target/dual-color-bar", "dual-color-bar-target"
	program := compilePrimitiveProgram(t, builtins, dualColorBarSource(builtins, slot))
	now := time.Date(2026, 8, 1, 5, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	targets := configuredTargetRun(t, slot, targetID, provider)
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).
		RunWithTargets(context.Background(), program, owner, targets, journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 3 || provider.requests[0].Region.X != 0.319 || provider.requests[0].Region.Width != 0.367 {
		t.Fatalf("regional capture requests = %#v", provider.requests)
	}
	if len(provider.keys) != 2 || fmt.Sprint(provider.keys[0].Keys) != "[D]" || fmt.Sprint(provider.keys[1].Keys) != "[A]" || provider.keys[0].DurationMilliseconds != 35 {
		t.Fatalf("feedback keys = %#v", provider.keys)
	}
	if got := string(execution.NodeOutputs["control"]["frames"].InlineJSON()); got != "3" {
		t.Fatalf("frames = %s", got)
	}
	actions := 0
	for _, entry := range journal.Current().Journal() {
		if entry.Action == "automation.control-dual-color-bar" {
			actions++
			if entry.Summary.Counters["frames"] != 3 || entry.Summary.Counters["right_actions"] != 1 || entry.Summary.Counters["left_actions"] != 1 ||
				entry.Summary.Counters["paced_cycles"] != 2 || entry.Summary.Counters["pacing_wait_ms"] <= 0 {
				t.Fatalf("action counters = %#v", entry.Summary.Counters)
			}
			for _, counter := range []string{"capture_ms", "analysis_ms"} {
				if _, ok := entry.Summary.Counters[counter]; !ok {
					t.Fatalf("action counters omitted %q: %#v", counter, entry.Summary.Counters)
				}
			}
		}
	}
	if actions != 1 {
		t.Fatalf("recorded actions = %d, want one aggregate action", actions)
	}
}

func TestControlDualColorBarActivatesAndWaitsForTheFirstBarInsideTheSameNode(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	blank := image.NewRGBA(image.Rect(0, 0, 160, 40))
	right := dualColorBarFrame(63)
	provider := &dualColorBarProvider{frames: [][]byte{blank.Pix, blank.Pix, right.Pix, blank.Pix}}
	const targetID, slot = "automation-target/activated-dual-color-bar", "activated-dual-color-bar-target"
	source := strings.ReplaceAll(string(dualColorBarSource(builtins, slot)),
		`"activation-keys":{"kind":"default"}`,
		`"activation-keys":{"kind":"value","value":["F"]}`,
	)
	source = strings.ReplaceAll(source,
		`"appearance-timeout":{"kind":"default"}`,
		`"appearance-timeout":{"kind":"value","value":1000}`,
	)
	source = strings.ReplaceAll(source,
		`"appearance-poll-duration":{"kind":"value","value":0}`,
		`"appearance-poll-duration":{"kind":"value","value":1}`,
	)
	source = strings.ReplaceAll(source,
		`"activation-retry-duration":{"kind":"default"}`,
		`"activation-retry-duration":{"kind":"value","value":1}`,
	)
	program := compilePrimitiveProgram(t, builtins, []byte(source))
	now := time.Date(2026, 8, 1, 5, 30, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	targets := configuredTargetRun(t, slot, targetID, provider)
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	execution, err := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).
		RunWithTargets(context.Background(), program, owner, targets, journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.keys) != 4 || fmt.Sprint(provider.keys[0].Keys) != "[F]" || fmt.Sprint(provider.keys[1].Keys) != "[F]" ||
		fmt.Sprint(provider.keys[2].Keys) != "[F]" || fmt.Sprint(provider.keys[3].Keys) != "[D]" {
		t.Fatalf("activation/control keys = %#v", provider.keys)
	}
	if got := string(execution.NodeOutputs["control"]["activation-actions"].InlineJSON()); got != "3" {
		t.Fatalf("activation-actions = %s", got)
	}
}

func dualColorBarFrame(innerX int) *image.RGBA {
	frame := image.NewRGBA(image.Rect(0, 0, 160, 40))
	for y := 12; y < 15; y++ {
		for x := 50; x < 91; x++ {
			frame.SetRGBA(x, y, color.RGBA{B: 255, A: 255})
		}
	}
	for y := 16; y < 22; y++ {
		for x := innerX - 1; x <= innerX+1; x++ {
			frame.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	return frame
}

func dualColorBarSource(builtins nodes.Builtins, slot string) []byte {
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	control, _ := builtins.Definition(nodes.ControlDualColorBarNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-dual-color-bar-control","name":"Dual Color Bar Control"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"control","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},"bindings":{
				"inner-range":{"kind":"value","value":{"space":"rgb","minimum":[220,0,0],"maximum":[255,80,80]}},
				"outer-range":{"kind":"value","value":{"space":"rgb","minimum":[0,0,220],"maximum":[80,80,255]}},
				"region":{"kind":"value","value":{"x":0.319,"y":0.0638,"width":0.367,"height":0.0115,"unit":"ratio"}},
				"inner-minimum-width":{"kind":"default"},"inner-maximum-width":{"kind":"default"},"outer-minimum-width":{"kind":"default"},
				"band-height-ratio":{"kind":"default"},"band-inner-height-ratio":{"kind":"default"},"inner-confidence-weight":{"kind":"default"},"outer-confidence-weight":{"kind":"default"},
				"tolerance-ratio":{"kind":"default"},"minimum-tolerance":{"kind":"default"},"left-keys":{"kind":"default"},"right-keys":{"kind":"default"},
				"hold-duration":{"kind":"default"},"neutral-duration":{"kind":"default"},"cycle-duration":{"kind":"default"},"maximum-iterations":{"kind":"value","value":5},
				"activation-keys":{"kind":"default"},"activation-hold-duration":{"kind":"default"},"appearance-poll-duration":{"kind":"value","value":0},
				"activation-retry-duration":{"kind":"default"},"appearance-timeout":{"kind":"default"}
			}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"control","portId":"in"}}],"inputs":[],"outputs":[]}],
		"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		control.Contract.NodeRef().NodeTypeID, control.Contract.NodeRef().SemanticDigest, slot))
}
