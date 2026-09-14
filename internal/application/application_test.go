package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/admission"
	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodeadapter"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/targetruntime"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

func TestApplicationRunsPersistedSourceThroughTheOnlyProgramWorker(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	application, sources, programs, _, events, _ := newTestApplication(t, now, nil)
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	saved := createConcatWorkflow(t, application)
	if saved.Source.Revision() != 1 {
		t.Fatalf("ApplyPatch = %#v", saved)
	}
	started, err := application.StartRun(context.Background(), appcore.StartRunRequest{WorkflowID: saved.Source.WorkflowID(), Principal: "user-1"})
	if err != nil || !started.Record.Valid() || !started.ProgramHash.Valid() || len(started.Diagnostics) != 0 {
		t.Fatalf("StartRun = %#v, %v", started, err)
	}
	if started.SourceHash != saved.Source.Hash() {
		t.Fatalf("compiled Source hash = %s, stored = %s", started.SourceHash, saved.Source.Hash())
	}
	if _, err := programs.Load(started.ProgramHash); err != nil {
		t.Fatalf("Program was not durable before admission: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.RunID == started.Record.Admission().RunID && event.Status == run.StatusSucceeded {
				loaded, err := application.GetRun(event.RunID)
				if err != nil || loaded.Status() != run.StatusSucceeded || len(loaded.Journal()) != 4 {
					t.Fatalf("terminal Run = %#v, journal=%d, %v", loaded, len(loaded.Journal()), err)
				}
				if listed := sources.List(); len(listed) != 1 || listed[0].Hash() != saved.Source.Hash() {
					t.Fatalf("Source list = %#v", listed)
				}
				return
			}
		case <-deadline:
			t.Fatal("Run did not reach succeeded")
		}
	}
}

func TestRunKeepsExactProviderGenerationLeaseAcrossHotReplacement(t *testing.T) {
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	entered, unblock := make(chan struct{}), make(chan struct{})
	var unblockOnce sync.Once
	releaseRun := func() { unblockOnce.Do(func() { close(unblock) }) }
	application, _, _, _, events, _ := newTestApplication(t, now, func(ctx context.Context, _ nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
		close(entered)
		select {
		case <-unblock:
			return nodeadapter.AdapterResult{}, errors.New("fixture completed")
		case <-ctx.Done():
			return nodeadapter.AdapterResult{}, ctx.Err()
		}
	})
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		releaseRun()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = application.Close(ctx)
	})
	profile, err := admission.SealHostProfile(admission.HostProfileDraft{OS: "windows", Architecture: "amd64", HostAPIGeneration: "1.0"})
	if err != nil {
		t.Fatal(err)
	}
	policy := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
		return admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "lease-test"}, nil
	})
	leased, released := make(chan struct{}), make(chan struct{})
	targets, err := targetruntime.NewSnapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := application.ReplaceExecutionEnvironment(profile, policy, map[string]run.InstalledProvider{}, func() (targetruntime.Snapshot, func(), error) {
		close(leased)
		var once sync.Once
		return targets, func() { once.Do(func() { close(released) }) }, nil
	}); err != nil {
		t.Fatal(err)
	}
	saved := createConcatWorkflow(t, application)
	started, err := application.StartRun(context.Background(), appcore.StartRunRequest{WorkflowID: saved.Source.WorkflowID(), Principal: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	<-leased
	<-entered
	if err := application.ReplaceExecutionEnvironment(profile, policy, map[string]run.InstalledProvider{}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
		t.Fatal("hot replacement released the provider generation of an active Run")
	default:
	}
	releaseRun()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.RunID == started.Record.Admission().RunID && event.Status == run.StatusFailed {
				select {
				case <-released:
					return
				case <-time.After(time.Second):
					t.Fatal("terminal Run did not release its provider generation")
				}
			}
		case <-deadline:
			t.Fatal("leased Run did not become terminal")
		}
	}
}

func TestApplicationCommandsRequireLiveLifecycle(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC)
	application, _, _, _, _, _ := newTestApplication(t, now, nil)
	if _, err := application.CreateSource(context.Background(), "before start"); err != appcore.ErrNotStarted {
		t.Fatalf("CreateSource before Start = %v", err)
	}
	if err := application.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := application.Start(context.Background()); err != appcore.ErrClosed {
		t.Fatalf("Start after Close = %v", err)
	}
}

func TestApplicationInventoriesWorkflowSourceBlobReferences(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 40, 0, 0, time.UTC)
	ref := blob.BlobRef{
		MediaType: "application/octet-stream",
		Digest:    artifact.Digest("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		Size:      42,
	}
	application, _, _, builtins, _, _ := newTestApplication(
		t, now, nil, blob.Object{Digest: ref.Digest, Size: ref.Size},
	)
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	created, err := application.CreateSource(context.Background(), "Blob inventory")
	if err != nil {
		t.Fatal(err)
	}
	patched, err := application.ApplyPatch(context.Background(), authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(), Commands: []authoring.Command{
			{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: builtins.BlobToStreamContract.NodeRef().NodeTypeID, Handle: "reader", Position: schema.Position{}}},
			{Kind: authoring.CommandBindBlob, BindBlob: &authoring.BindBlobCommand{GraphID: "main", NodeID: "$reader", PortID: "blob", Blob: ref}},
		},
	})
	if err != nil || !patched.Source.Valid() {
		t.Fatalf("ApplyPatch() = %#v, %v", patched, err)
	}
	var inventory []blob.BlobRef
	if err := application.WithDurableBlobReferences(context.Background(), func(refs []blob.BlobRef) error {
		inventory = append([]blob.BlobRef(nil), refs...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 1 || inventory[0] != ref {
		t.Fatalf("durable Blob inventory = %#v", inventory)
	}
}

func TestCreateSourceSeedsRunStartedRoot(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 45, 0, 0, time.UTC)
	application, _, _, _, _, _ := newTestApplication(t, now, nil)
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	if _, err := application.CreateSource(context.Background(), "  "); err == nil {
		t.Fatal("CreateSource accepted an empty workflow name")
	}

	created, err := application.CreateSource(context.Background(), "Runnable")
	if err != nil {
		t.Fatal(err)
	}
	var source schema.WorkflowSource
	if err := json.Unmarshal(created.Artifact(), &source); err != nil {
		t.Fatal(err)
	}
	if len(source.Graphs) != 1 || len(source.Graphs[0].Nodes) != 1 {
		t.Fatalf("created graph = %#v", source.Graphs)
	}
	root := source.Graphs[0].Nodes[0]
	if root.ID != "run-started" || root.NodeRef.NodeTypeID != nodes.RunStartedNodeID {
		t.Fatalf("created root = %#v", root)
	}
	loaded, err := application.GetSource(created.WorkflowID())
	if err != nil || loaded.Hash() != created.Hash() {
		t.Fatalf("GetSource = %#v, %v", loaded, err)
	}
	listed := application.ListSources()
	if len(listed) != 1 || listed[0].Hash() != created.Hash() {
		t.Fatalf("ListSources = %#v", listed)
	}
	if len(application.CatalogArtifact()) == 0 || !application.AuthoringProjection().Valid() {
		t.Fatal("application omitted its trusted authoring contracts")
	}
	compiled, err := application.CompileSource(context.Background(), created.WorkflowID())
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("CompileSource = %#v, %v", compiled, err)
	}
	if _, ok := compiled.Program(); !ok {
		t.Fatal("new workflow did not compile to a runnable Program")
	}
	draft, err := application.CompileDraft(context.Background(), created.Artifact())
	if err != nil || draft.SourceHash != created.Hash() {
		t.Fatalf("CompileDraft = %#v, %v", draft, err)
	}
	preview, err := application.PreviewRun(context.Background(), created.WorkflowID())
	if err != nil || !preview.ProgramHash.Valid() || len(preview.Diagnostics) != 0 {
		t.Fatalf("PreviewRun = %#v, %v", preview, err)
	}
	if err := application.CancelAll(context.Background()); err != nil {
		t.Fatalf("CancelAll = %v", err)
	}
}

func TestApplicationMigratesCompatibleNodeContractsBeforeCompile(t *testing.T) {
	now := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	application, sources, _, builtins, _, _ := newTestApplication(t, now, nil)
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	playback, _ := builtins.Definition(nodes.PlayInputClipNodeID)
	stalePlaybackRef := playback.Contract.NodeRef()
	stalePlaybackRef.Version = "1.0.0"
	stalePlaybackRef.SemanticDigest = "sha256:ff7ea9d0b2ca91cb2062cff30dd5ca8575555ec5363b4c76e746925ee6ae027b"
	source := schema.WorkflowSource{
		Format: schema.Format, Version: schema.Version,
		Workflow: schema.Workflow{ID: "playback-contract-retraction", Name: "Playback contract retraction"},
		Revision: 0, EntryGraph: "main",
		Graphs: []schema.Graph{{
			ID: "main", Kind: schema.GraphKindMain,
			Nodes: []schema.Node{
				{ID: "start", NodeRef: started.Contract.NodeRef(), Position: schema.Position{}, Config: map[string]any{}, Bindings: map[string]schema.InputBinding{}},
				{ID: "playback", NodeRef: stalePlaybackRef, Position: schema.Position{}, Config: map[string]any{"slot": "window-target"}, Bindings: map[string]schema.InputBinding{
					"clip": {Kind: schema.BindingDefault}, "turn-scale": {Kind: schema.BindingDefault},
				}},
			},
			Edges:  []schema.Edge{{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "start", PortID: "started"}, To: schema.Endpoint{NodeID: "playback", PortID: "in"}}},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
		}},
		Resources: []schema.WorkflowResource{}, TargetProfileDefinitions: []schema.TargetProfileDefinition{},
		CredentialRequirements: []schema.CredentialRequirement{}, Dependencies: []schema.NodePackageDependency{}, Variables: []schema.Variable{},
	}
	raw, err := artifact.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Save(context.Background(), raw, -1); err != nil {
		t.Fatal(err)
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = application.Close(ctx)
	})
	hasCode := func(diagnostics []schema.Diagnostic, code string) bool {
		for _, diagnostic := range diagnostics {
			if diagnostic.Code == code {
				return true
			}
		}
		return false
	}
	migrated, err := application.GetSource(source.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Revision() != 1 {
		t.Fatalf("migrated revision = %d, want 1", migrated.Revision())
	}
	migratedSource, diagnostics := schema.ParseSource(migrated.Artifact())
	if len(diagnostics) != 0 {
		t.Fatalf("migrated source diagnostics = %#v", diagnostics)
	}
	migratedPlayback := migratedSource.Graphs[0].Nodes[1]
	if migratedPlayback.NodeRef != playback.Contract.NodeRef() {
		t.Fatalf("migrated playback ref = %#v", migratedPlayback.NodeRef)
	}
	if _, ok := migratedPlayback.Bindings["turn-scale"]; ok {
		t.Fatal("obsolete turn-scale binding was retained")
	}
	after, err := application.CompileSource(context.Background(), source.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hasCode(after.Diagnostics, compiler.CodeNodeContractMismatch) || hasCode(after.Diagnostics, compiler.CodeUnknownPort) {
		t.Fatalf("migrated diagnostics = %#v", after.Diagnostics)
	}
}

func TestApplicationMigratesTemplateErrorCategoryContractsBeforeCompile(t *testing.T) {
	now := time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC)
	application, sources, _, builtins, _, _ := newTestApplication(t, now, nil)
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	waitTemplate, _ := builtins.Definition(nodes.WaitTemplateNodeID)
	clickTemplate, _ := builtins.Definition(nodes.ClickTemplateNodeID)
	waitTemplateGone, _ := builtins.Definition(nodes.WaitTemplateGoneNodeID)

	staleWaitRef := waitTemplate.Contract.NodeRef()
	staleWaitRef.Version = "1.0.0"
	staleWaitRef.SemanticDigest = "sha256:cd4f4177c886c36ba2ede3634b67b87c4d22597fa86e21269cae9bc38cc2066e"
	staleClickRef := clickTemplate.Contract.NodeRef()
	staleClickRef.Version = "1.0.0"
	staleClickRef.SemanticDigest = "sha256:370ee214c1f0e99d149a5f709019e74480f9054442e058ae6349054997f72c1d"
	staleWaitGoneRef := waitTemplateGone.Contract.NodeRef()
	staleWaitGoneRef.Version = "1.0.0"
	staleWaitGoneRef.SemanticDigest = "sha256:28eaccad35ee50f132dce3e2a45a85ad09d2c59d99c48dcdd42f9f7d3fb80253"

	source := schema.WorkflowSource{
		Format: schema.Format, Version: schema.Version,
		Workflow: schema.Workflow{ID: "template-error-category-contracts", Name: "Template error category contracts"},
		Revision: 0, EntryGraph: "main",
		Graphs: []schema.Graph{{
			ID: "main", Kind: schema.GraphKindMain,
			Nodes: []schema.Node{
				{ID: "start", NodeRef: started.Contract.NodeRef(), Position: schema.Position{}, Config: map[string]any{}, Bindings: map[string]schema.InputBinding{}},
				{ID: "wait", NodeRef: staleWaitRef, Position: schema.Position{}, Config: map[string]any{"slot": "window-target"}, Bindings: map[string]schema.InputBinding{}},
				{ID: "click", NodeRef: staleClickRef, Position: schema.Position{}, Config: map[string]any{"slot": "window-target"}, Bindings: map[string]schema.InputBinding{}},
				{ID: "wait-gone", NodeRef: staleWaitGoneRef, Position: schema.Position{}, Config: map[string]any{"slot": "window-target"}, Bindings: map[string]schema.InputBinding{}},
			},
			Edges: []schema.Edge{
				{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "start", PortID: "started"}, To: schema.Endpoint{NodeID: "wait", PortID: "in"}},
				{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "wait", PortID: "found"}, To: schema.Endpoint{NodeID: "click", PortID: "in"}},
				{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "click", PortID: "completed"}, To: schema.Endpoint{NodeID: "wait-gone", PortID: "in"}},
			},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
		}},
		Resources: []schema.WorkflowResource{}, TargetProfileDefinitions: []schema.TargetProfileDefinition{},
		CredentialRequirements: []schema.CredentialRequirement{}, Dependencies: []schema.NodePackageDependency{}, Variables: []schema.Variable{},
	}
	raw, err := artifact.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Save(context.Background(), raw, -1); err != nil {
		t.Fatal(err)
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	migrated, err := application.GetSource(source.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Revision() != 1 {
		t.Fatalf("migrated revision = %d, want 1", migrated.Revision())
	}
	migratedSource, parseDiagnostics := schema.ParseSource(migrated.Artifact())
	if len(parseDiagnostics) != 0 {
		t.Fatalf("migrated source diagnostics = %#v", parseDiagnostics)
	}
	if got := migratedSource.Graphs[0].Nodes[1].NodeRef; got != waitTemplate.Contract.NodeRef() {
		t.Fatalf("migrated wait-template ref = %#v", got)
	}
	if got := migratedSource.Graphs[0].Nodes[2].NodeRef; got != clickTemplate.Contract.NodeRef() {
		t.Fatalf("migrated click-template ref = %#v", got)
	}
	if got := migratedSource.Graphs[0].Nodes[3].NodeRef; got != waitTemplateGone.Contract.NodeRef() {
		t.Fatalf("migrated wait-template-gone ref = %#v", got)
	}
	if got := migratedSource.Graphs[0].Edges[1]; got.From.PortID != "found" || got.To.PortID != "in" {
		t.Fatalf("migration changed valid template edge = %#v", got)
	}

	compiled, err := application.CompileSource(context.Background(), source.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range compiled.Diagnostics {
		if diagnostic.Code == compiler.CodeNodeContractMismatch || diagnostic.Code == compiler.CodeUnknownPort {
			t.Fatalf("contract migration left derivative diagnostic = %#v", compiled.Diagnostics)
		}
	}
}

func TestApplicationMigratesIncompleteAIContractWithoutHidingItsMissingInputs(t *testing.T) {
	now := time.Date(2026, 7, 28, 15, 30, 0, 0, time.UTC)
	application, sources, _, builtins, _, _ := newTestApplication(t, now, nil)
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	extract, _ := builtins.Definition(nodes.AIExtractNodeID)
	staleExtractRef := extract.Contract.NodeRef()
	staleExtractRef.Version = "1.0.0"
	staleExtractRef.SemanticDigest = "sha256:dbcb528cb623272c3a7544c1a2ff6ed2e77c14dba1d795b6fea9511f87d99646"
	source := schema.WorkflowSource{
		Format: schema.Format, Version: schema.Version,
		Workflow: schema.Workflow{ID: "incomplete-ai-contract", Name: "Incomplete AI contract"},
		Revision: 0, EntryGraph: "main",
		Graphs: []schema.Graph{{
			ID: "main", Kind: schema.GraphKindMain,
			Nodes: []schema.Node{
				{ID: "start", NodeRef: started.Contract.NodeRef(), Position: schema.Position{}, Config: map[string]any{}, Bindings: map[string]schema.InputBinding{}},
				{
					ID: "extract", NodeRef: staleExtractRef, Position: schema.Position{},
					Config: map[string]any{"fields": []any{map[string]any{
						"name": "field1", "type": "string", "description": "", "nullable": false,
					}}},
					Bindings: map[string]schema.InputBinding{},
				},
			},
			Edges: []schema.Edge{{
				Channel: schema.EdgeExec,
				From:    schema.Endpoint{NodeID: "start", PortID: "started"},
				To:      schema.Endpoint{NodeID: "extract", PortID: "in"},
			}},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
		}},
		Resources: []schema.WorkflowResource{}, TargetProfileDefinitions: []schema.TargetProfileDefinition{},
		CredentialRequirements: []schema.CredentialRequirement{}, Dependencies: []schema.NodePackageDependency{}, Variables: []schema.Variable{},
	}
	raw, err := artifact.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sources.Save(context.Background(), raw, -1); err != nil {
		t.Fatal(err)
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })

	migrated, err := application.GetSource(source.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	migratedSource, diagnostics := schema.ParseSource(migrated.Artifact())
	if len(diagnostics) != 0 {
		t.Fatalf("migrated source diagnostics = %#v", diagnostics)
	}
	if got := migratedSource.Graphs[0].Nodes[1].NodeRef; got != extract.Contract.NodeRef() {
		t.Fatalf("migrated extract ref = %#v", got)
	}
	timeoutJSON, err := json.Marshal(migratedSource.Graphs[0].Nodes[1].Config["timeoutMilliseconds"])
	if err != nil || string(timeoutJSON) != "120000" {
		t.Fatalf("migrated AI timeout = %s, %v", timeoutJSON, err)
	}
	compiled, err := application.CompileSource(context.Background(), source.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range compiled.Diagnostics {
		if diagnostic.Code == compiler.CodeNodeContractMismatch || diagnostic.Code == compiler.CodeUnknownPort {
			t.Fatalf("contract migration left derivative diagnostic = %#v", compiled.Diagnostics)
		}
	}
}

func TestApplicationAtomicallyGuardsReferencedStateTypeMigration(t *testing.T) {
	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	application, sources, _, builtins, _, _ := newTestApplication(t, now, nil)
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	created, err := application.CreateSource(context.Background(), "State migration")
	if err != nil {
		t.Fatal(err)
	}
	edge := schema.Edge{
		Channel: schema.EdgeData,
		From:    schema.Endpoint{NodeID: "$read", PortID: "result"},
		To:      schema.Endpoint{NodeID: "$concat", PortID: "a"},
	}
	base, err := application.ApplyPatch(context.Background(), authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(), Commands: []authoring.Command{
			{Kind: authoring.CommandAddStateVariable, AddStateVariable: &authoring.AddStateVariableCommand{
				Name: "message", Type: datatype.RefExpression(builtins.StringType.TypeRef()), Default: "hello",
			}},
			{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.StateReadNodeID, Handle: "read"}},
			{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "$read", FieldID: "variable", Value: "message"}},
			{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "concat"}},
			{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$concat", PortID: "b", Value: " world"}},
			{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: authoring.PatchEdgeFromSource(edge)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	readID, concatID := base.GeneratedNodes[0].NodeID, base.GeneratedNodes[1].NodeID
	update := authoring.Command{Kind: authoring.CommandUpdateStateVariable, UpdateStateVariable: &authoring.UpdateStateVariableCommand{
		Name: "message", Type: datatype.RefExpression(builtins.IntegerType.TypeRef()), Default: 0,
	}}
	_, err = application.ApplyPatch(context.Background(), authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: base.Source.Revision(), Commands: []authoring.Command{update},
	})
	var migrationErr *appcore.UnsafeStateMigrationError
	if !errors.As(err, &migrationErr) || len(migrationErr.Diagnostics) == 0 {
		t.Fatalf("unsafe migration error = %#v, %v", migrationErr, err)
	}
	stored, err := sources.Load(created.WorkflowID())
	if err != nil || stored.Revision() != base.Source.Revision() {
		t.Fatalf("unsafe migration changed durable source: %#v, %v", stored, err)
	}
	prepared, err := application.PreparePatch(context.Background(), authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: base.Source.Revision(), Commands: []authoring.Command{update},
	})
	if err != nil || !prepared.Patch.Valid() {
		t.Fatalf("prepare unsafe migration = %#v, %v", prepared, err)
	}
	if _, err := application.CommitPreparedPatch(context.Background(), prepared.Patch); !errors.As(err, &migrationErr) {
		t.Fatalf("prepared unsafe migration commit = %v", err)
	}
	repairedEdge := edge
	repairedEdge.From.NodeID = readID
	repairedEdge.To.NodeID = concatID
	migrated, err := application.ApplyPatch(context.Background(), authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: base.Source.Revision(), Commands: []authoring.Command{
			{Kind: authoring.CommandDisconnect, Disconnect: &authoring.EdgeCommand{GraphID: "main", Edge: authoring.PatchEdgeFromSource(repairedEdge)}},
			{Kind: authoring.CommandRemoveNode, RemoveNode: &authoring.NodeCommand{GraphID: "main", NodeID: concatID}},
			update,
		},
	})
	if err != nil || migrated.Source.Revision() != base.Source.Revision()+1 {
		t.Fatalf("repaired migration = %#v, %v", migrated, err)
	}
}

func TestPreparedPatchCommitsTheExactReviewedArtifactAndRejectsStaleBase(t *testing.T) {
	now := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	application, sources, _, _, _, _ := newTestApplication(t, now, nil)
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	created, err := application.CreateSource(context.Background(), "Prepared")
	if err != nil {
		t.Fatal(err)
	}
	request := authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(),
		Commands: []authoring.Command{{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "reviewed", Position: schema.Position{X: 10, Y: 20},
		}}},
	}
	preview, err := application.PreparePatch(context.Background(), request)
	if err != nil || !preview.Patch.Valid() || preview.Patch.BaseHash() != created.Hash() || !preview.Patch.CandidateHash().Valid() {
		t.Fatalf("PreparePatch = %#v, %v", preview, err)
	}
	if current, loadErr := sources.Load(created.WorkflowID()); loadErr != nil || current.Hash() != created.Hash() {
		t.Fatalf("PreparePatch mutated durable source = %#v, %v", current, loadErr)
	}
	reviewed := preview.Patch.CandidateArtifact()
	committed, err := application.CommitPreparedPatch(context.Background(), preview.Patch)
	if err != nil || committed.Source.Hash() != preview.Patch.CandidateHash() || string(committed.Source.Artifact()) != string(reviewed) {
		t.Fatalf("CommitPreparedPatch = %#v, %v", committed, err)
	}
	if _, err := application.CommitPreparedPatch(context.Background(), preview.Patch); !errors.Is(err, workflowstore.ErrSourceConflict) {
		t.Fatalf("second CommitPreparedPatch = %v", err)
	}
}

func TestPreparedPatchCannotCommitAfterIndependentRevision(t *testing.T) {
	now := time.Date(2026, 7, 17, 9, 30, 0, 0, time.UTC)
	application, _, _, _, _, _ := newTestApplication(t, now, nil)
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	created, err := application.CreateSource(context.Background(), "Conflict")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := application.PreparePatch(context.Background(), authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(),
		Commands: []authoring.Command{{Kind: authoring.CommandRenameWorkflow, RenameWorkflow: &authoring.RenameWorkflowCommand{Name: "AI candidate"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.ApplyPatch(context.Background(), authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(),
		Commands: []authoring.Command{{Kind: authoring.CommandRenameWorkflow, RenameWorkflow: &authoring.RenameWorkflowCommand{Name: "Human edit"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.CommitPreparedPatch(context.Background(), preview.Patch); !errors.Is(err, workflowstore.ErrSourceConflict) {
		t.Fatalf("CommitPreparedPatch after human edit = %v", err)
	}
}

func TestApplicationCancellationOwnsRunningWorkerAndPersistsTerminalState(t *testing.T) {
	now := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
	invoked := make(chan struct{})
	adapter := func(ctx context.Context, _ nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
		close(invoked)
		<-ctx.Done()
		return nodeadapter.AdapterResult{}, ctx.Err()
	}
	application, _, _, _, events, _ := newTestApplication(t, now, adapter)
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved := createConcatWorkflow(t, application)
	started, err := application.StartRun(context.Background(), appcore.StartRunRequest{WorkflowID: saved.Source.WorkflowID(), Principal: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-invoked:
	case <-time.After(5 * time.Second):
		t.Fatal("adapter was not invoked")
	}
	if _, err := application.CancelRun(context.Background(), started.Record.Admission().RunID); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.RunID == started.Record.Admission().RunID && event.Status == run.StatusCancelled {
				loaded, err := application.GetRun(event.RunID)
				if err != nil || loaded.Status() != run.StatusCancelled {
					t.Fatalf("cancelled Run = %#v, %v", loaded, err)
				}
				if err := application.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				return
			}
		case <-deadline:
			t.Fatal("Run did not reach cancelled")
		}
	}
}

func TestApplicationDebugRunPausesStepsAndCancelsThroughTheOnlyWorker(t *testing.T) {
	now := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	application, _, _, _, events, debugEvents := newTestApplication(t, now, nil)
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	saved := createConcatWorkflow(t, application)
	started, err := application.StartDebugRun(context.Background(), appcore.StartRunRequest{
		WorkflowID: saved.Source.WorkflowID(), Principal: "user-1",
	}, []compiler.DebugBreakpoint{})
	if err != nil {
		t.Fatal(err)
	}
	runID := started.Record.Admission().RunID
	concatID := saved.GeneratedNodes[0].NodeID
	first := waitApplicationDebug(t, debugEvents, runID, func(snapshot compiler.DebugSnapshot) bool {
		return snapshot.Status == compiler.DebugPaused && snapshot.NodeID == concatID
	})
	if first.GraphID != "main" || first.Attempt != 1 {
		t.Fatalf("first debug snapshot = %#v", first)
	}
	if len(first.Inputs) != 2 || !first.Inputs["a"].Redacted || !first.Inputs["b"].Redacted {
		t.Fatalf("bounded debug inputs = %#v", first.Inputs)
	}
	if _, err := application.ControlDebugRun(context.Background(), runID, appcore.DebugStep); err != nil {
		t.Fatal(err)
	}
	second := waitApplicationDebug(t, debugEvents, runID, func(snapshot compiler.DebugSnapshot) bool {
		return snapshot.Status == compiler.DebugPaused && snapshot.NodeID == "run-started"
	})
	if len(second.Inputs) != 0 {
		t.Fatalf("RunStarted debug inputs = %#v", second.Inputs)
	}
	if second.PreviousNodeID != concatID || second.PreviousGraphID != "main" {
		t.Fatalf("previous debug node = %#v", second)
	}
	if _, err := application.CancelRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.RunID == runID && event.Status == run.StatusCancelled {
				snapshot, snapshotErr := application.GetDebugSnapshot(runID)
				if snapshotErr != nil || snapshot.Status != compiler.DebugCompleted || snapshot.RunStatus != string(run.StatusCancelled) {
					t.Fatalf("completed debug snapshot = %#v, %v", snapshot, snapshotErr)
				}
				return
			}
		case <-deadline:
			t.Fatal("debug Run did not cancel")
		}
	}
}

func waitApplicationDebug(t *testing.T, events <-chan appcore.DebugEvent, runID string, matches func(compiler.DebugSnapshot) bool) compiler.DebugSnapshot {
	t.Helper()
	deadline := time.After(5 * time.Second)
	var last appcore.DebugEvent
	for {
		select {
		case event := <-events:
			last = event
			if event.RunID == runID && matches(event.Snapshot) {
				return event.Snapshot
			}
		case <-deadline:
			t.Fatalf("debug event did not reach expected state; last=%#v", last)
		}
	}
}

func newTestApplication(t *testing.T, now time.Time, adapterOverride nodeadapter.Adapter, observed ...blob.Object) (*appcore.Application, *workflowstore.SourceStore, *workflowstore.ProgramStore, nodes.Builtins, chan appcore.RunEvent, chan appcore.DebugEvent) {
	return newConfiguredTestApplication(t, now, adapterOverride, nil, observed...)
}

func newConfiguredTestApplication(t *testing.T, now time.Time, adapterOverride nodeadapter.Adapter, configure func(*appcore.Config), observed ...blob.Object) (*appcore.Application, *workflowstore.SourceStore, *workflowstore.ProgramStore, nodes.Builtins, chan appcore.RunEvent, chan appcore.DebugEvent) {
	t.Helper()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := nodeauthoring.Project(nodeauthoring.Input{
		Catalog: builtins.Catalog, Types: builtins.Types, Capabilities: builtins.Capabilities,
		Contracts: builtins.Contracts, GeneratorVersion: nodes.GeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	build := testDigest(t, "application compiler")
	root := t.TempDir()
	roots, err := storage.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := catalog.Open(context.Background(), roots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foundation.Close() })
	for _, object := range observed {
		if err := foundation.Objects().Observe(context.Background(), object); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := workflowstore.OpenSourceStore(foundation.Workflows(), workflowstore.SourceStoreOptions{MaxSources: 8})
	if err != nil {
		t.Fatal(err)
	}
	programs, err := workflowstore.OpenProgramStore(filepath.Join(roots.Cache, "programs"), builtins.Catalog, builtins.ConfigValidators, build, workflowstore.ProgramStoreOptions{MaxPrograms: 8, MaxBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := run.OpenStore(foundation.Runs(), builtins.Catalog, run.StoreOptions{MaxRecords: 8})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := admission.SealHostProfile(admission.HostProfileDraft{
		OS: "windows", Architecture: "amd64", HostAPIGeneration: "1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
		return admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "test-policy"}, nil
	})
	admitter, err := admission.New(builtins.Catalog, profile, runs, policy, admission.Options{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := noderuntime.Installed(builtins, noderuntime.Dependencies{
		Script: applicationScriptRuntime{},
		Log:    noderuntime.LogEmitterFunc(func(context.Context, noderuntime.LogEntry) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapterOverride != nil {
		entry, ok := builtins.Catalog.Lookup(nodes.ConcatNodeID)
		if !ok {
			t.Fatal("Concat implementation is missing")
		}
		adapters[entry.Implementation.Entrypoint] = nodeadapter.InstalledAdapter{Implementation: entry.Implementation, Run: adapterOverride}
	}
	executor := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now }})
	events := make(chan appcore.RunEvent, 16)
	debugEvents := make(chan appcore.DebugEvent, 32)
	config := appcore.Config{
		Catalog: builtins.Catalog, Authoring: projection, CompilerBuild: build, ConfigValidators: builtins.ConfigValidators,
		BlobVerifier: compiler.BlobVerifierFunc(func(context.Context, blob.BlobRef) error { return nil }),
		Sources:      sources, Programs: programs, Runs: runs,
		Admitter: admitter, Executor: executor, Providers: map[string]run.InstalledProvider{},
		ResourceOptions: resource.Options{}, OwnerCloseTimeout: time.Second,
		Now: func() time.Time { return now }, OnRunEvent: func(event appcore.RunEvent) { events <- event },
		OnDebugEvent: func(event appcore.DebugEvent) { debugEvents <- event },
	}
	if configure != nil {
		configure(&config)
	}
	application, err := appcore.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return application, sources, programs, builtins, events, debugEvents
}

type applicationScriptRuntime struct{}

func (applicationScriptRuntime) Execute(context.Context, scriptengine.Request) (scriptengine.Response, error) {
	return scriptengine.Response{}, errors.New("unexpected script execution in application test")
}

func createConcatWorkflow(t *testing.T, application *appcore.Application) appcore.ApplyPatchResult {
	t.Helper()
	created, err := application.CreateSource(context.Background(), "Application")
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.ApplyPatch(context.Background(), authoring.PatchRequest{
		WorkflowID: created.WorkflowID(), BaseRevision: created.Revision(),
		Commands: []authoring.Command{
			{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
				GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "concat", Position: schema.Position{},
			}},
			{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$concat", PortID: "a", Value: "hello"}},
			{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$concat", PortID: "b", Value: " world"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func testDigest(t *testing.T, label string) artifact.Digest {
	t.Helper()
	digest, err := artifact.Sum("yotta/test/application/v1", []byte(label))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestIndependentRunCompletesWhileAnotherRunIsWaiting(t *testing.T) {
	now := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	entered := make(chan struct{})
	var first atomic.Bool
	var builtins nodes.Builtins
	adapter := func(ctx context.Context, i nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
		if first.CompareAndSwap(false, true) {
			close(entered)
			<-ctx.Done()
			return nodeadapter.AdapterResult{}, ctx.Err()
		}
		value, err := datatype.SealInlineJSON(builtins.Catalog, i.OutputTypes["result"], []byte(`"done"`))
		return nodeadapter.AdapterResult{Outputs: map[string]datatype.ValueEnvelope{"result": value}}, err
	}
	application, _, _, b, events, _ := newTestApplication(t, now, adapter)
	builtins = b
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := application.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	one, two := createConcatWorkflow(t, application), createConcatWorkflow(t, application)
	waiting, err := application.StartRun(context.Background(), appcore.StartRunRequest{WorkflowID: one.Source.WorkflowID(), Principal: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Run did not start")
	}
	independent, err := application.StartRun(context.Background(), appcore.StartRunRequest{WorkflowID: two.Source.WorkflowID(), Principal: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.RunID == independent.Record.Admission().RunID && event.Status == run.StatusSucceeded {
				stillWaiting, err := application.GetRun(waiting.Record.Admission().RunID)
				if err != nil || stillWaiting.Status() != run.StatusRunning {
					t.Fatalf("first Run was stopped or changed: %v", err)
				}
				if _, err := application.CancelRun(context.Background(), waiting.Record.Admission().RunID); err != nil {
					t.Fatal(err)
				}
				return
			}
		case <-deadline:
			t.Fatal("a waiting Run blocked independent execution")
		}
	}
}
