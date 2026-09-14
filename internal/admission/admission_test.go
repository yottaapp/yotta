package admission_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/admission"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/storage"
	storagecatalog "github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/stream"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

const admissionRunID = "0190c7d4-1e40-7cc5-a783-57b16d5c8e3a"

func newAdmissionRunStore(
	t *testing.T,
	valueCatalog datatype.ValueTypeCatalog,
	options run.StoreOptions,
) (*run.Store, error) {
	t.Helper()
	roots, err := storage.Resolve(filepath.Join(t.TempDir(), "profile"))
	if err != nil {
		return nil, err
	}
	foundation, err := storagecatalog.Open(context.Background(), roots)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		if err := foundation.Close(); err != nil {
			t.Errorf("close test Run Ledger: %v", err)
		}
	})
	return run.OpenStore(foundation.Runs(), valueCatalog, options)
}

func TestAdmitterRejectsMissingHostFeatureBeforePolicyOrRunCreation(t *testing.T) {
	builtins, program := scriptProgram(t)
	profileDraft := builtinProfileDraft(t, builtins)
	if len(profileDraft.Features) != 0 {
		t.Fatalf("test profile unexpectedly has features: %#v", profileDraft.Features)
	}
	profile, err := admission.SealHostProfile(profileDraft)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newAdmissionRunStore(t, builtins.Catalog, run.StoreOptions{MaxRecords: 8})
	if err != nil {
		t.Fatal(err)
	}
	policyCalls := 0
	policy := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
		policyCalls++
		return admission.PolicyDecision{}, nil
	})
	runIDCalls := 0
	admitter, err := admission.New(builtins.Catalog, profile, store, policy, admission.Options{
		Now: time.Now, NewRunID: func() (string, error) { runIDCalls++; return admissionRunID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = admitter.Admit(context.Background(), admission.Request{Program: program, Principal: "user-1"})
	var admissionErr *admission.Error
	if !errors.As(err, &admissionErr) || admissionErr.Code != admission.CodeUnsupportedHost ||
		admissionErr.GraphID != "main" || admissionErr.NodeID != "script" || admissionErr.RequirementID != "isolation" {
		t.Fatalf("Admit error = %#v", err)
	}
	if policyCalls != 0 || runIDCalls != 0 {
		t.Fatalf("unsupported host reached policy/run identity: policy=%d runID=%d", policyCalls, runIDCalls)
	}
	if scriptengine.IsolationHostFeatureID == "" {
		t.Fatal("script isolation feature identity is empty")
	}
}

func TestAdmitterReplacementUsesOneNewEnvironmentGeneration(t *testing.T) {
	builtins, program := scriptProgram(t)
	draft := builtinProfileDraft(t, builtins)
	oldProfile, err := admission.SealHostProfile(draft)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newAdmissionRunStore(t, builtins.Catalog, run.StoreOptions{MaxRecords: 8})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	deny := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
		return admission.PolicyDecision{Outcome: admission.PolicyDenied}, nil
	})
	admitter, err := admission.New(builtins.Catalog, oldProfile, store, deny, admission.Options{
		Now: func() time.Time { return now }, NewRunID: func() (string, error) { return admissionRunID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	draft.Features = []string{scriptengine.IsolationHostFeatureID}
	newProfile, err := admission.SealHostProfile(draft)
	if err != nil {
		t.Fatal(err)
	}
	approved := admission.PolicyFunc(func(_ context.Context, request admission.PolicyRequest) (admission.PolicyDecision, error) {
		if request.HostProfile != newProfile.Digest() {
			t.Fatalf("policy saw profile %q, want %q", request.HostProfile, newProfile.Digest())
		}
		return admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "live-generation"}, nil
	})
	if err := admitter.ReplaceEnvironment(newProfile, approved); err != nil {
		t.Fatal(err)
	}
	result, err := admitter.Admit(context.Background(), admission.Request{Program: program, Principal: "user-1"})
	if err != nil || !result.Grant.Valid() || !result.Record.Valid() {
		t.Fatalf("Admit after replacement = %+v, %v", result, err)
	}
}

func TestHostProfileRejectsInvalidFeatureSets(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	draft := builtinProfileDraft(t, builtins)
	draft.Features = []string{"not-a-versioned-uri"}
	if _, err := admission.SealHostProfile(draft); err == nil {
		t.Fatal("invalid host feature URI was accepted")
	}
	draft = builtinProfileDraft(t, builtins)
	draft.Features = make([]string, 257)
	for index := range draft.Features {
		draft.Features[index] = fmt.Sprintf("https://schemas.yotta.dev/host-features/test-%03d/v1", index)
	}
	if _, err := admission.SealHostProfile(draft); err == nil {
		t.Fatal("host feature budget overflow was accepted")
	}
}

func TestAdmitterPlansPolicyAndPersistsQueuedRunBeforeReturning(t *testing.T) {
	builtins, program := conversionProgram(t)
	now := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	profile := builtinProfile(t, builtins)
	store, err := newAdmissionRunStore(t, builtins.Catalog, run.StoreOptions{MaxRecords: 8})
	if err != nil {
		t.Fatal(err)
	}
	policyCalls := 0
	policy := admission.PolicyFunc(func(_ context.Context, request admission.PolicyRequest) (admission.PolicyDecision, error) {
		policyCalls++
		if request.ProgramHash != program.Hash() || request.PlanDigest != program.CapabilityPlan().Digest() || len(request.Bindings) != 4 {
			t.Fatalf("policy request = %#v", request)
		}
		return admission.PolicyDecision{
			Outcome: admission.PolicyApproved, Generation: "policy-1",
		}, nil
	})
	admitter, err := admission.New(builtins.Catalog, profile, store, policy, admission.Options{
		Now: func() time.Time { return now }, NewRunID: func() (string, error) { return admissionRunID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := admitter.Admit(context.Background(), admission.Request{Program: program, Principal: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if policyCalls != 1 || result.Grant.RunID() != admissionRunID || result.Record.Status() != run.StatusQueued {
		t.Fatalf("admission result = %#v, policy calls = %d", result, policyCalls)
	}
	loaded, err := store.Load(admissionRunID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Digest() != result.Record.Digest() || loaded.Admission().GrantDigest != result.Grant.Digest() {
		t.Fatal("Admit returned before the exact QUEUED RunRecord was durable")
	}
	if string(loaded.GrantArtifact()) != string(result.Grant.Bytes()) {
		t.Fatal("durable RunRecord did not retain the strict-open Run Grant artifact")
	}
	recoveredGrant, err := capability.OpenRunGrant(loaded.GrantArtifact(), program.CapabilityPlan(), builtins.Catalog)
	if err != nil || recoveredGrant.Digest() != result.Grant.Digest() {
		t.Fatalf("recover durable Run Grant = %s, %v", recoveredGrant.Digest(), err)
	}
	entries := result.Grant.Entries()
	sessionsByTarget := map[string]string{}
	for _, entry := range entries {
		if !entry.Binding.ProviderArtifactDigest.Valid() || entry.Binding.ProviderABI == "" {
			t.Fatalf("planned binding = %#v", entry.Binding)
		}
		key := entry.Binding.ProviderID + "/" + entry.Binding.TargetID
		if previous := sessionsByTarget[key]; previous != "" && previous != entry.Binding.SessionID {
			t.Fatalf("shared target received different sessions: %q / %q", previous, entry.Binding.SessionID)
		}
		sessionsByTarget[key] = entry.Binding.SessionID
	}
	if len(sessionsByTarget) != 2 || sessionsByTarget[blob.ProviderID+"/workspace"] == sessionsByTarget[stream.ProviderID+"/memory"] {
		t.Fatalf("target sessions = %#v", sessionsByTarget)
	}
}

func TestAdmitterReturnsPublishedRunWhenDurabilityCannotBeConfirmed(t *testing.T) {
	builtins, program := conversionProgram(t)
	now := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	wantErr := errors.New("sync Run Store directory")
	store := &uncertainRecordCreator{err: wantErr}
	policy := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
		return admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "policy-42"}, nil
	})
	admitter, err := admission.New(builtins.Catalog, builtinProfile(t, builtins), store, policy, admission.Options{
		Now: func() time.Time { return now }, NewRunID: func() (string, error) { return admissionRunID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := admitter.Admit(context.Background(), admission.Request{Program: program, Principal: "user-1"})
	var admissionErr *admission.Error
	if !errors.As(err, &admissionErr) || admissionErr.Code != admission.CodePersistenceUnconfirmed ||
		admissionErr.Commit != run.CommitPublished || !errors.Is(err, wantErr) {
		t.Fatalf("Admit error = %#v", err)
	}
	if !result.Grant.Valid() || !result.Record.Valid() || result.Record.Admission().RunID != admissionRunID || store.calls != 1 {
		t.Fatalf("published admission result = %#v, create calls = %d", result, store.calls)
	}
}

type uncertainRecordCreator struct {
	calls int
	err   error
}

func (s *uncertainRecordCreator) Create(context.Context, run.Record) (run.CommitOutcome, error) {
	s.calls++
	return run.CommitPublished, s.err
}

func TestAdmitterRejectsAmbiguousTargetBeforePolicyOrRunCreation(t *testing.T) {
	builtins, program := conversionProgram(t)
	now := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	draft := builtinProfileDraft(t, builtins)
	second := draft.Providers[0]
	second.ID = "blob-secondary"
	second.ArtifactDigest = testDigest(t, "blob-secondary")
	draft.Providers = append(draft.Providers, second)
	draft.Targets = append(draft.Targets, admission.AutomationTarget{ID: "archive", Kind: "blob-store", ProviderID: second.ID})
	profile, err := admission.SealHostProfile(draft)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newAdmissionRunStore(t, builtins.Catalog, run.StoreOptions{MaxRecords: 8})
	if err != nil {
		t.Fatal(err)
	}
	policyCalls := 0
	policy := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
		policyCalls++
		return admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "policy-1"}, nil
	})
	admitter, err := admission.New(builtins.Catalog, profile, store, policy, admission.Options{
		Now: func() time.Time { return now }, NewRunID: func() (string, error) { return admissionRunID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = admitter.Admit(context.Background(), admission.Request{Program: program, Principal: "user-1"})
	var admissionErr *admission.Error
	if !errors.As(err, &admissionErr) || admissionErr.Code != admission.CodeTargetAmbiguous {
		t.Fatalf("Admit error = %v", err)
	}
	if policyCalls != 0 {
		t.Fatal("policy ran before target planning was unambiguous")
	}
	if _, err := store.Load(admissionRunID); !errors.Is(err, run.ErrRunNotFound) {
		t.Fatalf("ambiguous admission created a RunRecord: %v", err)
	}

	result, err := admitter.Admit(context.Background(), admission.Request{
		Program: program, Principal: "user-1", Selection: admission.Selection{Targets: map[string]string{"blob-store": "workspace"}},
	})
	if err != nil || result.Record.Status() != run.StatusQueued {
		t.Fatalf("explicit target selection = %#v, %v", result, err)
	}
}

func TestAdmitterUsesTrustedHostProfileSlotBindings(t *testing.T) {
	builtins, program := conversionProgram(t)
	now := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	draft := builtinProfileDraft(t, builtins)
	second := draft.Providers[0]
	second.ID = "blob-secondary"
	second.ArtifactDigest = testDigest(t, "blob-secondary")
	draft.Providers = append(draft.Providers, second)
	draft.Targets = append(draft.Targets, admission.AutomationTarget{ID: "archive", Kind: "blob-store", ProviderID: second.ID})
	draft.TargetSlots = []admission.TargetSlotBinding{{Slot: "blob-store", TargetID: "workspace"}}
	profile, err := admission.SealHostProfile(draft)
	if err != nil {
		t.Fatal(err)
	}
	store, err := newAdmissionRunStore(t, builtins.Catalog, run.StoreOptions{MaxRecords: 8})
	if err != nil {
		t.Fatal(err)
	}
	policy := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
		return admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "policy-1"}, nil
	})
	admitter, err := admission.New(builtins.Catalog, profile, store, policy, admission.Options{
		Now: func() time.Time { return now }, NewRunID: func() (string, error) { return admissionRunID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := admitter.Admit(context.Background(), admission.Request{Program: program, Principal: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range result.Grant.Entries() {
		if entry.Binding.TargetKind == "blob-store" && entry.Binding.TargetID != "workspace" {
			t.Fatalf("host slot binding selected %q", entry.Binding.TargetID)
		}
	}

	draft.TargetSlots = []admission.TargetSlotBinding{{Slot: "blob-store", TargetID: "missing"}}
	if _, err := admission.SealHostProfile(draft); err == nil {
		t.Fatal("accepted a host slot binding to an unknown target")
	}
}

func TestAdmitterRejectsProviderABIMismatchAndPolicyDenialBeforeRunCreation(t *testing.T) {
	builtins, program := conversionProgram(t)
	now := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		profile  admission.HostProfile
		decision admission.PolicyDecision
		wantCode string
	}{
		{name: "provider ABI mismatch", profile: func() admission.HostProfile {
			draft := builtinProfileDraft(t, builtins)
			draft.Providers[0].ABI = "https://schemas.yotta.dev/provider-abi/wrong/v1"
			profile, err := admission.SealHostProfile(draft)
			if err != nil {
				t.Fatal(err)
			}
			return profile
		}(), decision: admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "policy-1"}, wantCode: admission.CodeProviderIncompatible},
		{name: "unsupported host", profile: func() admission.HostProfile {
			draft := builtinProfileDraft(t, builtins)
			for index := range draft.Providers {
				draft.Providers[index].OperatingSystems = []string{"linux"}
			}
			profile, err := admission.SealHostProfile(draft)
			if err != nil {
				t.Fatal(err)
			}
			return profile
		}(), decision: admission.PolicyDecision{Outcome: admission.PolicyApproved, Generation: "policy-1"}, wantCode: admission.CodeUnsupportedHost},
		{name: "policy denied", profile: builtinProfile(t, builtins), decision: admission.PolicyDecision{Outcome: admission.PolicyDenied}, wantCode: admission.CodePolicyDenied},
		{name: "consent required", profile: builtinProfile(t, builtins), decision: admission.PolicyDecision{Outcome: admission.PolicyConsentRequired}, wantCode: admission.CodeConsentRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := newAdmissionRunStore(t, builtins.Catalog, run.StoreOptions{MaxRecords: 8})
			if err != nil {
				t.Fatal(err)
			}
			policy := admission.PolicyFunc(func(context.Context, admission.PolicyRequest) (admission.PolicyDecision, error) {
				return test.decision, nil
			})
			admitter, err := admission.New(builtins.Catalog, test.profile, store, policy, admission.Options{
				Now: func() time.Time { return now }, NewRunID: func() (string, error) { return admissionRunID, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = admitter.Admit(context.Background(), admission.Request{Program: program, Principal: "user-1"})
			var admissionErr *admission.Error
			if !errors.As(err, &admissionErr) || admissionErr.Code != test.wantCode {
				t.Fatalf("Admit error = %v", err)
			}
			if _, err := store.Load(admissionRunID); !errors.Is(err, run.ErrRunNotFound) {
				t.Fatalf("rejected admission created a RunRecord: %v", err)
			}
		})
	}
}

func builtinProfile(t *testing.T, builtins nodes.Builtins) admission.HostProfile {
	t.Helper()
	profile, err := admission.SealHostProfile(builtinProfileDraft(t, builtins))
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func builtinProfileDraft(t *testing.T, builtins nodes.Builtins) admission.HostProfileDraft {
	t.Helper()
	lookup := func(id string) capability.Ref {
		definition, ok := builtins.Catalog.LookupCapability(id)
		if !ok {
			t.Fatalf("missing capability %q", id)
		}
		return definition.Ref()
	}
	return admission.HostProfileDraft{
		OS: "windows", Architecture: "amd64", HostAPIGeneration: "1.0",
		Providers: []admission.ProviderDescriptor{
			{ID: blob.ProviderID, ArtifactDigest: testDigest(t, "blob-provider"), ABI: "https://schemas.yotta.dev/provider-abi/resource/v1", PluginInstanceID: "builtin",
				OperatingSystems: []string{"windows"}, Architectures: []string{"amd64"}, HostAPIs: []string{"1.0"}, Capabilities: []admission.ProviderCapability{
					{Capability: lookup(nodes.BlobReadCapabilityID), ResourceKind: blob.KindReader},
					{Capability: lookup(nodes.BlobWriteCapabilityID), ResourceKind: blob.KindWriter},
				}},
			{ID: stream.ProviderID, ArtifactDigest: testDigest(t, "stream-provider"), ABI: "https://schemas.yotta.dev/provider-abi/resource/v1", PluginInstanceID: "builtin",
				OperatingSystems: []string{"windows"}, Architectures: []string{"amd64"}, HostAPIs: []string{"1.0"}, Capabilities: []admission.ProviderCapability{
					{Capability: lookup(nodes.StreamCapabilityID), ResourceKind: stream.Kind},
				}},
		},
		Targets: []admission.AutomationTarget{
			{ID: "workspace", Kind: "blob-store", ProviderID: blob.ProviderID},
			{ID: "memory", Kind: "stream-session", ProviderID: stream.ProviderID},
		},
	}
}

func conversionProgram(t *testing.T) (nodes.Builtins, compiler.ProgramSnapshot) {
	t.Helper()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	build := testDigest(t, "admission compiler")
	toStream := builtins.BlobToStreamContract.NodeRef()
	toBlob := builtins.StreamToBlobContract.NodeRef()
	ref := blob.BlobRef{MediaType: "application/octet-stream", Digest: testDigest(t, "input"), Size: 5}
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-admission","name":"Admission"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"to-stream","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"blob":{"kind":"blob","blob":{"mediaType":%q,"digest":%q,"size":%d}}}},
			{"id":"to-blob","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},
			 "config":{"mediaType":"application/octet-stream"},"bindings":{}}
		],"edges":[{"channel":"data","from":{"nodeId":"to-stream","portId":"stream"},"to":{"nodeId":"to-blob","portId":"stream"}}],
		"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, toStream.NodeTypeID, toStream.SemanticDigest, ref.MediaType, ref.Digest, ref.Size, toBlob.NodeTypeID, toBlob.SemanticDigest))
	compiled, err := compiler.New(build, builtins.ConfigValidators).CompileDraft(context.Background(), compiler.CompileRequest{SourceJSON: source, Catalog: builtins.Catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile = %v, diagnostics %#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("compiler did not produce a Program")
	}
	return builtins, program
}

func scriptProgram(t *testing.T) (nodes.Builtins, compiler.ProgramSnapshot) {
	t.Helper()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	ref := builtins.ScriptExecuteContract.NodeRef()
	started, ok := builtins.Catalog.Lookup(nodes.RunStartedNodeID)
	if !ok {
		t.Fatal("run-started contract is missing")
	}
	startRef := started.Contract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-script-admission","name":"Script admission"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"script","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},
			 "config":{"source":"return input;","timeoutMilliseconds":1000},"bindings":{"input":{"kind":"default"}}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"script","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, startRef.NodeTypeID, startRef.SemanticDigest, ref.NodeTypeID, ref.SemanticDigest))
	compiled, err := compiler.New(testDigest(t, "script admission compiler"), builtins.ConfigValidators).CompileDraft(
		context.Background(), compiler.CompileRequest{SourceJSON: source, Catalog: builtins.Catalog},
	)
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile script = %v, diagnostics %#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("compiler did not produce a script Program")
	}
	nodes := program.Nodes()
	if len(nodes) != 2 || len(nodes[1].HostFeatures) != 1 || nodes[1].HostFeatures[0].FeatureID != scriptengine.IsolationHostFeatureID {
		t.Fatalf("script Program host requirements = %#v", nodes)
	}
	return builtins, program
}

func testDigest(t *testing.T, label string) artifact.Digest {
	t.Helper()
	digest, err := artifact.Sum("yotta/test/admission/v1", []byte(label))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
