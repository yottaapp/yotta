package noderuntime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/httpegress"
	"github.com/yottaapp/yotta/internal/nodeadapter"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/targetruntime"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/sdk/plugin/positionsource"
)

type navigationProvider struct {
	mu                   sync.Mutex
	held                 bool
	sampled              time.Time
	readyAt              time.Time
	opened               int
	x, heading           float64
	turns, steps, closed int
	positionSource       bool
	allowReturn          bool
	sequence             int64
	clock                *positionFixtureClock
}

// Coordinate timestamps advance at source observations, independently of slow
// race-instrumented durable journal writes. Stale-source cases explicitly
// supply an expired observation.
type positionFixtureClock struct{ milliseconds atomic.Int64 }

func newPositionFixtureClock() *positionFixtureClock {
	c := &positionFixtureClock{}
	c.milliseconds.Store(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli())
	return c
}
func (c *positionFixtureClock) Now() time.Time    { return time.UnixMilli(c.milliseconds.Load()) }
func (c *positionFixtureClock) Sample() time.Time { return time.UnixMilli(c.milliseconds.Add(100)) }

// Navigation deadlines measure simulated wait time, not the cost of committing
// the surrounding integration workflow to disk. Keep the scheduler's real waits
// and pause hooks so the independent position feed and action branches still run.
func navigationFixtureTimers(adapters map[string]nodeadapter.InstalledAdapter) {
	for _, key := range []string{"automation.move-character-to", "navigation.follow-path", "navigation.follow-saved-path"} {
		entry := adapters[key]
		original := entry.Run
		entry.Run = func(ctx context.Context, i nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
			var elapsed atomic.Int64
			epoch := time.Now()
			i.MonotonicNow = func() time.Time { return epoch.Add(time.Duration(elapsed.Load())) }
			wait, waitWithPause := i.Wait, i.WaitWithPause
			i.Wait = func(ctx context.Context, duration time.Duration) error {
				err := wait(ctx, duration)
				elapsed.Add(int64(max(duration, 0)))
				return err
			}
			if waitWithPause != nil {
				i.WaitWithPause = func(ctx context.Context, duration time.Duration, pause func(context.Context) error) error {
					err := waitWithPause(ctx, duration, pause)
					elapsed.Add(int64(max(duration, 0)))
					return err
				}
			}
			return original(ctx, i)
		}
		adapters[key] = entry
	}
}

func (p *navigationProvider) Open(_ context.Context, r resource.ProviderOpenRequest) (any, error) {
	if r.Kind == installed.KindHeldInput && !slices.Equal(r.Operations, installed.HeldInputOperations()) {
		return nil, fmt.Errorf("held input session requires exact operations: %v", r.Operations)
	}
	p.mu.Lock()
	p.opened++
	p.mu.Unlock()
	return r.Kind, nil
}
func (p *navigationProvider) Close(_ context.Context, object any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.allowReturn {
		p.advancePathFixture(time.Now())
	}
	p.closed++
	if object == installed.KindHeldInput {
		p.held = false
	}
	return nil
}
func (p *navigationProvider) Invoke(_ context.Context, _ any, op string, payload []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.allowReturn {
		p.advancePathFixture(time.Now())
	}
	switch op {
	case httpegress.OperationGet:
		now := time.Now()
		if now.Before(p.readyAt) {
			return artifact.Marshal(httpegress.GetResponse{StatusCode: 200, ContentType: "application/json", Body: `{"valid":false}`})
		}
		if p.held && !p.sampled.IsZero() && !p.allowReturn {
			direction := 1.0
			elapsed := now.Sub(p.sampled).Seconds()
			p.x += elapsed * 10 * direction
		}
		p.sampled = now
		sampleTime := now
		if p.clock != nil {
			sampleTime = p.clock.Sample()
		}
		if p.positionSource {
			p.sequence++
			observation := func(value any) positionsource.Observation {
				raw, _ := json.Marshal(value)
				return positionsource.Observation{Status: "tracking", Value: raw, SampleTimeMs: sampleTime.UnixMilli(), Sequence: p.sequence}
			}
			raw, err := json.Marshal(positionsource.Snapshot{Descriptor: positionsource.Descriptor{
				Protocol: positionsource.Protocol, Source: "synthetic.navigation",
				Capabilities: []positionsource.Capability{positionsource.Position, positionsource.CameraHeading},
				Frame:        positionsource.Frame{ID: "test/world", Unit: "test-raw", AxisHeading: 90, AxisSign: 1, Kind: "world", Recovery: "stable"}},
				Epoch: "test-session", Observations: map[positionsource.Capability]positionsource.Observation{
					positionsource.Position: observation(positionsource.Point{X: p.x}), positionsource.CameraHeading: observation(math.Mod(math.Mod(p.heading, 360)+360, 360)),
				}})
			if err != nil {
				return nil, err
			}
			return artifact.Marshal(httpegress.GetResponse{StatusCode: 200, ContentType: "application/json", Body: string(raw)})
		}
		return artifact.Marshal(httpegress.GetResponse{StatusCode: 200, ContentType: "application/json", Body: fmt.Sprintf(`{"valid":true,"x":%v,"y":0,"cameraHeading":%v,"sampleTimeMs":%d}`, p.x, p.heading, sampleTime.UnixMilli())})
	case installed.OperationTurnView:
		var req installed.TurnViewRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		p.heading += req.Degrees
		p.turns++
	case installed.OperationHoldKeys:
		if !p.allowReturn && math.Abs(p.heading-90) > 5 {
			return nil, fmt.Errorf("held forward before facing target: heading=%v x=%v", p.heading, p.x)
		}
		p.held = true
		p.sampled = time.Now()
		p.steps++
	case installed.OperationPressKeys:
		var req installed.PressKeysRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		if math.Abs(p.heading-90) > 5 || len(req.Keys) != 1 || req.Keys[0] != "W" {
			return nil, fmt.Errorf("walked before facing target: %v %+v", p.heading, req)
		}
		p.x += float64(req.DurationMilliseconds) * 0.01
		p.steps++
	default:
		return nil, fmt.Errorf("unexpected operation %s", op)
	}
	return []byte(`{}`), nil
}

// Settle motion before changing heading or releasing a held key. Otherwise a
// predictive stop between position polls discards the final movement entirely.
func (p *navigationProvider) advancePathFixture(now time.Time) {
	if p.held && !p.sampled.IsZero() {
		elapsed := min(now.Sub(p.sampled).Seconds(), 0.1)
		p.x += elapsed * 10 * math.Cos((p.heading-90)*math.Pi/180)
	}
	p.sampled = now
}

func navigationSource(t *testing.T, b nodes.Builtins, id string, config map[string]any, bindings map[string]any) []byte {
	t.Helper()
	start, _ := b.Definition(nodes.RunStartedNodeID)
	node, _ := b.Definition(id)
	ref := func(def nodes.BuiltinDefinition) map[string]any {
		r := def.Contract.NodeRef()
		return map[string]any{"nodeTypeId": r.NodeTypeID, "version": r.Version, "semanticDigest": r.SemanticDigest}
	}
	source := map[string]any{"format": "yotta.workflow", "version": "5", "workflow": map[string]any{"id": "navigation-test", "name": "Navigation test"}, "revision": 0, "entryGraph": "main",
		"graphs": []any{map[string]any{"id": "main", "kind": "main", "nodes": []any{
			map[string]any{"id": "start", "nodeRef": ref(start), "position": map[string]int{"x": 0, "y": 0}, "config": map[string]any{}, "bindings": map[string]any{}},
			map[string]any{"id": "navigation", "nodeRef": ref(node), "position": map[string]int{"x": 100, "y": 0}, "config": config, "bindings": bindings},
		}, "edges": []any{map[string]any{"channel": "exec", "from": map[string]string{"nodeId": "start", "portId": "started"}, "to": map[string]string{"nodeId": "navigation", "portId": "in"}}}, "inputs": []any{}, "outputs": []any{}}},
		"variables": []any{}, "resources": []any{}, "targetProfileDefinitions": []any{}, "credentialRequirements": []any{}, "dependencies": []any{},
	}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCharacterMoveConsumesIndependentPositionUpdates(t *testing.T) {
	testCharacterPositionFeed(t, 0, 60000, 10, false)
}

func TestCharacterMoveWaitsForColdPositionSource(t *testing.T) {
	testCharacterPositionFeed(t, 1500*time.Millisecond, 60000, 10, false)
}

func TestCharacterMoveAcceptsHundredMinuteTimeout(t *testing.T) {
	testCharacterPositionFeed(t, 0, 100*60*1000, 0, false)
}

func TestCharacterMoveConsumesPositionSourceProtocol(t *testing.T) {
	testCharacterPositionFeed(t, 0, 60000, 10, true)
}

func testCharacterPositionFeed(t *testing.T, startupDelay time.Duration, timeout int64, targetX float64, positionSource bool) {
	b, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	bindings := map[string]any{}
	for key, value := range map[string]any{"target-x": targetX, "target-y": 0, "tolerance": 0.6, "timeout": timeout, "interval": 100, "slow-distance": 3} {
		bindings[key] = map[string]any{"kind": "value", "value": value}
	}

	raw := navigationPositionFeed(t, b, navigationSource(t, b, nodes.MoveCharacterNodeID, map[string]any{"slot": "game", "position-variable": "position"}, bindings), map[string]any{"axisHeading": 90})
	program := compilePrimitiveProgram(t, b, raw)
	if slots := program.ConfiguredTargetSlots(b.Catalog); !slices.Equal(slots, []string{"game", "position"}) {
		t.Fatalf("resolved navigation target slots: %v", slots)
	}

	clock := newPositionFixtureClock()
	p := &navigationProvider{clock: clock, heading: 180, readyAt: time.Now().Add(startupDelay), positionSource: positionSource}
	snapshot, err := targetruntime.NewSnapshot([]targetruntime.Installation{{Slot: "game", TargetID: "test/game", Provider: p}, {Slot: "position", TargetID: "test/position", Provider: p}})
	if err != nil {
		t.Fatal(err)
	}
	targets, err := snapshot.NewRun()
	if err != nil {
		t.Fatal(err)
	}
	defer targets.Close(context.Background())
	now := time.Now().UTC()
	var store *run.Store
	_, owner, journal := admittedExecutionWithConsent(t, b, program, map[string]run.InstalledProvider{}, now, executionProfile(t, b), nil, func(value *run.Store) { store = value })
	defer owner.Close(context.Background())
	dependencies := testDependencies()
	dependencies.Now = clock.Now
	adapters, err := noderuntime.Installed(b, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	navigationFixtureTimers(adapters)
	result, err := compiler.NewExecutor(b.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }}).RunWithTargets(context.Background(), program, owner, targets, journal)
	if err != nil {
		t.Fatal(err)
	}
	var distance float64
	if err := json.Unmarshal(result.NodeOutputs["navigation"]["distance"].InlineJSON(), &distance); err != nil {
		t.Fatal(err)
	}
	movementInvalid := p.turns != 2 || p.steps == 0
	if targetX == 0 {
		movementInvalid = p.turns != 0 || p.steps != 0
	}
	if distance > 0.6 || movementInvalid || p.closed != p.opened || p.held {
		t.Fatalf("distance=%v provider=%+v", distance, p)
	}
	actions := 0
	timeline, err := store.TimelineSnapshot(context.Background(), journal.Current().Admission().RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range timeline.Entries {
		if entry.Kind == run.JournalAdapterAction && entry.NodeID == "navigation" {
			actions++
		}
	}
	if actions != 1 {
		t.Fatalf("adapter journal actions=%d", actions)
	}
}

func navigationPositionFeed(t *testing.T, b nodes.Builtins, input []byte, parseConfig map[string]any) []byte {
	t.Helper()
	var source map[string]any
	if err := json.Unmarshal(input, &source); err != nil {
		t.Fatal(err)
	}
	graph := source["graphs"].([]any)[0].(map[string]any)
	makeNode := func(id, kind string, config, bindings map[string]any) map[string]any {
		def, _ := b.Definition(kind)
		for _, port := range def.Contract.Machine().Ports.DataInputs {
			if _, ok := bindings[port.ID]; !ok && port.Default != nil {
				bindings[port.ID] = map[string]any{"kind": "default"}
			}
		}
		return map[string]any{"id": id, "nodeRef": def.Contract.NodeRef(), "position": map[string]int{"x": 0, "y": 0}, "config": config, "bindings": bindings}
	}
	graph["nodes"] = append(graph["nodes"].([]any),
		makeNode("monitor", nodes.MonitorNodeID, map[string]any{}, map[string]any{"interval-milliseconds": map[string]any{"kind": "value", "value": 20}}),
		makeNode("get", nodes.HTTPGetNodeID, map[string]any{"slot": "position"}, map[string]any{"path": map[string]any{"kind": "value", "value": "/v1/position"}}),
		makeNode("parse", nodes.ParseWorldPositionNodeID, parseConfig, map[string]any{}),
		makeNode("write", nodes.StateWriteNodeID, map[string]any{"variable": "position"}, map[string]any{}))
	edge := func(channel, from, out, to, in string) map[string]any {
		return map[string]any{"channel": channel, "from": map[string]string{"nodeId": from, "portId": out}, "to": map[string]string{"nodeId": to, "portId": in}}
	}
	graph["edges"] = []any{edge("exec", "start", "started", "monitor", "in"), edge("exec", "monitor", "main", "navigation", "in"), edge("exec", "monitor", "tick", "get", "in"), edge("exec", "get", "completed", "parse", "in"), edge("data", "get", "body", "parse", "source"), edge("exec", "parse", "done", "write", "in"), edge("data", "parse", "position", "write", "value")}
	source["variables"] = []any{map[string]any{"name": "position", "type": map[string]any{"kind": "ref", "ref": b.WorldPositionType.TypeRef()}, "default": b.WorldPositionType.Authoring().Examples[0]}}
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPathFixtureSettlesMovementBeforeRelease(t *testing.T) {
	p := &navigationProvider{held: true, heading: 90, allowReturn: true, sampled: time.Now().Add(-80 * time.Millisecond)}
	if err := p.Close(context.Background(), installed.KindHeldInput); err != nil {
		t.Fatal(err)
	}
	if p.x < 0.7 || p.x > 1.01 || p.held {
		t.Fatalf("release discarded movement: x=%v held=%v", p.x, p.held)
	}
	stopped := p.x
	time.Sleep(time.Millisecond)
	p.advancePathFixture(time.Now())
	if p.x != stopped {
		t.Fatal("movement continued after release")
	}
}
