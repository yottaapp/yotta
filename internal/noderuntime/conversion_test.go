package noderuntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/admission"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/nodeadapter"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/stream"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workspacefs"
)

func TestInstalledAdaptersRejectCatalogThatSelfAssertsAnotherImplementation(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]nodecatalog.Binding, 0, len(builtins.Contracts))
	for _, contract := range builtins.Contracts {
		entry, ok := builtins.Catalog.Lookup(contract.NodeRef().NodeTypeID)
		if !ok {
			t.Fatal("built-in contract is missing from its Catalog")
		}
		lock := entry.Implementation
		if contract.NodeRef().NodeTypeID == nodes.BlobToStreamNodeID {
			lock.ArtifactDigest, err = artifact.Sum("yotta/test/forged-implementation/v1", []byte("different adapter"))
			if err != nil {
				t.Fatal(err)
			}
		}
		bindings = append(bindings, nodecatalog.Binding{Contract: contract, Implementation: lock})
	}
	forged, err := nodecatalog.Seal(builtins.Types, builtins.Capabilities, bindings, "v1")
	if err != nil {
		t.Fatal(err)
	}
	builtins.Catalog = forged
	if _, err := noderuntime.Installed(builtins, testDependencies()); err == nil {
		t.Fatal("installed adapters trusted implementation identity asserted by the supplied Catalog")
	}
}

func TestExecutorRunsPureProgramWithoutResourceProviders(t *testing.T) {
	ctx := context.Background()
	graphID := strings.Repeat("g", 128)
	nodeID := strings.Repeat("n", 128)
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	build, err := artifact.Sum("yotta/test/compiler-build/v1", []byte("noderuntime pure test"))
	if err != nil {
		t.Fatal(err)
	}
	ref := builtins.ConcatContract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-pure","name":"Pure"},
		"revision":0,"entryGraph":%q,"graphs":[{"id":%q,"kind":"main","nodes":[{
			"id":%q,"nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},
			"config":{},"bindings":{"a":{"kind":"value","value":"Yotta "},"b":{"kind":"value","value":"v1"}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, graphID, graphID, nodeID, ref.NodeTypeID, ref.SemanticDigest))
	compiled, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(ctx, compiler.CompileRequest{SourceJSON: source, Catalog: builtins.Catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile = %v, diagnostics %#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("compiler did not produce a Program")
	}
	now := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	executor := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(2 * time.Second) }})
	execution, err := executor.Run(ctx, program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(execution.NodeOutputs[nodeID]["result"].InlineJSON(), &got); err != nil {
		t.Fatal(err)
	}
	if got != "Yotta v1" {
		t.Fatalf("concat result = %q", got)
	}
	if journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("pure Run status = %s", journal.Current().Status())
	}
	if err := owner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, deniedOwner, deniedJournal := admittedExecution(t, builtins, program, nil, now)
	if err := deniedOwner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Run(ctx, program, deniedOwner, deniedJournal); !errors.Is(err, run.ErrGrantDenied) {
		t.Fatalf("execution after Run Owner close = %v", err)
	}
}

func TestExecutorClosesSuccessfulAttemptWhenCallerCancelsAsAdapterReturns(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	build, err := artifact.Sum("yotta/test/compiler-build/v1", []byte("noderuntime cancellation race"))
	if err != nil {
		t.Fatal(err)
	}
	ref := builtins.ConcatContract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-cancel-race","name":"Cancel race"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"concat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},
			"config":{},"bindings":{"a":{"kind":"value","value":"Yotta "},"b":{"kind":"value","value":"v1"}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest))
	compiled, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(context.Background(), compiler.CompileRequest{SourceJSON: source, Catalog: builtins.Catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile = %v, diagnostics %#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("compiler did not produce a Program")
	}
	now := time.Date(2026, 7, 15, 3, 30, 0, 0, time.UTC)
	_, owner, journal := admittedExecution(t, builtins, program, nil, now)
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := builtins.Catalog.Lookup(nodes.ConcatNodeID)
	if !ok {
		t.Fatal("concat Catalog entry is missing")
	}
	original := adapters[entry.Implementation.Entrypoint]
	runCtx, cancel := context.WithCancel(context.Background())
	originalRun := original.Run
	original.Run = func(ctx context.Context, invocation nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
		outputs, err := originalRun(ctx, invocation)
		cancel()
		return outputs, err
	}
	adapters[entry.Implementation.Entrypoint] = original
	executor := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(2 * time.Second) }})
	if _, err := executor.Run(runCtx, program, owner, journal); err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("Run status = %s", journal.Current().Status())
	}
	facts := journal.Current().Journal()
	if len(facts) != 2 || facts[1].AttemptOutcome != run.AttemptSucceeded {
		t.Fatalf("journal = %#v", facts)
	}
}

func TestExecutorConvertsBlobToStreamAndBackThroughAdmittedCapabilities(t *testing.T) {
	ctx := context.Background()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	store, err := blob.Open(t.TempDir(), blob.Limits{MaxBlobBytes: 2 << 20, MaxTotalBytes: 4 << 20})
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte("Yotta-v1-conversion\n"), 20_000)
	inputRef, err := store.Put(ctx, "application/octet-stream", bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	program := compilePrimitiveProgram(t, builtins, conversionSource(builtins, inputRef))

	now := time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)
	blobProvider, err := blob.NewProvider(store, blob.ProviderLimits{MaxChunkBytes: 64 << 10, QueueCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	streamProvider, err := stream.NewProvider(stream.Limits{MaxCapacity: 4, MaxChunkBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, journal := admittedExecution(t, builtins, program, map[string]run.InstalledProvider{
		blob.ProviderID:   {ArtifactDigest: blobProviderDigest(t), ABI: blob.ProviderABI, Provider: blobProvider},
		stream.ProviderID: {ArtifactDigest: streamProviderDigest(t), ABI: stream.ProviderABI, Provider: streamProvider},
	}, now)
	t.Cleanup(func() {
		if err := owner.Close(context.Background()); err != nil {
			t.Errorf("close Run Owner: %v", err)
		}
	})
	adapters, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	executor := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(2 * time.Second) }})
	execution, err := executor.Run(ctx, program, owner, journal)
	if err != nil {
		t.Fatal(err)
	}
	facts := journal.Current().Journal()
	if len(facts) != 6 || facts[1].Kind != run.JournalAdapterAction || facts[1].Action != "conversion.stream-opened" ||
		facts[1].ActionOutcome != run.ActionSucceeded || facts[1].Summary.Counters["bytes"] != int64(len(want)) ||
		facts[4].Kind != run.JournalAdapterAction || facts[4].Action != "conversion.blob-committed" {
		t.Fatalf("conversion journal = %#v", facts)
	}
	if journal.Current().Status() != run.StatusSucceeded {
		t.Fatalf("conversion Run status = %s", journal.Current().Status())
	}
	if _, exposed := execution.NodeOutputs["to-stream"]["stream"]; exposed {
		t.Fatal("ExecutionResult exposed a reclaimed runtime authority")
	}
	output, ok := execution.NodeOutputs["to-blob"]["blob"].BlobRef()
	if !ok {
		t.Fatal("conversion did not produce a BlobRef")
	}
	if reclaimed, err := store.Sweep(nil); err != nil || reclaimed != 0 {
		t.Fatalf("Sweep reclaimed uncommitted Run output: %d, %v", reclaimed, err)
	}
	got, err := store.ReadRange(ctx, output, 0, output.Size)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("converted bytes differ: got %d bytes, want %d", len(got), len(want))
	}
	if err := owner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reclaimed, err := store.Sweep(nil); err != nil || reclaimed != 1 {
		t.Fatalf("Sweep after Run close = %d, %v; want released output", reclaimed, err)
	}
}

func TestExecutorFailsClosedWhenEffectAdapterJournalIsMissingOrCancelled(t *testing.T) {
	ctx := context.Background()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	store, err := blob.Open(t.TempDir(), blob.Limits{MaxBlobBytes: 1 << 20, MaxTotalBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	inputRef, err := store.Put(ctx, "application/octet-stream", bytes.NewReader([]byte("journal")))
	if err != nil {
		t.Fatal(err)
	}
	build, err := artifact.Sum("yotta/test/compiler-build/v1", []byte("noderuntime journal failures"))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(ctx, compiler.CompileRequest{
		SourceJSON: conversionSource(builtins, inputRef), Catalog: builtins.Catalog,
	})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile = %v, diagnostics %#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("compiler did not produce a Program")
	}
	blobProvider, err := blob.NewProvider(store, blob.ProviderLimits{MaxChunkBytes: 64 << 10, QueueCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	streamProvider, err := stream.NewProvider(stream.Limits{MaxCapacity: 4, MaxChunkBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	providers := map[string]run.InstalledProvider{
		blob.ProviderID:   {ArtifactDigest: blobProviderDigest(t), ABI: blob.ProviderABI, Provider: blobProvider},
		stream.ProviderID: {ArtifactDigest: streamProviderDigest(t), ABI: stream.ProviderABI, Provider: streamProvider},
	}
	installed, err := noderuntime.Installed(builtins, testDependencies())
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := builtins.Catalog.Lookup(nodes.BlobToStreamNodeID)
	if !ok {
		t.Fatal("blob-to-stream Catalog entry is missing")
	}
	now := time.Date(2026, 7, 15, 4, 0, 0, 0, time.UTC)
	original := installed[entry.Implementation.Entrypoint].Run
	tests := []struct {
		name       string
		adapter    nodeadapter.Adapter
		wantStatus run.Status
		wantError  error
		wantText   string
	}{
		{name: "missing action", adapter: func(context.Context, nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
			return nodeadapter.AdapterResult{}, errors.New("adapter omitted its action")
		}, wantStatus: run.StatusFailed},
		{name: "cancelled action", adapter: func(ctx context.Context, invocation nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
			err := invocation.RecordAction(context.WithoutCancel(ctx), nodeadapter.AdapterAction{
				EffectID: nodes.BlobToStreamEffectID, Action: "conversion.test-action", Outcome: run.ActionCancelled,
				SummaryCode: "conversion.test",
			})
			return nodeadapter.AdapterResult{}, errors.Join(context.Canceled, err)
		}, wantStatus: run.StatusCancelled, wantError: context.Canceled},
		{name: "duplicate action", adapter: func(ctx context.Context, invocation nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
			outputs, err := original(ctx, invocation)
			if err != nil {
				return nodeadapter.AdapterResult{}, err
			}
			_ = invocation.RecordAction(context.WithoutCancel(ctx), nodeadapter.AdapterAction{
				EffectID: nodes.BlobToStreamEffectID, Action: "conversion.duplicate", Outcome: run.ActionSucceeded,
				SummaryCode: "conversion.test",
			})
			return outputs, nil
		}, wantStatus: run.StatusFailed, wantText: "more than once"},
		{name: "failed action with successful return", adapter: func(ctx context.Context, invocation nodeadapter.Invocation) (nodeadapter.AdapterResult, error) {
			err := invocation.RecordAction(context.WithoutCancel(ctx), nodeadapter.AdapterAction{
				EffectID: nodes.BlobToStreamEffectID, Action: "conversion.failed", Outcome: run.ActionFailed,
				ErrorCode: "conversion.blob_to_stream_failed", SummaryCode: "conversion.test",
			})
			return nodeadapter.AdapterResult{}, err
		}, wantStatus: run.StatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			grant, owner, journal := admittedExecution(t, builtins, program, providers, now)
			t.Cleanup(func() { _ = owner.Close(context.Background()) })
			adapters := make(map[string]nodeadapter.InstalledAdapter, len(installed))
			for id, adapter := range installed {
				adapters[id] = adapter
			}
			adapters[entry.Implementation.Entrypoint] = nodeadapter.InstalledAdapter{Implementation: entry.Implementation, Run: test.adapter}
			executor := compiler.NewExecutor(builtins.Catalog, adapters, compiler.ExecutorOptions{Now: func() time.Time { return now.Add(2 * time.Second) }})
			_, runErr := executor.Run(ctx, program, owner, journal)
			if runErr == nil || test.wantError != nil && !errors.Is(runErr, test.wantError) || test.wantText != "" && !strings.Contains(runErr.Error(), test.wantText) {
				t.Fatalf("Run error = %v", runErr)
			}
			if journal.Current().Status() != test.wantStatus {
				t.Fatalf("Run %s status = %s", grant.RunID(), journal.Current().Status())
			}
		})
	}
}

func admittedExecution(t *testing.T, builtins nodes.Builtins, program compiler.ProgramSnapshot, providers map[string]run.InstalledProvider, now time.Time) (capability.RunGrant, *run.Owner, *run.JournalWriter) {
	t.Helper()
	return admittedExecutionWithProfile(t, builtins, program, providers, now, executionProfile(t, builtins))
}

func admittedExecutionWithProfile(t *testing.T, builtins nodes.Builtins, program compiler.ProgramSnapshot, providers map[string]run.InstalledProvider, now time.Time, profileDraft admission.HostProfileDraft) (capability.RunGrant, *run.Owner, *run.JournalWriter) {
	t.Helper()
	return admittedExecutionWithConsent(t, builtins, program, providers, now, profileDraft, nil)
}

func admittedExecutionWithConsent(t *testing.T, builtins nodes.Builtins, program compiler.ProgramSnapshot, providers map[string]run.InstalledProvider, now time.Time, profileDraft admission.HostProfileDraft, consentLineage []artifact.Digest, observeStore ...func(*run.Store)) (capability.RunGrant, *run.Owner, *run.JournalWriter) {
	t.Helper()
	profile, err := admission.SealHostProfile(profileDraft)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newNodeRuntimeRunStore(t, builtins.Catalog, run.StoreOptions{MaxRecords: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, observe := range observeStore {
		observe(store)
	}
	policy := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
		return admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "policy-1", ConsentLineage: consentLineage}, nil
	})
	admitter, err := admission.New(builtins.Catalog, profile, store, policy, admission.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := admitter.Admit(context.Background(), admission.Request{Program: program, Principal: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := result.Record.Start(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), result.Record.Digest(), running); err != nil {
		t.Fatal(err)
	}
	journal, err := store.OpenJournal(result.Grant.RunID())
	if err != nil {
		t.Fatal(err)
	}
	owner, err := run.NewOwner(context.Background(), result.Grant, providers, resource.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result.Grant, owner, journal
}

func executionProfile(t *testing.T, builtins nodes.Builtins) admission.HostProfileDraft {
	t.Helper()
	capabilityRef := func(id string) capability.Ref {
		definition, ok := builtins.Catalog.LookupCapability(id)
		if !ok {
			t.Fatalf("missing capability %q", id)
		}
		return definition.Ref()
	}
	return admission.HostProfileDraft{
		OS: "windows", Architecture: "amd64", HostAPIGeneration: "1.0",
		Features: []string{scriptengine.IsolationHostFeatureID},
		Providers: []admission.ProviderDescriptor{
			{ID: blob.ProviderID, ArtifactDigest: blobProviderDigest(t), ABI: blob.ProviderABI, PluginInstanceID: "builtin",
				OperatingSystems: []string{"windows"}, Architectures: []string{"amd64"}, HostAPIs: []string{"1.0"}, Capabilities: []admission.ProviderCapability{
					{Capability: capabilityRef(nodes.BlobReadCapabilityID), ResourceKind: blob.KindReader},
					{Capability: capabilityRef(nodes.BlobWriteCapabilityID), ResourceKind: blob.KindWriter},
				}},
			{ID: stream.ProviderID, ArtifactDigest: streamProviderDigest(t), ABI: stream.ProviderABI, PluginInstanceID: "builtin",
				OperatingSystems: []string{"windows"}, Architectures: []string{"amd64"}, HostAPIs: []string{"1.0"}, Capabilities: []admission.ProviderCapability{
					{Capability: capabilityRef(nodes.StreamCapabilityID), ResourceKind: stream.Kind},
				}},
			{ID: workspacefs.ProviderID, ArtifactDigest: workspaceFSProviderDigest(t), ABI: workspacefs.ProviderABI, PluginInstanceID: "builtin",
				OperatingSystems: []string{"windows"}, Architectures: []string{"amd64"}, HostAPIs: []string{"1.0"}, Capabilities: []admission.ProviderCapability{
					{Capability: capabilityRef(nodes.FilesystemCapabilityID), ResourceKind: workspacefs.Kind},
				}},
		},
		Targets: []admission.AutomationTarget{
			{ID: "workspace", Kind: "blob-store", ProviderID: blob.ProviderID},
			{ID: "memory", Kind: "stream-session", ProviderID: stream.ProviderID},
			{ID: workspacefs.TargetID, Kind: workspacefs.TargetKind, ProviderID: workspacefs.ProviderID},
		},
		TargetSlots: []admission.TargetSlotBinding{{Slot: "workspace-files", TargetID: workspacefs.TargetID}},
	}
}

func blobProviderDigest(t *testing.T) artifact.Digest {
	t.Helper()
	digest, err := blob.ProviderArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func streamProviderDigest(t *testing.T) artifact.Digest {
	t.Helper()
	digest, err := stream.ProviderArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func workspaceFSProviderDigest(t *testing.T) artifact.Digest {
	t.Helper()
	digest, err := workspacefs.ProviderArtifactDigest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func conversionSource(builtins nodes.Builtins, ref blob.BlobRef) []byte {
	toStream := builtins.BlobToStreamContract.NodeRef()
	toBlob := builtins.StreamToBlobContract.NodeRef()
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-convert","name":"Convert"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"to-stream","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"blob":{"kind":"blob","blob":{"mediaType":%q,"digest":%q,"size":%d}}}},
			{"id":"to-blob","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},
			 "config":{"mediaType":"application/octet-stream"},"bindings":{}}
		],"edges":[{"channel":"data","from":{"nodeId":"to-stream","portId":"stream"},
		"to":{"nodeId":"to-blob","portId":"stream"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, toStream.NodeTypeID, toStream.SemanticDigest, ref.MediaType, ref.Digest, ref.Size, toBlob.NodeTypeID, toBlob.SemanticDigest))
}
