package application

import (
	"context"
	"encoding/json"
	"github.com/yottaapp/yotta/internal/resource"
	"github.com/yottaapp/yotta/internal/targetruntime"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"testing"
)

type validationTargetProvider struct{}

func (validationTargetProvider) Open(context.Context, resource.ProviderOpenRequest) (any, error) {
	panic("binding validation must not open a target")
}
func (validationTargetProvider) Invoke(context.Context, any, string, []byte) ([]byte, error) {
	panic("binding validation must not invoke a target")
}
func (validationTargetProvider) Close(context.Context, any) error { return nil }

func TestTargetBindingKindsAndUnusedRoles(t *testing.T) {
	var installations []targetruntime.Installation
	for _, kind := range []string{"desktop-window", "android-device", "browser-cdp", "configured-application", "http-target"} {
		installations = append(installations, targetruntime.Installation{Slot: kind, TargetID: kind, Provider: validationTargetProvider{}, Configuration: targetruntime.Configuration{Kind: kind}})
	}
	snapshot, err := targetruntime.NewSnapshot(installations)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ declared, actual, code string }{
		{"automation", "desktop-window", ""}, {"automation", "android-device", ""}, {"automation", "browser-cdp", ""},
		{"configured-application", "configured-application", ""}, {"desktop-window", "desktop-window", ""},
		{"automation", "http-target", CodeTargetKindMismatch}, {"automation", "configured-application", CodeTargetKindMismatch},
		{"desktop-window", "android-device", CodeTargetKindMismatch}, {"configured-application", "desktop-window", CodeTargetKindMismatch},
	} {
		t.Run(tc.declared+"/"+tc.actual, func(t *testing.T) {
			source := schema.WorkflowSource{Targets: []schema.WorkflowTarget{{ID: "role", Name: "Role", Kind: tc.declared, Default: true}}}
			raw, _ := json.Marshal(tc.actual)
			values := map[string]json.RawMessage{"@target/role": raw}
			bindings, ds := checkTargetBindings(source, values, snapshot, map[string]bool{"role": true})
			if tc.code == "" {
				if len(ds) != 0 || bindings["role"] != tc.actual {
					t.Fatalf("bindings=%v diagnostics=%v", bindings, ds)
				}
			} else {
				if len(ds) != 1 || ds[0].Code != tc.code || ds[0].Params["parameterId"] != "@target/role" || ds[0].Params["parameterLabel"] != "Role" || ds[0].Params["expectedKind"] != tc.declared || ds[0].Params["actualKind"] != tc.actual {
					t.Fatalf("diagnostics=%v", ds)
				}
				// A saved binding that became incompatible must not block unrelated Runs.
				if _, ds := checkTargetBindings(source, values, snapshot, map[string]bool{}); len(ds) != 0 {
					t.Fatalf("unused role blocked Run: %v", ds)
				}
			}
		})
	}
}
