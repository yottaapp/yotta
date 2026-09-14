package authoringcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/yottaapp/yotta/internal/apperr"
	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/automation/target"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
	"image"
	"image/png"
	"testing"
)

type testTargets struct {
	slot string
	fail bool
}

func (targets *testTargets) ResolveTarget(_ context.Context, slot string) (target.Target, error) {
	targets.slot = slot
	return target.Target{Kind: "desktop-window", DisplayName: "Example", Resolution: target.Size{W: 2400, H: 1200}}, nil
}
func (targets *testTargets) CapturePNG(_ context.Context, slot string) ([]byte, error) {
	targets.slot = slot
	if targets.fail {
		return nil, errors.New("private path and credential must not escape")
	}
	var raw bytes.Buffer
	_ = png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 2400, 1200)))
	return raw.Bytes(), nil
}
func TestCapturePreservesCoordinateMappingAndReturnsImage(t *testing.T) {
	targets := &testTargets{}
	service := &Service{Targets: targets}
	capture, err := service.Capture(context.Background(), CaptureRequest{Slot: "game"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(capture.Data))
	if err != nil || targets.slot != "game" || capture.Info.SourceWidth != 2400 || capture.Info.Width != 1920 || decoded.Bounds().Dy() != 960 || capture.Info.CoordinateSpace != "target" {
		t.Fatalf("capture=%+v err=%v", capture.Info, err)
	}
	targets.fail = true
	_, err = service.Capture(context.Background(), CaptureRequest{Slot: "game"})
	if apperr.From(err).ID != "authoring.observation.capture_failed" {
		t.Fatal(err)
	}
}
func TestScreenOriginAndCancellation(t *testing.T) {
	service := &Service{Screen: func(context.Context) (image.Image, image.Point, error) {
		return image.NewRGBA(image.Rect(0, 0, 640, 480)), image.Pt(-640, -100), nil
	}}
	capture, err := service.Capture(context.Background(), CaptureRequest{Screen: true})
	if err != nil || capture.Info.OriginX != -640 || capture.Info.OriginY != -100 || capture.Info.CoordinateSpace != "screen" {
		t.Fatalf("%+v %v", capture.Info, err)
	}
	_, err = service.Capture(context.Background(), CaptureRequest{Screen: true, Slot: "game"})
	if apperr.From(err).ID != "authoring.observation.invalid_capture" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = service.Capture(ctx, CaptureRequest{Screen: true}); err == nil {
		t.Fatal("cancelled capture succeeded")
	}
}

type observationApplication struct {
	source   workflowstore.SourceSnapshot
	values   map[string]json.RawMessage
	reads    int
	mismatch bool
}

func (a *observationApplication) GetSource(id string) (workflowstore.SourceSnapshot, error) {
	if id != a.source.WorkflowID() {
		return workflowstore.SourceSnapshot{}, errors.New("missing")
	}
	return a.source, nil
}
func (a *observationApplication) GetParameters(string) (appcore.ParameterConfiguration, error) {
	a.reads++
	values := map[string]json.RawMessage{}
	for key, value := range a.values {
		values[key] = append(json.RawMessage(nil), value...)
	}
	revision := a.source.Revision()
	if a.mismatch {
		revision++
	}
	return appcore.ParameterConfiguration{Revision: revision, Values: values}, nil
}
func observationSource(t *testing.T, legacy bool) workflowstore.SourceSnapshot {
	t.Helper()
	roots, err := storage.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := catalog.Open(context.Background(), roots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foundation.Close() })
	store, err := workflowstore.OpenSourceStore(foundation.Workflows(), workflowstore.SourceStoreOptions{MaxSources: 4})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"format":"yotta.workflow","version":"5","workflow":{"id":"observed","name":"Observed"},"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`)
	var source schema.WorkflowSource
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if !legacy {
		source.Targets = []schema.WorkflowTarget{{ID: "game-role", Name: "Game role", Kind: "automation", Default: true}, {ID: "unbound", Name: "Other", Kind: "automation"}}
		source.TargetDefaults = []schema.TargetDefault{{Target: "target", Slot: "game-role"}}
	} else {
		source.TargetDefaults = []schema.TargetDefault{{Target: "target", Slot: "machine"}}
	}
	raw, err = json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Save(context.Background(), raw, -1)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func TestObservationUsesLocalBindingButReportsPortableRole(t *testing.T) {
	app := &observationApplication{source: observationSource(t, false), values: map[string]json.RawMessage{"@target/game-role": json.RawMessage(`"machine"`)}}
	targets := &testTargets{}
	service := &Service{Application: app, Targets: targets, ListTargets: func() []TargetInfo {
		return []TargetInfo{{Slot: "machine", Label: "Private window", Kind: "desktop-window", Adapter: "win32"}, {Slot: "other-machine", Label: "Unrelated", Kind: "browser-cdp"}}
	}}
	inspected, err := service.Inspect("observed")
	if err != nil || len(inspected.Targets) != 2 || inspected.Targets[0] != (TargetInfo{Slot: "game-role", Label: "Game role", Kind: "desktop-window", Adapter: "win32"}) || inspected.Targets[1].Slot != "unbound" || service.Editor().WorkflowID != "" {
		t.Fatalf("inspect=%+v err=%v", inspected, err)
	}
	for _, slot := range []string{"", "game-role"} {
		before := app.reads
		captured, err := service.Capture(context.Background(), CaptureRequest{WorkflowID: "observed", Slot: slot})
		if err != nil || captured.Info.Slot != "game-role" || targets.slot != "machine" || app.reads != before+1 {
			t.Fatalf("capture=%+v err=%v reads=%d", captured.Info, err, app.reads-before)
		}
	}
	described, err := service.DescribeWorkflow(context.Background(), "observed", "game-role")
	if err != nil || described.Slot != "game-role" || described.Name != "Game role" || targets.slot != "machine" {
		t.Fatalf("describe=%+v %v", described, err)
	}
	service.SetEditor(Editor{WorkflowID: "observed"})
	described, err = service.Describe(context.Background(), "game-role")
	if err != nil || described.Slot != "game-role" || targets.slot != "machine" {
		t.Fatal(described, err)
	}
	targets.slot = "untouched"
	for _, slot := range []string{"unbound", "machine"} {
		_, err := service.Capture(context.Background(), CaptureRequest{Slot: slot})
		if apperr.From(err).ID != "authoring.observation.target_unavailable" || targets.slot != "untouched" {
			t.Fatalf("unbound=%v slot=%s", err, targets.slot)
		}
	}
	app.values = map[string]json.RawMessage{}
	_, err = service.Capture(context.Background(), CaptureRequest{})
	if apperr.From(err).ID != "authoring.observation.default_target_missing" {
		t.Fatal(err)
	}
	app.mismatch = true
	_, err = service.Inspect("observed")
	if apperr.From(err).ID != "authoring.observation.save_first" {
		t.Fatal(err)
	}
}
func TestLegacyObservationKeepsMachineSlots(t *testing.T) {
	app := &observationApplication{source: observationSource(t, true)}
	targets := &testTargets{}
	service := &Service{Application: app, Targets: targets, ListTargets: func() []TargetInfo { return []TargetInfo{{Slot: "machine"}} }}
	inspected, err := service.Inspect("observed")
	if err != nil || len(inspected.Targets) != 1 || inspected.Targets[0].Slot != "machine" {
		t.Fatal(inspected, err)
	}
	captured, err := service.Capture(context.Background(), CaptureRequest{WorkflowID: "observed"})
	if err != nil || captured.Info.Slot != "machine" || targets.slot != "machine" || app.reads != 0 {
		t.Fatal(captured.Info, err)
	}
}
