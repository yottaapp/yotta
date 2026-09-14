package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	"github.com/yottaapp/yotta/internal/targetruntime"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
	"slices"
	"testing"
	"time"
)

type bindingProvider struct{}

func (bindingProvider) Open(_ context.Context, r resource.ProviderOpenRequest) (any, error) {
	return r.TargetID, nil
}
func (bindingProvider) Invoke(_ context.Context, object any, _ string, _ []byte) ([]byte, error) {
	return []byte(object.(string)), nil
}
func (bindingProvider) Close(context.Context, any) error { return nil }

func TestTargetBindingsParametersAndRunSnapshot(t *testing.T) {
	ctx := context.Background()
	snapshot, err := targetruntime.NewSnapshot([]targetruntime.Installation{
		{Slot: "local-one", TargetID: "one", Provider: bindingProvider{}, Configuration: targetruntime.Configuration{Kind: "configured-application"}},
		{Slot: "local-two", TargetID: "two", Provider: bindingProvider{}, Configuration: targetruntime.Configuration{Kind: "configured-application"}},
		{Slot: "window", TargetID: "window", Provider: bindingProvider{}, Configuration: targetruntime.Configuration{Kind: "desktop-window"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	app, sources, _, builtins, _, _ := newConfiguredTestApplication(t, time.Now(), nil, func(c *appcore.Config) {
		c.TargetSnapshot = func() (targetruntime.Snapshot, func(), error) { return snapshot, func() {}, nil }
	})
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer app.Close(ctx)
	created, err := app.CreateSource(ctx, "Target binding")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := app.ApplyPatch(ctx, authoring.PatchRequest{WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(), Commands: []authoring.Command{
		{Kind: authoring.CommandSetWorkflowTargets, SetWorkflowTargets: &authoring.SetWorkflowTargetsCommand{Targets: []schema.WorkflowTarget{
			{ID: "unused-default", Name: "Default", Kind: "automation", Default: true}, {ID: "launcher", Name: "Launcher", Kind: "configured-application"},
		}}},
		{Kind: authoring.CommandAddStateVariable, AddStateVariable: &authoring.AddStateVariableCommand{Name: "person", Type: datatype.RefExpression(builtins.StringType.TypeRef()), Default: "author", Parameter: &schema.Parameter{ID: "person", Label: "Person", Control: "auto"}}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.LaunchApplicationNodeID, Handle: "launch"}},
		{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "$launch", FieldID: "slot", Value: "launcher"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: authoring.PatchEdgeFromSource(schema.Edge{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "run-started", PortID: "started"}, To: schema.Endpoint{NodeID: "$launch", PortID: "in"}})}},
	}})
	if err != nil {
		t.Fatalf("patch=%+v %#v", saved, err)
	}
	id, revision := created.WorkflowID(), saved.Source.Revision()
	configuration, err := app.GetParameters(id)
	if err != nil || len(configuration.Targets) != 2 {
		t.Fatalf("get=%+v %v", configuration, err)
	}
	missing, err := app.StartRun(ctx, appcore.StartRunRequest{WorkflowID: id, Principal: "user-1"})
	if err != nil || len(missing.Diagnostics) != 1 || missing.Diagnostics[0].Code != appcore.CodeTargetBindingMissing || missing.Diagnostics[0].Params["targetId"] != "launcher" || missing.Record.Valid() {
		t.Fatalf("missing=%+v %v", missing, err)
	}
	diagnostics, err := app.SaveParameters(ctx, id, revision, map[string]json.RawMessage{"person": json.RawMessage(`"user"`), "@target/launcher": json.RawMessage(`"local-one"`)})
	if err != nil || schema.HasErrors(diagnostics) {
		t.Fatalf("save=%v %v", diagnostics, err)
	}
	var frozen []targetruntime.Snapshot
	app.SetRunServicePreparer(func(_ context.Context, slots []string, s targetruntime.Snapshot) ([]string, error) {
		if !slices.Contains(slots, "launcher") {
			t.Errorf("slots=%v", slots)
		}
		frozen = append(frozen, s)
		return nil, nil
	})
	first, err := app.StartDebugRun(ctx, appcore.StartRunRequest{WorkflowID: id, Principal: "user-1"}, nil)
	if err != nil || !first.Record.Valid() {
		t.Fatalf("first=%+v %v", first, err)
	}
	diagnostics, err = app.SaveTargetBindings(ctx, id, revision, map[string]string{"launcher": "local-two"})
	if err != nil || schema.HasErrors(diagnostics) {
		t.Fatalf("rebind=%v %v", diagnostics, err)
	}
	configuration, err = app.GetParameters(id)
	if err != nil || string(configuration.Values["person"]) != `"user"` {
		t.Fatalf("ordinary parameter overwritten=%+v %v", configuration, err)
	}
	second, err := app.StartRun(ctx, appcore.StartRunRequest{WorkflowID: id, Principal: "user-1"})
	if err != nil || !second.Record.Valid() || len(frozen) != 2 {
		t.Fatalf("second=%+v %v", second, err)
	}
	for index, expected := range []string{"one", "two"} {
		runtime, err := frozen[index].NewRun()
		if err != nil {
			t.Fatal(err)
		}
		handle, err := runtime.Open(ctx, targetruntime.OpenRequest{Slot: "launcher", Kind: "test", Operations: []string{"read"}})
		if err != nil {
			t.Fatal(err)
		}
		actual, err := runtime.Invoke(ctx, handle, "read", nil)
		if err != nil || string(actual) != expected {
			t.Fatalf("Run %d=%s %v", index, actual, err)
		}
		if err := runtime.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	current, err := sources.Load(id)
	if err != nil || !bytes.Equal(current.Artifact(), saved.Source.Artifact()) {
		t.Fatal("local bindings changed Source/defaults")
	}
	for _, tc := range []struct {
		name     string
		bindings map[string]string
		code     string
	}{
		{"missing", map[string]string{"launcher": "removed"}, appcore.CodeTargetBindingMissing},
		{"kind", map[string]string{"launcher": "window"}, appcore.CodeTargetKindMismatch},
		{"undeclared", map[string]string{"unlisted": "local-one"}, appcore.CodeTargetUndeclared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, e := app.SaveTargetBindings(ctx, id, revision, tc.bindings)
			if e != nil || len(d) != 1 || d[0].Code != tc.code {
				t.Fatalf("diagnostics=%v %v", d, e)
			}
		})
	}
	if _, err := app.SaveTargetBindings(ctx, id, revision-1, map[string]string{}); !errors.Is(err, workflowstore.ErrSourceConflict) {
		t.Fatalf("stale=%v", err)
	}
	configuration, _ = app.GetParameters(id)
	if string(configuration.Values["@target/launcher"]) != `"local-two"` {
		t.Fatal("failed save changed binding")
	}
	diagnostics, err = app.SaveTargetBindings(ctx, id, revision, map[string]string{})
	if err != nil || schema.HasErrors(diagnostics) {
		t.Fatal(diagnostics, err)
	}
	configuration, _ = app.GetParameters(id)
	if len(configuration.Values) != 1 || string(configuration.Values["person"]) != `"user"` {
		t.Fatal("clearing bindings changed ordinary parameters")
	}
	for _, raw := range []string{`42`, `true`, `{}`, `null`, ` null `} {
		d, e := app.SaveParameters(ctx, id, revision, map[string]json.RawMessage{"@target/launcher": json.RawMessage(raw)})
		if e != nil || len(d) != 1 || d[0].Code != appcore.CodeTargetBindingInvalid {
			t.Fatalf("invalid binding %s=%v %v", raw, d, e)
		}
	}
	source, ds := schema.ParseSource(saved.Source.Artifact())
	if schema.HasErrors(ds) {
		t.Fatal(ds)
	}
	var launchID string
	for _, node := range source.Graphs[0].Nodes {
		if node.NodeRef.NodeTypeID == nodes.LaunchApplicationNodeID {
			launchID = node.ID
		}
	}
	disconnected, e := app.ApplyPatch(ctx, authoring.PatchRequest{WorkflowID: id, BaseRevision: revision, Commands: []authoring.Command{{Kind: authoring.CommandDisconnect, Disconnect: &authoring.EdgeCommand{GraphID: "main", Edge: authoring.PatchEdgeFromSource(schema.Edge{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "run-started", PortID: "started"}, To: schema.Endpoint{NodeID: launchID, PortID: "in"}})}}}})
	if e != nil {
		t.Fatal(e)
	}
	app.SetRunServicePreparer(nil)
	unused, e := app.StartRun(ctx, appcore.StartRunRequest{WorkflowID: id, Principal: "user-1"})
	if e != nil || !unused.Record.Valid() || schema.HasErrors(unused.Diagnostics) {
		t.Fatalf("disconnected target blocked Run=%+v %v revision=%d", unused, e, disconnected.Source.Revision())
	}

}

func TestCopyParametersOnlyClonesMatchingLocalDeclarations(t *testing.T) {
	ctx := context.Background()
	store, err := workflowstore.OpenParameterStore("")
	if err != nil {
		t.Fatal(err)
	}
	app, sources, _, builtins, _, _ := newConfiguredTestApplication(t, time.Now(), nil, func(c *appcore.Config) { c.Parameters = store })
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer app.Close(ctx)
	makeSource := func(name string, extra bool) workflowstore.SourceSnapshot {
		created, err := app.CreateSource(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		commands := []authoring.Command{{Kind: authoring.CommandAddStateVariable, AddStateVariable: &authoring.AddStateVariableCommand{Name: "shared", Type: datatype.RefExpression(builtins.StringType.TypeRef()), Default: "author", Parameter: &schema.Parameter{ID: "shared", Label: "Shared", Control: "auto"}}}}
		if extra {
			commands = append(commands, authoring.Command{Kind: authoring.CommandAddStateVariable, AddStateVariable: &authoring.AddStateVariableCommand{Name: "sourceonly", Type: datatype.RefExpression(builtins.StringType.TypeRef()), Default: "extra", Parameter: &schema.Parameter{ID: "source-only", Label: "Extra", Control: "auto"}}})
		}
		saved, err := app.ApplyPatch(ctx, authoring.PatchRequest{WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(), Commands: commands})
		if err != nil {
			t.Fatal(err)
		}
		return saved.Source
	}
	from, to := makeSource("Original", true), makeSource("Clone", false)
	// Invalid values intentionally survive a local clone, for the user's repair.
	values := map[string]json.RawMessage{"shared": json.RawMessage(`42`), "source-only": json.RawMessage(`"not in clone"`), "removed": json.RawMessage(`true`), "@target/workflow-default": json.RawMessage(`"removed-device"`), "@target/ghost": json.RawMessage(`"old-device"`)}
	if err := store.Save(from.WorkflowID(), values); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(to.WorkflowID(), map[string]json.RawMessage{"old": json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	if err := app.CopyParameters(from.WorkflowID(), to.WorkflowID()); err != nil {
		t.Fatal(err)
	}
	copied, err := app.GetParameters(to.WorkflowID())
	if err != nil || len(copied.Values) != 2 || string(copied.Values["shared"]) != "42" || string(copied.Values["@target/workflow-default"]) != `"removed-device"` {
		t.Fatalf("copy=%+v %v", copied, err)
	}
	for _, original := range []workflowstore.SourceSnapshot{from, to} {
		current, err := sources.Load(original.WorkflowID())
		if err != nil || !bytes.Equal(current.Artifact(), original.Artifact()) {
			t.Fatal("copy modified Source")
		}
	}
	if err := app.CopyParameters(from.WorkflowID(), from.WorkflowID()); err == nil {
		t.Fatal("self copy accepted")
	}
	if err := app.CopyParameters(from.WorkflowID(), "missing"); err == nil {
		t.Fatal("missing destination accepted")
	}
	after, err := store.Load(from.WorkflowID())
	if err != nil || len(after) != len(values) {
		t.Fatal("copy mutated original local record")
	}
}

func TestRunInheritsGlobalTargetAndChecksNodeKind(t *testing.T) {
	for _, tc := range []struct{ name, declared, actual, node, code string }{
		{"application", "configured-application", "configured-application", nodes.LaunchApplicationNodeID, ""},
		{"automation", "automation", "desktop-window", nodes.MaximizeWindowNodeID, ""},
		{"node-kind", "automation", "browser-cdp", nodes.MaximizeWindowNodeID, appcore.CodeTargetKindMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			snapshot, err := targetruntime.NewSnapshot([]targetruntime.Installation{{Slot: "local", TargetID: "local", Provider: bindingProvider{}, Configuration: targetruntime.Configuration{Kind: tc.actual}}})
			if err != nil {
				t.Fatal(err)
			}
			app, _, _, _, _, _ := newConfiguredTestApplication(t, time.Now(), nil, func(c *appcore.Config) {
				c.TargetSnapshot = func() (targetruntime.Snapshot, func(), error) { return snapshot, func() {}, nil }
			})
			if err := app.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer app.Close(ctx)
			created, err := app.CreateSource(ctx, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := app.ApplyPatch(ctx, authoring.PatchRequest{WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(), Commands: []authoring.Command{
				{Kind: authoring.CommandSetWorkflowTargets, SetWorkflowTargets: &authoring.SetWorkflowTargetsCommand{Targets: []schema.WorkflowTarget{{ID: "workflow-default", Name: "Default", Kind: tc.declared, Default: true}}}},
				{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: tc.node, Handle: "operation"}},
				{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: authoring.PatchEdgeFromSource(schema.Edge{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "run-started", PortID: "started"}, To: schema.Endpoint{NodeID: "$operation", PortID: "in"}})}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			ds, err := app.SaveTargetBindings(ctx, created.WorkflowID(), saved.Source.Revision(), map[string]string{"workflow-default": "local"})
			if err != nil || schema.HasErrors(ds) {
				t.Fatalf("save=%v %v", ds, err)
			}
			result, err := app.StartDebugRun(ctx, appcore.StartRunRequest{WorkflowID: created.WorkflowID(), Principal: "user-1"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.code == "" {
				if !result.Record.Valid() || schema.HasErrors(result.Diagnostics) {
					t.Fatalf("inherit=%+v", result)
				}
			} else {
				if result.Record.Valid() || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != tc.code || result.Diagnostics[0].NodeID == "" || result.Diagnostics[0].Params["actualKind"] != tc.actual {
					t.Fatalf("mismatch=%+v", result)
				}
			}
		})
	}
}
