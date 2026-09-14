package targetruntime_test

import (
	"context"
	"github.com/yottaapp/yotta/internal/targetruntime"
	"slices"
	"testing"
)

func TestBindingAliasesFreezeAndNeverFallThrough(t *testing.T) {
	provider := &testProvider{}
	source, err := targetruntime.NewSnapshot([]targetruntime.Installation{
		{Slot: "device", TargetID: "target/editor", Provider: provider, Configuration: targetruntime.Configuration{Kind: "desktop-window", Arguments: []string{"original"}}},
		{Slot: "unbound", TargetID: "target/editor", Provider: provider},
	})
	if err != nil {
		t.Fatal(err)
	}
	roles := []string{"first", "second", "unbound"}
	bindings := map[string]string{"first": "device", "second": "device"}
	bound, err := source.Bind(roles, bindings)
	if err != nil {
		t.Fatal(err)
	}
	bindings["first"] = "deleted"
	roles[0] = "changed"
	metadata := bound.Configuration("first")
	metadata.Arguments[0] = "changed"
	if source.Configuration("first").Kind != "" || bound.Configuration("first").Arguments[0] != "original" || slices.Contains(bound.Slots(), "unbound") || !slices.Contains(source.Slots(), "unbound") {
		t.Fatal("binding mutated or fell through")
	}
	runtime, err := bound.NewRun()
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	for _, role := range []string{"first", "second"} {
		if _, err := runtime.Open(context.Background(), targetruntime.OpenRequest{Slot: role, Kind: "automation/input-session", Operations: []string{"click"}}); err != nil {
			t.Fatal(err)
		}
	}
	if provider.opened != 2 {
		t.Fatal("multiple roles did not open independently")
	}
	if _, err := source.Bind([]string{"first"}, map[string]string{"first": "missing"}); err == nil {
		t.Fatal("missing device accepted")
	}
	// Resolve from original installations even when another role shadows its slot.
	collision, err := source.Bind([]string{"device", "first"}, map[string]string{"first": "device"})
	if err != nil || collision.Configuration("first").Kind != "desktop-window" || slices.Contains(collision.Slots(), "device") {
		t.Fatalf("collision=%v %v", collision.Slots(), err)
	}
}
