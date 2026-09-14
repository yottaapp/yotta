package appbootstrap_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/admission"
	"github.com/yottaapp/yotta/internal/ai"
	"github.com/yottaapp/yotta/internal/appbootstrap"
	"github.com/yottaapp/yotta/internal/appcontrol"
	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/artifact"
	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/httpegress"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/services/workflow"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workspacefs"
)

func TestBuildComposesWorkflowServiceThroughProductionProgramChain(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	events := make(chan appcore.RunEvent, 16)
	stores := newTestWorkflowStorage(t)
	runtime, err := appbootstrap.Build(appbootstrap.Config{
		DataRoot: stores.roots.Data, ProgramCacheRoot: filepath.Join(stores.roots.Cache, "programs"),
		WorkflowRepository: stores.foundation.Workflows(),
		RunRepository:      stores.foundation.Runs(),
		BlobStore:          stores.blobs,
		Limits:             testLimits(), AIInstallations: emptyAIInstallations(t), HTTPInstallations: emptyHTTPInstallations(t), ApplicationInstallations: emptyApplicationInstallations(t), AutomationInstallations: emptyAutomationInstallations(t), ScriptRuntime: bootstrapScriptRuntime(t),
		LogEmitter:        discardWorkflowLog{},
		OwnerCloseTimeout: time.Second, Now: func() time.Time { return now },
		OnRunEvent: func(event appcore.RunEvent) { events <- event },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	service, err := workflow.NewService(runtime.Application)
	if err != nil {
		t.Fatal(err)
	}
	createdSource, err := service.CreateSource("Bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	patched, err := service.ApplyPatch(createdSource.WorkflowID, createdSource.Revision, []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "concat", Position: schema.Position{},
		}},
		{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$concat", PortID: "a", Value: "a"}},
		{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$concat", PortID: "b", Value: "b"}},
	})
	if err != nil || patched.Source.SourceJSON == "" || patched.Source.Revision != 1 || len(patched.GeneratedNodes) != 1 {
		t.Fatalf("ApplyPatch = %#v, %v", patched, err)
	}
	saved := patched.Source
	compiled, err := service.CompileSource(saved.WorkflowID)
	if err != nil || !compiled.ProgramHash.Valid() || len(compiled.Diagnostics) != 0 || saved.SourceHash != compiled.SourceHash {
		t.Fatalf("CompileSource = %#v, %v", compiled, err)
	}
	if record, found, err := stores.foundation.Workflows().Get(context.Background(), saved.WorkflowID); err != nil ||
		!found || record.Revision != saved.Revision || record.Hash != saved.SourceHash {
		t.Fatalf("Catalog Workflow Source = %#v, found=%v, %v", record, found, err)
	}
	if _, err := os.Stat(filepath.Join(stores.roots.Cache, "programs")); err != nil {
		t.Fatalf("Program cache RootSet projection: %v", err)
	}
	for _, retired := range []string{
		filepath.Join(stores.roots.Data, "workspace", "workflows"),
		filepath.Join(stores.roots.Data, "workspace", "programs"),
	} {
		if _, err := os.Stat(retired); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retired workspace store was created at %s: %v", retired, err)
		}
	}
	listed, err := service.ListSources()
	if err != nil || len(listed) != 1 || listed[0].Name != "Bootstrap" || listed[0].SourceJSON != "" || listed[0].SourceHash != saved.SourceHash {
		t.Fatalf("ListSources = %#v, %v", listed, err)
	}
	if authoring := service.GetAuthoringProjection(); !strings.Contains(authoring, `"format":"yotta.node-authoring-projection"`) {
		t.Fatalf("GetAuthoringProjection = %s", authoring)
	}
	created, err := service.CreateSource(" Empty ")
	if err != nil || created.Name != "Empty" || created.Revision != 0 || !strings.Contains(created.SourceJSON, `"version":"5"`) {
		t.Fatalf("CreateSource = %#v, %v", created, err)
	}
	started, err := service.StartRun(saved.WorkflowID)
	if err != nil || started.Run == nil || started.Run.Status != string(run.StatusQueued) || started.ProgramHash != compiled.ProgramHash {
		t.Fatalf("StartRun = %#v, %v", started, err)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if event.RunID == started.Run.RunID && event.Status == run.StatusSucceeded {
				timeline, err := service.GetRunTimeline(event.RunID)
				if err != nil || timeline.Status != string(run.StatusSucceeded) || len(timeline.Timeline) != 4 || timeline.Failure != nil {
					t.Fatalf("GetRunTimeline = %#v, %v", timeline, err)
				}
				if catalog := service.GetCatalog(); !strings.Contains(catalog, `"version":"1"`) {
					t.Fatalf("GetCatalog = %s", catalog)
				}
				return
			}
		case <-deadline:
			t.Fatal("production Run did not succeed")
		}
	}
}

func TestBuildStartsWithOneCorruptWorkflowSourceIsolatedAndRepairable(t *testing.T) {
	stores := newTestWorkflowStorage(t)
	build := func() *appbootstrap.Runtime {
		runtime, err := appbootstrap.Build(appbootstrap.Config{
			DataRoot: stores.roots.Data, ProgramCacheRoot: filepath.Join(stores.roots.Cache, "programs"),
			WorkflowRepository: stores.foundation.Workflows(),
			RunRepository:      stores.foundation.Runs(),
			BlobStore:          stores.blobs, Limits: testLimits(),
			AIInstallations: emptyAIInstallations(t), HTTPInstallations: emptyHTTPInstallations(t),
			ApplicationInstallations: emptyApplicationInstallations(t), AutomationInstallations: emptyAutomationInstallations(t),
			ScriptRuntime: bootstrapScriptRuntime(t), LogEmitter: discardWorkflowLog{},
			OwnerCloseTimeout: time.Second, Now: time.Now,
		})
		if err != nil {
			t.Fatalf("Build = %v", err)
		}
		if err := runtime.Application.Start(context.Background()); err != nil {
			t.Fatalf("Start = %v", err)
		}
		return runtime
	}
	closeRuntime := func(runtime *appbootstrap.Runtime) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Fatalf("Close = %v", err)
		}
	}

	first := build()
	service, err := workflow.NewService(first.Application)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSource("Recoverable")
	if err != nil {
		t.Fatal(err)
	}
	closeRuntime(first)
	if _, err := stores.foundation.Workflows().Delete(
		context.Background(), created.WorkflowID, created.Revision, created.SourceHash,
	); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte(`{"format":"yotta.workflow","version":"5",`)
	recoveryID, err := artifact.Sum("yotta/test/workflow-quarantine/v1", corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if err := stores.foundation.Workflows().PutQuarantine(context.Background(), catalog.WorkflowQuarantineRecord{
		ID: recoveryID, OriginalName: created.WorkflowID + ".json",
		Reason: "synthetic invalid JSON", Artifact: corrupt, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	second := build()
	t.Cleanup(func() { closeRuntime(second) })
	recoveryService, err := workflow.NewService(second.Application)
	if err != nil {
		t.Fatal(err)
	}
	if listed, err := recoveryService.ListSources(); err != nil || len(listed) != 0 {
		t.Fatalf("healthy Sources after isolation = %#v, %v", listed, err)
	}
	recoveries := recoveryService.ListSourceRecoveries()
	if len(recoveries) != 1 || recoveries[0].OriginalName != created.WorkflowID+".json" {
		t.Fatalf("recoveries = %#v", recoveries)
	}
	repaired, err := recoveryService.RepairSourceRecovery(recoveries[0].RecoveryID, created.SourceJSON)
	if err != nil || repaired.WorkflowID != created.WorkflowID || len(recoveryService.ListSourceRecoveries()) != 0 {
		t.Fatalf("RepairSourceRecovery = %#v, %v", repaired, err)
	}
}

func TestRuntimeHotReplacesApplicationAutomationAndAuthoringGeneration(t *testing.T) {
	if !automationinstalled.PlatformSupported() {
		t.Skip("installed automation targets are intentionally unavailable")
	}
	stores := newTestWorkflowStorage(t)
	runtime, err := appbootstrap.Build(appbootstrap.Config{
		DataRoot: stores.roots.Data, ProgramCacheRoot: filepath.Join(stores.roots.Cache, "programs"),
		WorkflowRepository: stores.foundation.Workflows(),
		RunRepository:      stores.foundation.Runs(),
		BlobStore:          stores.blobs, Limits: testLimits(),
		AIInstallations: emptyAIInstallations(t), HTTPInstallations: emptyHTTPInstallations(t),
		ApplicationInstallations: emptyApplicationInstallations(t), AutomationInstallations: emptyAutomationInstallations(t),
		ScriptRuntime: bootstrapScriptRuntime(t), LogEmitter: discardWorkflowLog{},
		OwnerCloseTimeout: time.Second, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	path := filepath.Join(t.TempDir(), "editor.exe")
	if err := os.WriteFile(path, []byte("live-automation-target"), 0o700); err != nil {
		t.Fatal(err)
	}
	applicationDraft := appcontrol.ProfileDraft{Executable: path, Arguments: []string{}}
	automationProfile := automationinstalled.NewDesktopProfileDraft(automationinstalled.DesktopProfilePayload{
		Application: applicationDraft, WindowTitle: "Editor.*", WindowTitleMatch: "regex", WindowSelection: "topmost",
		WindowClass: "EditorWindow", InputBackend: "postmessage", CaptureBackend: "gdi", ResolveTimeoutMilliseconds: 500,
	})
	prepared, err := runtime.PrepareAutomation([]appcontrol.InstallationDraft{{Slot: "editor", Profile: applicationDraft}}, []automationinstalled.InstallationDraft{{
		Slot: "editor-window", Profile: automationProfile,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.Commit(); err == nil {
		t.Fatal("prepared automation generation committed twice")
	}
	aborted, err := runtime.PrepareAutomation(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	aborted.Abort()
	if err := aborted.Commit(); err == nil {
		t.Fatal("aborted automation generation committed")
	}
	if backend, err := runtime.AuthoringTargets().CaptureBackend("editor-window"); err != nil || backend != "gdi" {
		t.Fatalf("live authoring target = %q, %v", backend, err)
	}
	service, err := workflow.NewService(runtime.Application)
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.CreateSource("Live target admission")
	if err != nil {
		t.Fatal(err)
	}
	patched, err := service.ApplyPatch(source.WorkflowID, source.Revision, []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.PressKeysNodeID, Handle: "keys", Position: schema.Position{X: 400, Y: 160}}},
		{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "$keys", FieldID: "slot", Value: schema.DefaultWorkflowTargetID}},
		{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$keys", PortID: "keys", Value: []string{"F9"}}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: authoring.PatchEdgeFromSource(schema.Edge{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "run-started", PortID: "started"}, To: schema.Endpoint{NodeID: "$keys", PortID: "in"}})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ds, err := service.SaveTargetBindings(patched.Source.WorkflowID, patched.Source.Revision, map[string]string{schema.DefaultWorkflowTargetID: "editor-window"}); err != nil || schema.HasErrors(ds) {
		t.Fatalf("save local target: %v, %+v", err, ds)
	}
	started, err := service.StartRun(patched.Source.WorkflowID)
	if err != nil || started.Run == nil || started.Run.RunID == "" {
		t.Fatalf("same-process target admission = %#v, %v", started, err)
	}
	replacement, err := runtime.PrepareAutomation(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.AuthoringTargets().CaptureBackend("editor-window"); err == nil {
		t.Fatal("removed target remained visible through the live authoring handle")
	}
	rollback, err := runtime.PrepareAutomation([]appcontrol.InstallationDraft{{Slot: "editor", Profile: applicationDraft}}, []automationinstalled.InstallationDraft{{
		Slot: "editor-window", Profile: automationProfile,
	}})
	if err != nil {
		t.Fatal(err)
	}
	closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelClose()
	if err := runtime.Application.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err := rollback.Commit(); err == nil {
		t.Fatal("published automation generation after Application closed")
	}
	if _, err := runtime.AuthoringTargets().CaptureBackend("editor-window"); err == nil {
		t.Fatal("failed publication changed the authoring generation")
	}
}

func TestRuntimeHotInstallsAIModelForNewRuns(t *testing.T) {
	stores := newTestWorkflowStorage(t)
	runtime, err := appbootstrap.Build(appbootstrap.Config{
		DataRoot: stores.roots.Data, ProgramCacheRoot: filepath.Join(stores.roots.Cache, "programs"),
		WorkflowRepository: stores.foundation.Workflows(),
		RunRepository:      stores.foundation.Runs(),
		BlobStore:          stores.blobs, Limits: testLimits(),
		AIInstallations: emptyAIInstallations(t), HTTPInstallations: emptyHTTPInstallations(t),
		ApplicationInstallations: emptyApplicationInstallations(t), AutomationInstallations: emptyAutomationInstallations(t),
		ScriptRuntime: bootstrapScriptRuntime(t), LogEmitter: discardWorkflowLog{},
		OwnerCloseTimeout: time.Second, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Close = %v", err)
		}
	})

	prepared, err := runtime.PrepareInstallations(
		[]ai.InstallationDraft{{Slot: "model", Profile: ai.ModelProfileDraft{
			Provider: ai.ProviderOpenAIResponses, Endpoint: "http://127.0.0.1:1/v1/responses",
			AllowLocalHTTP: true, Model: "test-model", MaxOutputTokens: 64,
			Evaluation: ai.EvaluationUnverified, ProviderMetadata: json.RawMessage(`{}`),
		}}},
		testAICredentials{},
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Commit(); err != nil {
		t.Fatal(err)
	}

	service, err := workflow.NewService(runtime.Application)
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.CreateSource("Live AI model")
	if err != nil {
		t.Fatal(err)
	}
	patched, err := service.ApplyPatch(source.WorkflowID, source.Revision, []authoring.Command{
		addNode("generate", nodes.AIGenerateNodeID, 320),
		setSlot("generate", "model"),
		bindValue("generate", "prompt", "hello"),
		connect("run-started", "started", "$generate", "in"),
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.StartRun(patched.Source.WorkflowID)
	if err != nil || started.Run == nil || started.Run.RunID == "" {
		t.Fatalf("same-process AI admission = %#v, %v", started, err)
	}
}

func TestCompositionRootProjectsAutomationAsConfiguredTargets(t *testing.T) {
	source, err := os.ReadFile("execution_environment.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"switch resourceKind",
		"automationinstalled.OperationPressKeys",
		"nodes.AutomationKeyInputCapabilityID",
		"nodes.AutomationDesktopInputCapabilityID",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("composition root contains a second automation capability fact source: %s", forbidden)
		}
	}
	if strings.Contains(text, "manifest.Capabilities") || !strings.Contains(text, "targetruntime.Installation") {
		t.Fatal("composition root did not project automation as plain configured targets")
	}
}

func TestBuiltinPolicyRejectsUninstalledProviderIdentity(t *testing.T) {
	policy, err := appbootstrap.NewBuiltinPolicy(emptyAIInstallations(t))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.Authorize(context.Background(), admission.PolicyRequest{Bindings: []capability.Binding{{
		ProviderID: "third-party", ProviderArtifactDigest: testDigest(t, "forged"), ProviderABI: "https://example.test/abi/v1",
		TargetID: "remote", TargetKind: "remote", PluginInstanceID: "plugin",
	}}})
	if err != nil || decision.Outcome != admission.PolicyDenied {
		t.Fatalf("Authorize = %#v, %v", decision, err)
	}
}

func TestBuiltinPolicyPinsWorkspaceFilesystemProvider(t *testing.T) {
	policy, err := appbootstrap.NewBuiltinPolicy(emptyAIInstallations(t))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspacefs.ProviderArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	binding := capability.Binding{
		ProviderID: workspacefs.ProviderID, ProviderArtifactDigest: digest, ProviderABI: workspacefs.ProviderABI,
		TargetID: workspacefs.TargetID, TargetKind: workspacefs.TargetKind, ResourceKind: workspacefs.Kind,
		PluginInstanceID: "builtin",
	}
	decision, err := policy.Authorize(context.Background(), admission.PolicyRequest{Bindings: []capability.Binding{binding}})
	if err != nil || decision.Outcome != admission.PolicyApproved {
		t.Fatalf("workspace filesystem decision = %#v, %v", decision, err)
	}
	binding.TargetID = "host-root"
	decision, err = policy.Authorize(context.Background(), admission.PolicyRequest{Bindings: []capability.Binding{binding}})
	if err != nil || decision.Outcome != admission.PolicyDenied {
		t.Fatalf("forged workspace filesystem decision = %#v, %v", decision, err)
	}
}

func TestBuiltinPolicyApprovesExactConfiguredAIInstallation(t *testing.T) {
	profileDraft := ai.ModelProfileDraft{
		Provider: ai.ProviderOpenAIResponses, Model: "gpt-test", MaxOutputTokens: 4096,
		Capabilities: ai.ProfileCapabilities{StructuredOutput: true},
	}
	profileDraft, evaluation := approvedAIProfile(t, profileDraft)
	installed, err := ai.Install([]ai.InstallationDraft{{Slot: "primary", Profile: profileDraft, Evaluation: evaluation}}, testAICredentials{})
	if err != nil {
		t.Fatal(err)
	}
	entry := installed.Entries()[0]
	binding := capability.Binding{
		ProviderID: entry.ProviderID, ProviderArtifactDigest: entry.ProviderArtifact, ProviderABI: ai.ProviderABI,
		TargetID: entry.TargetID, TargetKind: "ai-model", ResourceKind: ai.KindModelSession,
		PluginInstanceID: "builtin", CredentialBindingID: entry.CredentialBindingID,
	}
	policy, err := appbootstrap.NewBuiltinPolicy(installed)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.Authorize(context.Background(), admission.PolicyRequest{Bindings: []capability.Binding{binding}})
	if err != nil || decision.Outcome != admission.PolicyApproved || len(decision.ConsentLineage) != 0 {
		t.Fatalf("configured decision = %#v, %v", decision, err)
	}
	binding.CredentialBindingID = "ai-credential/forged"
	decision, err = policy.Authorize(context.Background(), admission.PolicyRequest{Bindings: []capability.Binding{binding}})
	if err != nil || decision.Outcome != admission.PolicyDenied {
		t.Fatalf("forged decision = %#v, %v", decision, err)
	}
}

func TestBuiltinPolicyLimitsUnverifiedAIToOrdinaryGeneration(t *testing.T) {
	installed, err := ai.Install([]ai.InstallationDraft{{Slot: "model", Profile: ai.ModelProfileDraft{
		Provider: ai.ProviderOpenAIResponses, Model: "gpt-test", MaxOutputTokens: 4096,
		Evaluation: ai.EvaluationUnverified,
	}}}, testAICredentials{})
	if err != nil {
		t.Fatal(err)
	}
	entry := installed.Entries()[0]
	binding := capability.Binding{
		GraphID: "main", NodeID: "ai", RequirementID: "model",
		ProviderID: entry.ProviderID, ProviderArtifactDigest: entry.ProviderArtifact, ProviderABI: ai.ProviderABI,
		TargetID: entry.TargetID, TargetKind: "ai-model", ResourceKind: ai.KindModelSession,
		PluginInstanceID: "builtin", CredentialBindingID: entry.CredentialBindingID,
	}
	policy, err := appbootstrap.NewBuiltinPolicy(installed)
	if err != nil {
		t.Fatal(err)
	}
	request := admission.PolicyRequest{
		Bindings: []capability.Binding{binding},
		Requirements: []capability.PlanEntry{{
			GraphID: "main", NodeID: "ai",
			Requirement: capability.Requirement{ID: "model", Operations: []string{ai.OperationGenerate}},
		}},
	}
	decision, err := policy.Authorize(context.Background(), request)
	if err != nil || decision.Outcome != admission.PolicyApproved {
		t.Fatalf("unverified generation decision = %#v, %v", decision, err)
	}
	request.Requirements[0].Requirement.Operations = []string{ai.OperationAgentStart}
	decision, err = policy.Authorize(context.Background(), request)
	if err != nil || decision.Outcome != admission.PolicyDenied {
		t.Fatalf("unverified agent decision = %#v, %v", decision, err)
	}
}

type testAICredentials struct{}

func (testAICredentials) Get(string) (string, error) { return "secret", nil }

type discardWorkflowLog struct{}

func (discardWorkflowLog) EmitWorkflowLog(context.Context, noderuntime.LogEntry) error { return nil }

func bootstrapScriptRuntime(t *testing.T) *scriptengine.Runtime {
	t.Helper()
	runtime, err := scriptengine.NewRuntime(scriptengine.RuntimeOptions{
		Executable:         filepath.Join(t.TempDir(), scriptengine.WorkerExecutableName),
		ProcessMemoryBytes: scriptengine.DefaultMemoryBytes, JobMemoryBytes: scriptengine.DefaultMemoryBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func emptyAIInstallations(t *testing.T) ai.Installations {
	t.Helper()
	installations, err := ai.Install(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return installations
}

func emptyHTTPInstallations(t *testing.T) httpegress.Installations {
	t.Helper()
	installations, err := httpegress.Install(nil)
	if err != nil {
		t.Fatal(err)
	}
	return installations
}

func emptyApplicationInstallations(t *testing.T) appcontrol.Installations {
	t.Helper()
	installations, err := appcontrol.Install(nil)
	if err != nil {
		t.Fatal(err)
	}
	return installations
}

func emptyAutomationInstallations(t *testing.T) automationinstalled.Installations {
	t.Helper()
	installations, err := automationinstalled.Install(nil)
	if err != nil {
		t.Fatal(err)
	}
	return installations
}

func testLimits() appbootstrap.Limits {
	return appbootstrap.Limits{
		MaxSources: 8, MaxPrograms: 8, MaxRuns: 8,
		MaxProgramCacheBytes:    8 << 20,
		MaxResourcePayloadBytes: 2 << 20,
		BlobChunkBytes:          64 << 10, BlobQueueCapacity: 2, StreamCapacity: 4, StreamChunkBytes: 64 << 10,
	}
}

type testWorkflowStorage struct {
	roots      storage.Roots
	foundation *catalog.Foundation
	blobs      *blob.Store
}

func newTestWorkflowStorage(t *testing.T) testWorkflowStorage {
	t.Helper()
	roots, err := storage.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := catalog.Open(context.Background(), roots)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := foundation.Close(); err != nil {
			t.Errorf("close test Catalog = %v", err)
		}
	})
	blobs, err := blob.Open(
		roots.Objects,
		blob.Limits{MaxBlobBytes: 1 << 20, MaxTotalBytes: 8 << 20},
		foundation.Objects(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return testWorkflowStorage{roots: roots, foundation: foundation, blobs: blobs}
}

func testDigest(t *testing.T, label string) artifact.Digest {
	t.Helper()
	digest, err := artifact.Sum("yotta/test/appbootstrap/v1", []byte(label))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func approvedAIProfile(t *testing.T, draft ai.ModelProfileDraft) (ai.ModelProfileDraft, ai.EvalReportArtifact) {
	t.Helper()
	suite, err := ai.BuiltinEvalSuite()
	if err != nil {
		t.Fatal(err)
	}
	draft.Evaluation = ai.EvaluationUnverified
	draft.EvaluationSuite = ""
	draft.EvaluationReport = ""
	profile, err := ai.SealModelProfile(draft)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := ai.EvaluationSubjectDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := ai.NewEvalCandidate(subject, []artifact.Digest{suite.Machine().Baseline})
	if err != nil {
		t.Fatal(err)
	}
	observations := make([]ai.EvalObservation, 0, len(suite.Machine().Cases))
	for _, evalCase := range suite.Machine().Cases {
		observations = append(observations, ai.EvalObservation{
			CaseID: evalCase.ID, Output: append(json.RawMessage(nil), evalCase.Expected...), Refused: evalCase.RequireRefusal,
			InputTokens: 10, OutputTokens: 5, CostMicrounits: 100, LatencyMillis: 10,
		})
	}
	evidence, err := ai.GradeEvalSuite(suite, candidate, observations)
	if err != nil {
		t.Fatal(err)
	}
	draft.Evaluation = ai.EvaluationApproved
	draft.EvaluationSuite = suite.Digest()
	draft.EvaluationReport = evidence.Digest
	return draft, evidence
}
