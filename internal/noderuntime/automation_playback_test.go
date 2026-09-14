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
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/services/inputclip"
	"github.com/yottaapp/yotta/internal/services/macro"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

type automationPlaybackProvider struct {
	events   []automationinstalled.PlaybackEvent
	releases int
	closed   int
}

func (provider *automationPlaybackProvider) Open(_ context.Context, request resource.ProviderOpenRequest) (any, error) {
	if request.Kind != automationinstalled.KindPlayback || fmt.Sprint(request.Operations) != "[play-event release-held]" || string(request.Config) != `{}` || len(request.CapabilityScope) != 0 {
		return nil, fmt.Errorf("unexpected automation playback open request: %#v", request)
	}
	return struct{}{}, nil
}

func (provider *automationPlaybackProvider) Invoke(_ context.Context, _ any, operation string, payload []byte) ([]byte, error) {
	switch operation {
	case automationinstalled.OperationPlayEvent:
		var event automationinstalled.PlaybackEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		provider.events = append(provider.events, event)
	case automationinstalled.OperationReleaseHeld:
		if string(payload) != `{}` {
			return nil, errors.New("unexpected release payload")
		}
		provider.releases++
	default:
		return nil, errors.New("unexpected playback operation")
	}
	return []byte(`{}`), nil
}

func (provider *automationPlaybackProvider) Close(context.Context, any) error {
	provider.closed++
	return nil
}

func TestPlayInputClipReadsNominalBlobAndUsesExclusivePlaybackSession(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	clip := &inputclip.InputClip{
		Meta: inputclip.ClipMeta{RecordingMode: inputclip.RecordingModePrecise, MouseMode: "mixed", BaseResolution: [2]int{1920, 1080}, MouseCounts360: 400},
		Events: []inputclip.Event{
			{TUs: 0, Seq: 0, Type: inputclip.EventTypeKeyDown, A: 0x41},
			{TUs: 1000, Seq: 0, Type: inputclip.EventTypeRawDelta, B: 3, C: -2},
			{TUs: 1000, Seq: 1, Type: inputclip.EventTypeKeyUp, A: 0x41},
		},
	}
	clip.UpdateDuration()
	var carrier bytes.Buffer
	if err := inputclip.Encode(&carrier, clip); err != nil {
		t.Fatal(err)
	}
	store, err := blob.Open(t.TempDir(), blob.Limits{MaxBlobBytes: 1 << 20, MaxTotalBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), inputclip.MediaType, bytes.NewReader(carrier.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	blobProvider, err := blob.NewProvider(store, blob.ProviderLimits{MaxChunkBytes: 64 << 10, QueueCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	playbackProvider := &automationPlaybackProvider{}
	const targetID, slot = "automation-target/playback-test", "playback-test"
	program := compilePrimitiveProgram(t, builtins, automationPlaybackSource(builtins, slot, ref))
	now := time.Date(2026, 7, 16, 22, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{
		blob.ProviderID: {ArtifactDigest: blobProviderDigest(t), ABI: blob.ProviderABI, Provider: blobProvider},
	}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	targets := configuredTargetRun(t, slot, targetID, playbackProvider)
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	_, err = compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).
		RunWithTargets(context.Background(), program, owner, targets, journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(playbackProvider.events) != 3 || playbackProvider.events[1].Kind != automationinstalled.PlaybackMoveRelative ||
		playbackProvider.events[1].DeltaX != 3 || playbackProvider.events[1].DeltaY != -2 || playbackProvider.events[1].SourceCounts360 != 400 {
		t.Fatalf("playback events = %#v", playbackProvider.events)
	}
	if playbackProvider.releases != 1 || playbackProvider.closed != 1 {
		t.Fatalf("releases=%d closed=%d", playbackProvider.releases, playbackProvider.closed)
	}
	actions := 0
	for _, entry := range journal.Current().Journal() {
		if entry.Kind == run.JournalAdapterAction {
			actions++
			if entry.Action != "automation.play-input-clip" || entry.ActionOutcome != run.ActionSucceeded ||
				entry.Summary.Counters["events"] != 3 || entry.Summary.Counters["blob_bytes"] != ref.Size || entry.Summary.Counters["duration_ms"] != 1 {
				t.Fatalf("playback action = %#v", entry)
			}
		}
	}
	if actions != 1 {
		t.Fatalf("actions=%d", actions)
	}
}

func TestPlayMacroPreservesOverlappingKeysAndReleasesSession(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	document := macro.Document{SchemaVersion: macro.SchemaVersion, BaseResolution: [2]int{1280, 720}, Meta: macro.DefaultMeta(), Actions: []macro.Action{
		{ID: "w-down", Kind: macro.ActionKeyDown, Key: "W"},
		{ID: "wait-1", Kind: macro.ActionSleep, DurationUs: 50_000},
		{ID: "d-down", Kind: macro.ActionKeyDown, Key: "D"},
		{ID: "wait-2", Kind: macro.ActionSleep, DurationUs: 450_000},
		{ID: "w-up", Kind: macro.ActionKeyUp, Key: "W"},
		{ID: "wait-3", Kind: macro.ActionSleep, DurationUs: 100_000},
		{ID: "d-up", Kind: macro.ActionKeyUp, Key: "D"},
	}}
	var carrier bytes.Buffer
	if err := macro.Encode(&carrier, document); err != nil {
		t.Fatal(err)
	}
	store, err := blob.Open(t.TempDir(), blob.Limits{MaxBlobBytes: 1 << 20, MaxTotalBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), macro.MediaType, bytes.NewReader(carrier.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	blobProvider, err := blob.NewProvider(store, blob.ProviderLimits{MaxChunkBytes: 64 << 10, QueueCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	playbackProvider := &automationPlaybackProvider{}
	const targetID, slot = "automation-target/macro-test", "macro-test"
	program := compilePrimitiveProgram(t, builtins, automationMacroSource(builtins, slot, ref))
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{
		blob.ProviderID: {ArtifactDigest: blobProviderDigest(t), ABI: blob.ProviderABI, Provider: blobProvider},
	}, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	targets := configuredTargetRun(t, slot, targetID, playbackProvider)
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	_, err = compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).
		RunWithTargets(context.Background(), program, owner, targets, journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(playbackProvider.events) != 4 {
		t.Fatalf("playback events = %#v", playbackProvider.events)
	}
	want := []struct {
		kind string
		key  uint32
	}{{automationinstalled.PlaybackKeyDown, 'W'}, {automationinstalled.PlaybackKeyDown, 'D'}, {automationinstalled.PlaybackKeyUp, 'W'}, {automationinstalled.PlaybackKeyUp, 'D'}}
	for index, expected := range want {
		if playbackProvider.events[index].Kind != expected.kind || playbackProvider.events[index].KeyCode != expected.key {
			t.Fatalf("event %d = %#v", index, playbackProvider.events[index])
		}
	}
	if playbackProvider.releases != 1 || playbackProvider.closed != 1 {
		t.Fatalf("releases=%d closed=%d", playbackProvider.releases, playbackProvider.closed)
	}
	for _, entry := range journal.Current().Journal() {
		if entry.Kind == run.JournalAdapterAction && entry.Action == "automation.play-macro" {
			if entry.ActionOutcome != run.ActionSucceeded || entry.Summary.Counters["actions"] != 7 || entry.Summary.Counters["duration_ms"] != 600 {
				t.Fatalf("macro action = %#v", entry)
			}
			return
		}
	}
	t.Fatal("macro adapter action was not journaled")
}

func automationPlaybackSource(builtins nodes.Builtins, slot string, ref blob.BlobRef) []byte {
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	playback, _ := builtins.Definition(nodes.PlayInputClipNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-automation-playback","name":"Automation Playback"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"playback","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},
			 "bindings":{"clip":{"kind":"blob","blob":{"mediaType":%q,"digest":%q,"size":%d}}}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"playback","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().Version, started.Contract.NodeRef().SemanticDigest,
		playback.Contract.NodeRef().NodeTypeID, playback.Contract.NodeRef().Version, playback.Contract.NodeRef().SemanticDigest,
		slot, ref.MediaType, ref.Digest, ref.Size))
}

func automationMacroSource(builtins nodes.Builtins, slot string, ref blob.BlobRef) []byte {
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	playback, _ := builtins.Definition(nodes.PlayMacroNodeID)
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-automation-macro","name":"Automation Macro"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"playback","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"slot":%q},
			 "bindings":{"macro":{"kind":"blob","blob":{"mediaType":%q,"digest":%q,"size":%d}}}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"playback","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().SemanticDigest,
		playback.Contract.NodeRef().NodeTypeID, playback.Contract.NodeRef().SemanticDigest, slot, ref.MediaType, ref.Digest, ref.Size))
}
