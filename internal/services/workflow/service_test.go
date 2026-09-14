package workflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/ai"
	"github.com/yottaapp/yotta/internal/appbootstrap"
	"github.com/yottaapp/yotta/internal/appcontrol"
	"github.com/yottaapp/yotta/internal/apperr"
	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/httpegress"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/registryclient"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/services/workflow"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	publicbundle "github.com/yottaapp/yotta/pkg/workflowbundle"
)

func TestServiceQueriesOneThousandSourcesWithBoundedPages(t *testing.T) {
	runtime := workflowRuntime(t, time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC), 1_000)
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	service, err := workflow.NewService(runtime.Application)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1_000; index++ {
		name := fmt.Sprintf("Workflow %04d", index)
		if index == 100 || index == 900 {
			name += " Needle"
		}
		request := workflow.CreateSourceRequest{
			Name: name, Description: fmt.Sprintf("Fixture workflow %04d", index),
			Category: []string{"Even", "Odd"}[index%2], Tags: []string{"common"},
		}
		if index == 100 || index == 900 {
			request.Tags = append(request.Tags, "needle")
		}
		if _, err := service.CreateSourceWithMetadata(request); err != nil {
			t.Fatalf("CreateSource(%d): %v", index, err)
		}
	}

	started := time.Now()
	page, err := service.QuerySources(workflow.SourceQuery{Sort: "name_asc", Page: 10, PageSize: 100})
	if err != nil || page.Total != 1_000 || len(page.Items) != 100 || page.Items[0].Name != "Workflow 0900 Needle" {
		t.Fatalf("QuerySources page = %#v, %v", page, err)
	}
	search, err := service.QuerySources(workflow.SourceQuery{Search: "needle", Sort: "name_asc", Page: 1, PageSize: 20})
	if err != nil || search.Total != 2 || len(search.Items) != 2 {
		t.Fatalf("QuerySources search = %#v, %v", search, err)
	}
	filtered, err := service.QuerySources(workflow.SourceQuery{
		Category: "even", Tags: []string{"NEEDLE"}, Sort: "nodes_desc", Page: 1, PageSize: 20,
	})
	if err != nil || filtered.Total != 2 || len(filtered.Items) != 2 || filtered.Items[0].NodeCount != 1 {
		t.Fatalf("QuerySources facets = %#v, %v", filtered, err)
	}
	recent, err := service.QuerySources(workflow.SourceQuery{
		CreatedSince: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		UpdatedSince: time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Sort:         "updated_desc", Page: 1, PageSize: 20,
	})
	if err != nil || recent.Total != 1_000 || recent.Items[0].CreatedAt == "" || recent.Items[0].UpdatedAt == "" {
		t.Fatalf("QuerySources date filters = %#v, %v", recent, err)
	}
	future, err := service.QuerySources(workflow.SourceQuery{
		CreatedSince: time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		Sort:         "created_desc", Page: 1, PageSize: 20,
	})
	if err != nil || future.Total != 0 {
		t.Fatalf("QuerySources future date filter = %#v, %v", future, err)
	}
	if _, err := service.QuerySources(workflow.SourceQuery{CreatedSince: "invalid", Page: 1}); err == nil {
		t.Fatal("QuerySources accepted an invalid date filter")
	}
	if len(filtered.Categories) != 2 || filtered.Categories[0].Value != "Even" || filtered.Categories[0].Count != 500 ||
		len(filtered.Tags) != 2 || filtered.Tags[0].Value != "common" || filtered.Tags[0].Count != 1_000 {
		t.Fatalf("QuerySources facet values = categories %#v, tags %#v", filtered.Categories, filtered.Tags)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("querying 1000 workflow sources took %s", elapsed)
	}
}

func TestServiceActiveRunQueryValidationAndEmptyState(t *testing.T) {
	runtime := workflowRuntime(t, time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC), 1)
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = runtime.Close(ctx)
	})
	service, err := workflow.NewService(runtime.Application)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetActiveSourceRuns(make([]string, 101)); err == nil {
		t.Fatal("oversized query succeeded")
	}
	if _, err := service.GetActiveSourceRuns([]string{""}); err == nil {
		t.Fatal("empty workflow ID succeeded")
	}
	got, err := service.GetActiveSourceRuns([]string{"missing", "missing"})
	if err != nil || len(got) != 0 {
		t.Fatalf("empty active runs = %#v, %v", got, err)
	}
	if recoveries := service.ListSourceRecoveries(); recoveries == nil {
		t.Fatal("recoveries must be an empty slice")
	}
	if _, err := service.StartDebugRun("missing", nil); err == nil {
		t.Fatal("debugging a missing workflow succeeded")
	}
	if _, err := service.GetDebugSnapshot("missing"); err == nil {
		t.Fatal("missing debug snapshot succeeded")
	}
	if _, err := service.ControlDebugRun("missing", "pause"); err == nil {
		t.Fatal("controlling a missing debug Run succeeded")
	}
	if _, err := service.SetDebugBreakpoints("missing", nil); err == nil {
		t.Fatal("setting breakpoints on a missing Run succeeded")
	}
	if _, err := service.CancelRun("missing"); err == nil {
		t.Fatal("cancelling a missing Run succeeded")
	}
	if _, err := service.GetRunTimelinePage("missing", 1, 10); err == nil {
		t.Fatal("missing timeline page succeeded")
	}
}

func TestServiceProjectsProductionWorkflowLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 17, 3, 0, 0, 0, time.UTC)
	runtime := workflowRuntime(t, now)
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	service, err := workflow.NewService(runtime.Application)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSource(" Projection ")
	if err != nil || created.Name != "Projection" || created.SourceJSON == "" ||
		created.CreatedAt != now.Format(time.RFC3339Nano) || created.UpdatedAt != created.CreatedAt {
		t.Fatalf("CreateSource() = %#v, %v", created, err)
	}
	metadata, err := service.UpdateSourceMetadata(created.WorkflowID, created.Revision, workflow.UpdateSourceMetadataRequest{
		Name: "Projection", Description: "Service projection", Category: "Tests", Tags: []string{"workflow", "Workflow"},
	})
	if err != nil || metadata.Description != "Service projection" || metadata.Category != "Tests" || len(metadata.Tags) != 1 || metadata.Revision != 1 {
		t.Fatalf("UpdateSourceMetadata() = %#v, %v", metadata, err)
	}
	batch := service.BatchUpdateSourceMetadata([]workflow.BatchUpdateSourceMetadataRequest{
		{WorkflowID: created.WorkflowID, BaseRevision: metadata.Revision, Category: "Batch", Tags: []string{"reviewed"}},
	})
	if len(batch) != 1 || !batch[0].Updated || batch[0].Problem != nil {
		t.Fatalf("BatchUpdateSourceMetadata() = %#v", batch)
	}
	created, err = service.GetSource(created.WorkflowID)
	if err != nil || created.Name != "Projection" || created.Description != "Service projection" || created.Category != "Batch" || len(created.Tags) != 1 || created.Tags[0] != "reviewed" {
		t.Fatalf("batch metadata source = %#v, %v", created, err)
	}
	loaded, err := service.GetSource(created.WorkflowID)
	if err != nil || loaded.SourceHash != created.SourceHash || loaded.SourceJSON == "" {
		t.Fatalf("GetSource() = %#v, %v", loaded, err)
	}
	patched, err := service.ApplyPatch(created.WorkflowID, created.Revision, []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "concat", Position: schema.Position{}}},
		{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$concat", PortID: "a", Value: "a"}},
		{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$concat", PortID: "b", Value: "b"}},
	})
	if err != nil || len(patched.GeneratedNodes) != 1 || patched.Source.Revision != 3 || patched.Source.NodeCount != 2 {
		t.Fatalf("ApplyPatch() = %#v, %v", patched, err)
	}
	listed, err := service.ListSources()
	if err != nil || len(listed) != 1 || listed[0].SourceJSON != "" || listed[0].SourceHash != patched.Source.SourceHash {
		t.Fatalf("ListSources() = %#v, %v", listed, err)
	}
	compiled, err := service.CompileSource(created.WorkflowID)
	if err != nil || !compiled.ProgramHash.Valid() || len(compiled.Diagnostics) != 0 {
		t.Fatalf("CompileSource() = %#v, %v", compiled, err)
	}
	current, err := service.GetSource(created.WorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := service.CheckDraft(current.SourceJSON)
	if err != nil || checked.SourceHash != compiled.SourceHash || checked.ProgramHash != compiled.ProgramHash {
		t.Fatalf("CheckDraft() = %#v, %v", checked, err)
	}
	preview, err := service.PreviewRun(created.WorkflowID)
	if err != nil || preview.ProgramHash != compiled.ProgramHash {
		t.Fatalf("PreviewRun() = %#v, %v", preview, err)
	}
	if !strings.Contains(service.GetCatalog(), `"version":"1"`) || !strings.Contains(service.GetAuthoringProjection(), `"format":"yotta.node-authoring-projection"`) {
		t.Fatal("service projections omitted their format identity")
	}
	started, err := service.StartRun(created.WorkflowID)
	if err != nil || started.Run == nil || started.Run.Status != string(run.StatusQueued) {
		t.Fatalf("StartRun() = %#v, %v", started, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := service.GetRunTimeline(started.Run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status == string(run.StatusSucceeded) {
			if len(view.Timeline) != 4 || view.Failure != nil {
				t.Fatalf("terminal Run = %#v", view)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run remained %s", view.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	timelinePath := filepath.Join(t.TempDir(), "run-timeline.json")
	exported, err := service.ExportRunTimeline(started.Run.RunID, timelinePath)
	if err != nil || exported.Path != timelinePath || exported.Entries != 4 {
		t.Fatalf("ExportRunTimeline() = %#v, %v", exported, err)
	}
	raw, err := os.ReadFile(timelinePath)
	if err != nil {
		t.Fatal(err)
	}
	var timelineExport struct {
		Format  string `json:"format"`
		Version string `json:"version"`
		Run     struct {
			Timeline      []workflow.TimelineEntry `json:"timeline"`
			TimelineTotal int                      `json:"timelineTotal"`
		} `json:"run"`
	}
	if err := json.Unmarshal(raw, &timelineExport); err != nil {
		t.Fatal(err)
	}
	if timelineExport.Format != "yotta.run-timeline" || timelineExport.Version != "1" ||
		len(timelineExport.Run.Timeline) != 4 || timelineExport.Run.TimelineTotal != 4 {
		t.Fatalf("timeline export = %#v", timelineExport)
	}
	if err := service.CancelAllRuns(); err != nil {
		t.Fatal(err)
	}
}

func TestServicePersistsCompilesAndPhysicallyDeletesSubgraphLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)
	runtime := workflowRuntime(t, now)
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	service, err := workflow.NewService(runtime.Application)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSource("Subgraph lifecycle")
	if err != nil {
		t.Fatal(err)
	}

	authored, err := service.ApplyPatch(created.WorkflowID, created.Revision, []authoring.Command{
		{Kind: authoring.CommandAddGraph, AddGraph: &authoring.AddGraphCommand{Graph: schema.Graph{
			ID: "child", Name: "Wait", Kind: schema.GraphKindSubgraph,
			Nodes: []schema.Node{}, Calls: []schema.GraphCall{}, Edges: []schema.Edge{},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
			Entries: []schema.Endpoint{}, Exits: []schema.GraphExit{}, Annotations: []schema.Annotation{},
		}}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "child", NodeTypeID: nodes.DelayNodeID, Handle: "wait",
			Position: schema.Position{X: 40, Y: 60},
		}},
		{Kind: authoring.CommandBindDefault, BindDefault: &authoring.PortCommand{
			GraphID: "child", NodeID: "$wait", PortID: "duration-milliseconds",
		}},
		{Kind: authoring.CommandUpdateGraphInterface, UpdateGraphInterface: &authoring.GraphInterfaceCommand{
			GraphID: "child",
			Entries: []schema.Endpoint{{NodeID: "$wait", PortID: "in"}},
			Exits: []schema.GraphExit{
				{ID: "done", Name: "完成", Channel: schema.EdgeExec, Endpoint: schema.Endpoint{NodeID: "$wait", PortID: "done"}},
				{ID: "failed", Name: "失败", Channel: schema.EdgeError, Endpoint: schema.Endpoint{NodeID: "$wait", PortID: "failed"}},
			},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
		}},
		{Kind: authoring.CommandAddGraphCall, AddGraphCall: &authoring.GraphCallCommand{
			GraphID: "main",
			Call: schema.GraphCall{
				ID: "call-a", GraphID: "child", Position: schema.Position{X: 160, Y: 80},
				Bindings: map[string]schema.InputBinding{},
			},
		}},
		{Kind: authoring.CommandAddGraphCall, AddGraphCall: &authoring.GraphCallCommand{
			GraphID: "main",
			Call: schema.GraphCall{
				ID: "call-b", GraphID: "child", Position: schema.Position{X: 360, Y: 80},
				Bindings: map[string]schema.InputBinding{},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened := parseServiceSource(t, service, created.WorkflowID)
	if len(reopened.Graphs) != 2 || len(reopened.Graphs[0].Calls) != 2 ||
		len(reopened.Graphs[1].Entries) != 1 || len(reopened.Graphs[1].Exits) != 2 {
		t.Fatalf("reopened authored source = %#v", reopened.Graphs)
	}
	if compiled, err := service.CompileSource(created.WorkflowID); err != nil || schema.HasErrors(compiled.Diagnostics) {
		t.Fatalf("compile authored source = %#v, %v", compiled.Diagnostics, err)
	}

	expanded, err := service.ApplyPatch(created.WorkflowID, authored.Source.Revision, []authoring.Command{
		{Kind: authoring.CommandRemoveGraphCall, RemoveGraphCall: &authoring.CallCommand{
			GraphID: "main", CallID: "call-a",
		}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.DelayNodeID, Handle: "expanded",
			Position: schema.Position{X: 160, Y: 80},
		}},
		{Kind: authoring.CommandBindDefault, BindDefault: &authoring.PortCommand{
			GraphID: "main", NodeID: "$expanded", PortID: "duration-milliseconds",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened = parseServiceSource(t, service, created.WorkflowID)
	if len(reopened.Graphs) != 2 || len(reopened.Graphs[0].Calls) != 1 ||
		reopened.Graphs[0].Calls[0].ID != "call-b" || len(reopened.Graphs[0].Nodes) != 2 {
		t.Fatalf("reopened expanded source = %#v", reopened.Graphs)
	}
	if compiled, err := service.CompileSource(created.WorkflowID); err != nil || schema.HasErrors(compiled.Diagnostics) {
		t.Fatalf("compile expanded source = %#v, %v", compiled.Diagnostics, err)
	}

	if _, err := service.ApplyPatch(created.WorkflowID, expanded.Source.Revision, []authoring.Command{
		{Kind: authoring.CommandRemoveGraphCall, RemoveGraphCall: &authoring.CallCommand{
			GraphID: "main", CallID: "call-b",
		}},
		{Kind: authoring.CommandRemoveGraph, RemoveGraph: &authoring.GraphCommand{GraphID: "child"}},
	}); err != nil {
		t.Fatal(err)
	}
	reopened = parseServiceSource(t, service, created.WorkflowID)
	if len(reopened.Graphs) != 1 || reopened.Graphs[0].ID != "main" ||
		len(reopened.Graphs[0].Calls) != 0 || len(reopened.Graphs[0].Nodes) != 2 {
		t.Fatalf("reopened cascade-deleted source = %#v", reopened.Graphs)
	}
	if compiled, err := service.CompileSource(created.WorkflowID); err != nil || schema.HasErrors(compiled.Diagnostics) {
		t.Fatalf("compile cascade-deleted source = %#v, %v", compiled.Diagnostics, err)
	}
}

func TestServicePersistsAndCompilesEveryRunStateType(t *testing.T) {
	now := time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)
	runtime := workflowRuntime(t, now)
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	service, err := workflow.NewService(runtime.Application)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSource("Run state matrix")
	if err != nil {
		t.Fatal(err)
	}
	projection, err := nodeauthoring.Project(nodeauthoring.Input{
		Catalog: runtime.Builtins.Catalog, Types: runtime.Builtins.Types,
		Capabilities: runtime.Builtins.Capabilities, Contracts: runtime.Builtins.Contracts,
		GeneratorVersion: nodes.GeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]authoring.Command, 0, len(runtime.Builtins.Types)+1)
	for _, definition := range runtime.Builtins.Types {
		projected, ok := projection.Type(definition.TypeRef().TypeID)
		if !ok || len(projected.StateInitial) == 0 {
			continue
		}
		var initial any
		decoder := json.NewDecoder(bytes.NewReader(projected.StateInitial))
		decoder.UseNumber()
		if err := decoder.Decode(&initial); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, authoring.Command{
			Kind: authoring.CommandAddStateVariable,
			AddStateVariable: &authoring.AddStateVariableCommand{
				Name: fmt.Sprintf("state-%02d", len(commands)),
				Type: datatype.RefExpression(definition.TypeRef()), Default: initial,
			},
		})
	}
	keyCode := runtime.Builtins.KeyCodeType.TypeRef()
	commands = append(commands, authoring.Command{
		Kind: authoring.CommandAddStateVariable,
		AddStateVariable: &authoring.AddStateVariableCommand{
			Name:    "shortcut",
			Type:    datatype.ListExpression(datatype.RefExpression(keyCode)),
			Default: []any{"CTRL", "K"},
		},
	})
	if _, err := service.ApplyPatch(created.WorkflowID, created.Revision, commands); err != nil {
		t.Fatal(err)
	}
	reopened := parseServiceSource(t, service, created.WorkflowID)
	if len(reopened.Variables) != len(commands) {
		t.Fatalf("reopened state variables = %d, want %d", len(reopened.Variables), len(commands))
	}
	if compiled, err := service.CompileSource(created.WorkflowID); err != nil || schema.HasErrors(compiled.Diagnostics) {
		t.Fatalf("compile state matrix = %#v, %v", compiled.Diagnostics, err)
	}
}

func parseServiceSource(t *testing.T, service *workflow.Service, workflowID string) schema.WorkflowSource {
	t.Helper()
	view, err := service.GetSource(workflowID)
	if err != nil {
		t.Fatal(err)
	}
	source, diagnostics := schema.ParseSource([]byte(view.SourceJSON))
	if schema.HasErrors(diagnostics) {
		t.Fatalf("reopened source diagnostics = %#v", diagnostics)
	}
	return source
}

func TestServiceExposesWorkflowSourcePortabilityWithoutMachineInstallations(t *testing.T) {
	runtime := workflowRuntime(t, time.Date(2026, 7, 17, 3, 30, 0, 0, time.UTC))
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	service, err := workflow.NewService(runtime.Application, workflow.WithBundleManager(runtime.Bundles))
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSource("Portable")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), created.WorkflowID+".yotta-workflow")
	exported, err := service.ExportSourceBundle(created.WorkflowID, archivePath)
	if err != nil || !exported.Exported || exported.Path != archivePath {
		t.Fatalf("ExportSourceBundle() = %#v, %v", exported, err)
	}
	info, err := service.InspectSourceBundle(archivePath)
	if err != nil || info.WorkflowID != created.WorkflowID || info.SourceHash != created.SourceHash {
		t.Fatalf("InspectSourceBundle() = %#v, %v", info, err)
	}
	imported, err := service.ImportSourceBundle(archivePath)
	if err != nil || imported.WorkflowID == created.WorkflowID || imported.Name != created.Name || imported.Revision != 0 {
		t.Fatalf("ImportSourceBundle() = %#v, %v", imported, err)
	}
	replaced, err := service.ReplaceSourceFromBundle(archivePath, imported.WorkflowID, imported.Revision, imported.SourceHash)
	if err != nil || replaced.WorkflowID != imported.WorkflowID || replaced.Revision != 1 {
		t.Fatalf("ReplaceSourceFromBundle() = %#v, %v", replaced, err)
	}
	batchDirectory := t.TempDir()
	results := service.ExportSourceBundles([]string{created.WorkflowID, imported.WorkflowID}, batchDirectory)
	if len(results) != 2 || !results[0].Exported || !results[1].Exported {
		t.Fatalf("ExportSourceBundles() = %#v", results)
	}
	repeated := service.ExportSourceBundles([]string{created.WorkflowID}, batchDirectory)
	if len(repeated) != 1 || repeated[0].Exported || repeated[0].Problem == nil || repeated[0].Problem.ID != "workflow.bundle.destination_exists" {
		t.Fatalf("repeated ExportSourceBundles() = %#v", repeated)
	}
}

func TestServicePublishesCanonicalBundleAndSearchesRegistry(t *testing.T) {
	runtime := workflowRuntime(t, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	registry := &registryClientFake{}
	service, err := workflow.NewService(runtime.Application,
		workflow.WithBundleManager(runtime.Bundles), workflow.WithRegistryClient(registry))
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSource("整理照片")
	if err != nil {
		t.Fatal(err)
	}
	release, err := service.PublishSourceToRegistry(context.Background(), workflow.PublishRegistryRequest{
		WorkflowID: created.WorkflowID, ReleaseVersion: "1.0.0", Title: "整理照片", Summary: "自动整理照片。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if release.ReleaseID != "release-1" || registry.published.WorkflowID != created.WorkflowID {
		t.Fatalf("release = %#v, inspected = %#v", release, registry.published)
	}
	page, err := service.SearchRegistry(context.Background(), "照片", 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "整理照片" {
		t.Fatalf("SearchRegistry = %#v, %v", page, err)
	}
	installed, err := service.InstallRegistryWorkflow(context.Background(), "release-1")
	if err != nil || installed.WorkflowID != created.WorkflowID || installed.Name != created.Name {
		t.Fatalf("InstallRegistryWorkflow = %#v, %v", installed, err)
	}
	consumer := workflowRuntime(t, time.Now())
	if err := consumer.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = consumer.Close(context.Background()) })
	statePath := filepath.Join(t.TempDir(), "installations.json")
	consumerService, err := workflow.NewService(consumer.Application, workflow.WithBundleManager(consumer.Bundles), workflow.WithRegistryClient(registry), workflow.WithRegistryState(statePath))
	if err != nil {
		t.Fatal(err)
	}
	first, err := consumerService.InstallRegistryWorkflow(context.Background(), "release-1")
	if err != nil || first.WorkflowID != created.WorkflowID {
		t.Fatalf("first install = %#v, %v", first, err)
	}
	if _, err := service.UpdateSourceMetadata(created.WorkflowID, created.Revision, workflow.UpdateSourceMetadataRequest{Name: "Updated workflow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PublishSourceToRegistry(context.Background(), workflow.PublishRegistryRequest{WorkflowID: created.WorkflowID, ReleaseVersion: "2.0.0", Title: "Updated", Summary: "Updated"}); err != nil {
		t.Fatal(err)
	}
	updated, err := consumerService.InstallRegistryWorkflow(context.Background(), "release-2")
	if err != nil || updated.WorkflowID != first.WorkflowID || updated.Name != "Updated workflow" {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	registry.releaseVersion = "0.9.0"
	lower, err := consumerService.InstallRegistryWorkflow(context.Background(), "release-old")
	if err != nil || lower.Revision != updated.Revision {
		t.Fatalf("downgrade changed source: %#v,%v", lower, err)
	}
	installedRecords, err := consumerService.RegistryInstallations()
	if err != nil || len(installedRecords) != 1 || installedRecords[0].ReleaseVersion != "2.0.0" {
		t.Fatalf("downgrade changed tracking: %#v,%v", installedRecords, err)
	}
	registry.releaseVersion = "2.0.0"
	// A fresh service must recover the installation record, so a repeat is an open.
	reopened, err := workflow.NewService(consumer.Application, workflow.WithBundleManager(consumer.Bundles), workflow.WithRegistryClient(registry), workflow.WithRegistryState(statePath))
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.InstallRegistryWorkflow(context.Background(), "release-2")
	if err != nil || again.Revision != updated.Revision {
		t.Fatalf("repeat install = %#v, %v", again, err)
	}
	if _, err := reopened.UpdateSourceMetadata(updated.WorkflowID, updated.Revision, workflow.UpdateSourceMetadataRequest{Name: "Local edits"}); err != nil {
		t.Fatal(err)
	}
	registry.releaseVersion = "3.0.0"
	if _, err := reopened.InstallRegistryWorkflow(context.Background(), "release-3"); apperr.From(err).ID != "workflow.registry.local_changes" {
		t.Fatalf("modified update = %v", err)
	}
	cloned, err := reopened.CloneSource(context.Background(), updated.WorkflowID)
	if err != nil || cloned.WorkflowID == updated.WorkflowID || cloned.Name != "Local edits" {
		t.Fatalf("clone = %#v, %v", cloned, err)
	}
}

func TestServiceMapsRegistryVersionConflictToStableProblem(t *testing.T) {
	runtime := workflowRuntime(t, time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	registry := &registryClientFake{err: registryclient.Problem{Code: "registry.release_version_conflict", Status: 409}}
	service, err := workflow.NewService(runtime.Application,
		workflow.WithBundleManager(runtime.Bundles), workflow.WithRegistryClient(registry))
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSource("冲突")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.PublishSourceToRegistry(context.Background(), workflow.PublishRegistryRequest{
		WorkflowID: created.WorkflowID, ReleaseVersion: "1.0.0", Title: "冲突", Summary: "版本冲突。",
	})
	if envelope := apperr.From(err); envelope.ID != "workflow.registry.release_version_conflict" || envelope.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

type registryClientFake struct {
	releaseVersion string
	published      publicbundle.Info
	bundle         []byte
	err            error
}

func (client *registryClientFake) PublishWorkflow(ctx context.Context, request registryclient.PublishRequest) (registryclient.WorkflowRelease, error) {
	raw, err := io.ReadAll(request.Bundle)
	if err != nil {
		return registryclient.WorkflowRelease{}, err
	}
	client.published, err = publicbundle.Inspect(ctx, raw)
	if err != nil {
		return registryclient.WorkflowRelease{}, err
	}
	if client.err != nil {
		return registryclient.WorkflowRelease{}, client.err
	}
	client.bundle = append([]byte(nil), raw...)
	client.releaseVersion = request.ReleaseVersion
	return registryclient.WorkflowRelease{
		ReleaseID: "release-1", WorkflowID: client.published.WorkflowID,
		ReleaseVersion: request.ReleaseVersion, Title: request.Title, Summary: request.Summary,
		Creator: registryclient.Creator{UserKey: "TestA123"}, Availability: "active",
	}, nil
}

func (client *registryClientFake) Search(context.Context, string, int) (registryclient.SearchPage, error) {
	return registryclient.SearchPage{Items: []registryclient.SearchResult{{
		Kind: "workflow", Workflow: registryclient.WorkflowRelease{ReleaseID: "release-1", Title: "整理照片"},
	}}}, client.err
}

func (client *registryClientFake) GetWorkflowRelease(context.Context, string) (registryclient.WorkflowRelease, error) {
	return registryclient.WorkflowRelease{ReleaseID: "release-1", BundleDigest: "sha256:" + strings.Repeat("1", 64)}, client.err
}

func (client *registryClientFake) CreateInstallPlan(context.Context, string, registryclient.Environment) (registryclient.InstallPlan, error) {
	return registryclient.InstallPlan{ResolvedWorkflowRelease: registryclient.WorkflowRelease{
		ReleaseID: "release-1", BundleDigest: "sha256:" + strings.Repeat("1", 64),
		WorkflowID: client.published.WorkflowID, SourceHash: string(client.published.SourceHash), ReleaseVersion: client.releaseVersion,
	}}, client.err
}

func (client *registryClientFake) DownloadArtifact(context.Context, string) ([]byte, error) {
	return append([]byte(nil), client.bundle...), client.err
}

func TestServiceRejectsMissingOrUntrustedApplication(t *testing.T) {
	if _, err := workflow.NewService(nil); err == nil {
		t.Fatal("NewService accepted nil Application")
	}
}

func TestServiceQueriesAndDeletesSourcesWithCASAndReferenceBlocking(t *testing.T) {
	runtime := workflowRuntime(t, time.Date(2026, 7, 17, 4, 0, 0, 0, time.UTC))
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	service, err := workflow.NewService(runtime.Application, workflow.WithReferenceResolver(func(workflowID string) []workflow.SourceReference {
		if strings.HasSuffix(workflowID, "blocked") {
			return []workflow.SourceReference{{Kind: "schedule", ID: "nightly", Label: "Nightly"}}
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := service.CreateSource("Alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := service.CreateSource("Beta")
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.QuerySources(workflow.SourceQuery{Search: "a", Sort: "name_desc", Page: 1, PageSize: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].Name != "Beta" {
		t.Fatalf("QuerySources = %#v, %v", page, err)
	}

	blockedID := beta.WorkflowID + "-blocked"
	previews, err := service.PreviewDeleteSources([]string{alpha.WorkflowID, blockedID})
	if err == nil || len(previews) != 0 {
		// Preview is strict: a missing Source aborts rather than presenting stale confirmation data.
		t.Fatalf("PreviewDeleteSources missing source = %#v, %v", previews, err)
	}
	serviceWithReference, err := workflow.NewService(runtime.Application, workflow.WithReferenceResolver(func(workflowID string) []workflow.SourceReference {
		if workflowID == beta.WorkflowID {
			return []workflow.SourceReference{{Kind: "schedule", ID: "nightly", Label: "Nightly"}}
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	previews, err = serviceWithReference.PreviewDeleteSources([]string{alpha.WorkflowID, beta.WorkflowID})
	if err != nil || len(previews) != 2 || len(previews[0].References) != 0 || len(previews[1].References) != 1 {
		t.Fatalf("PreviewDeleteSources = %#v, %v", previews, err)
	}
	results := serviceWithReference.DeleteSources([]workflow.DeleteSourceRequest{
		{WorkflowID: alpha.WorkflowID, Revision: alpha.Revision, SourceHash: alpha.SourceHash},
		{WorkflowID: beta.WorkflowID, Revision: beta.Revision, SourceHash: beta.SourceHash},
	})
	if len(results) != 2 || !results[0].Deleted || results[1].Deleted || len(results[1].References) != 1 {
		t.Fatalf("DeleteSources = %#v", results)
	}
	if _, err := service.GetSource(alpha.WorkflowID); err == nil {
		t.Fatal("deleted Source still loads")
	}
	if _, err := service.GetSource(beta.WorkflowID); err != nil {
		t.Fatalf("referenced Source was deleted: %v", err)
	}
}

func workflowRuntime(t *testing.T, now time.Time, maxSources ...int) *appbootstrap.Runtime {
	return workflowRuntimeConfigured(t, now, nil, maxSources...)
}

func workflowRuntimeConfigured(t *testing.T, now time.Time, configure func(*appbootstrap.Config), maxSources ...int) *appbootstrap.Runtime {
	t.Helper()
	sourceLimit := 8
	if len(maxSources) != 0 {
		sourceLimit = maxSources[0]
	}
	aiInstallations, err := ai.Install(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpInstallations, err := httpegress.Install(nil)
	if err != nil {
		t.Fatal(err)
	}
	applicationInstallations, err := appcontrol.Install(nil)
	if err != nil {
		t.Fatal(err)
	}
	automationInstallations, err := automationinstalled.Install(nil)
	if err != nil {
		t.Fatal(err)
	}
	roots, err := storage.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := catalog.Open(context.Background(), roots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foundation.Close() })
	blobStore, err := blob.Open(roots.Objects, blob.Limits{MaxBlobBytes: 1 << 20, MaxTotalBytes: 8 << 20}, foundation.Objects())
	if err != nil {
		t.Fatal(err)
	}
	scriptRuntime, err := scriptengine.NewRuntime(scriptengine.RuntimeOptions{
		Executable:         filepath.Join(t.TempDir(), scriptengine.WorkerExecutableName),
		ProcessMemoryBytes: scriptengine.DefaultMemoryBytes,
		JobMemoryBytes:     scriptengine.DefaultMemoryBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := appbootstrap.Config{
		DataRoot: roots.Data, ProgramCacheRoot: filepath.Join(roots.Cache, "programs"),
		WorkflowRepository: foundation.Workflows(),
		RunRepository:      foundation.Runs(),
		BlobStore:          blobStore,
		Limits: appbootstrap.Limits{
			MaxSources: sourceLimit, MaxPrograms: 8, MaxRuns: 8, MaxResourcePayloadBytes: 2 << 20,
			MaxProgramCacheBytes: 8 << 20,
			BlobChunkBytes:       64 << 10, BlobQueueCapacity: 2, StreamCapacity: 4, StreamChunkBytes: 64 << 10,
		},
		AIInstallations: aiInstallations, HTTPInstallations: httpInstallations,
		ApplicationInstallations: applicationInstallations, AutomationInstallations: automationInstallations,
		ScriptRuntime: scriptRuntime, LogEmitter: noderuntime.LogEmitterFunc(func(context.Context, noderuntime.LogEntry) error { return nil }),
		OwnerCloseTimeout: time.Second, Now: func() time.Time { return now },
	}
	if configure != nil {
		configure(&config)
	}
	runtime, err := appbootstrap.Build(config)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}
