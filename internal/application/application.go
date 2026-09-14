// Package application owns the Yotta command surface and its single local
// Run worker. GUI, CLI, MCP, schedules, and hotkeys call this package instead
// of constructing execution runtimes themselves.
package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yottaapp/yotta/internal/admission"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/configvalidator"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/runprepare"
	"github.com/yottaapp/yotta/internal/targetruntime"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

var (
	ErrNotStarted = errors.New("application is not started")
	ErrClosed     = errors.New("application is closed")
)

type Config struct {
	Parameters        *workflowstore.ParameterStore
	NodePackages      []schema.NodePackageDependency
	Catalog           nodecatalog.Snapshot
	Authoring         nodeauthoring.Snapshot
	CompilerBuild     artifact.Digest
	ConfigValidators  configvalidator.Registry
	BlobVerifier      compiler.BlobVerifier
	RunImagePlanner   *runprepare.Planner
	Sources           *workflowstore.SourceStore
	Programs          *workflowstore.ProgramStore
	Runs              *run.Store
	Admitter          *admission.Admitter
	Executor          *compiler.Executor
	Providers         map[string]run.InstalledProvider
	TargetSnapshot    func() (targetruntime.Snapshot, func(), error)
	ResourceOptions   resource.Options
	OwnerCloseTimeout time.Duration
	Now               func() time.Time
	OnRunEvent        func(RunEvent)
	OnDebugEvent      func(DebugEvent)
}

type RunEvent struct {
	RunID      string
	WorkflowID string
	Status     run.Status
	Generation uint64
	Digest     artifact.Digest
	Err        error
}

type DebugEvent struct {
	RunID    string
	Snapshot compiler.DebugSnapshot
}

type DebugAction string

const (
	DebugContinue DebugAction = "continue"
	DebugPause    DebugAction = "pause"
	DebugStep     DebugAction = "step"
)

const MaxDebugSessions = 128

type StartRunRequest struct {
	WorkflowID string
	Principal  string
	Selection  admission.Selection
}

type StartArtifactRunRequest struct {
	SourceArtifact []byte
	Principal      string
	Selection      admission.Selection
}

type StartRunResult struct {
	SourceHash  artifact.Digest
	ProgramHash artifact.Digest
	Diagnostics []schema.Diagnostic
	Record      run.Record
}

type ApplyPatchResult struct {
	Source         workflowstore.SourceSnapshot
	GeneratedNodes []authoring.GeneratedNode
}

// UnsafeStateMigrationError reports compiler errors introduced by changing a
// referenced state variable. The candidate is never persisted.
type UnsafeStateMigrationError struct {
	Diagnostics []schema.Diagnostic
}

func (e *UnsafeStateMigrationError) Error() string {
	if e == nil || len(e.Diagnostics) == 0 {
		return "state type migration is unsafe"
	}
	first := e.Diagnostics[0]
	location := strings.Join(first.GraphPath, "/")
	if first.NodeID != "" {
		if location != "" {
			location += "/"
		}
		location += first.NodeID
	}
	if location == "" {
		return fmt.Sprintf("state type migration introduces %s", first.Code)
	}
	return fmt.Sprintf("state type migration introduces %s at %s", first.Code, location)
}

// PreparedPatch is an application-sealed, exact Workflow Source transition.
// Its state is intentionally opaque: presentation and AI clients can review a
// candidate, but only Application can create or commit one.
type PreparedPatch struct{ state *preparedPatchState }

type preparedPatchState struct {
	workflowID           string
	baseRevision         int64
	baseHash             artifact.Digest
	candidate            []byte
	candidateHash        artifact.Digest
	generated            []authoring.GeneratedNode
	unsafeStateMigration []schema.Diagnostic
}

func (p PreparedPatch) Valid() bool {
	return p.state != nil && p.state.workflowID != "" && p.state.baseRevision >= 0 &&
		p.state.baseHash.Valid() && p.state.candidateHash.Valid() && len(p.state.candidate) != 0
}

func (p PreparedPatch) BaseHash() artifact.Digest {
	if !p.Valid() {
		return ""
	}
	return p.state.baseHash
}

func (p PreparedPatch) CandidateHash() artifact.Digest {
	if !p.Valid() {
		return ""
	}
	return p.state.candidateHash
}

func (p PreparedPatch) CandidateArtifact() []byte {
	if !p.Valid() {
		return nil
	}
	return append([]byte(nil), p.state.candidate...)
}

func (p PreparedPatch) GeneratedNodes() []authoring.GeneratedNode {
	if !p.Valid() {
		return nil
	}
	return append([]authoring.GeneratedNode(nil), p.state.generated...)
}

type PreparePatchResult struct {
	Patch          PreparedPatch
	Diagnostics    []schema.Diagnostic
	CapabilityPlan []capability.PlanEntry
}

type RunPreview struct {
	SourceHash     artifact.Digest        `json:"sourceHash,omitempty"`
	ProgramHash    artifact.Digest        `json:"programHash,omitempty"`
	Diagnostics    []schema.Diagnostic    `json:"diagnostics"`
	CapabilityPlan []capability.PlanEntry `json:"capabilityPlan"`
}

type lifecycleState uint8

const (
	stateNew lifecycleState = iota
	stateRunning
	stateClosed
)

type Application struct {
	parameters         *workflowstore.ParameterStore
	pluginMu           sync.RWMutex
	validatePlugins    func([]byte) ([]string, error)
	prepareRunServices func(context.Context, []string, targetruntime.Snapshot) ([]string, error)
	nodePackages       []schema.NodePackageDependency
	catalog            nodecatalog.Snapshot
	authoring          nodeauthoring.Snapshot
	authoringEngine    *authoring.Engine
	compiler           *compiler.Compiler
	blobVerifier       compiler.BlobVerifier
	runImagePlanner    *runprepare.Planner
	sources            *workflowstore.SourceStore
	programs           *workflowstore.ProgramStore
	runs               *run.Store
	admitter           *admission.Admitter
	providers          map[string]run.InstalledProvider // immutable; replacement assigns a new snapshot
	targetSnapshot     func() (targetruntime.Snapshot, func(), error)
	executor           *compiler.Executor
	resourceOptions    resource.Options
	ownerCloseTimeout  time.Duration
	onRunEvent         func(RunEvent)
	onDebugEvent       func(DebugEvent)
	now                func() time.Time

	commandMu sync.RWMutex
	state     lifecycleState

	admissionMu sync.RWMutex
	runMu       sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	wake        chan struct{}
	queue       []string
	jobs        map[string]*runJob
	debug       map[string]*compiler.DebugController
	worker      sync.WaitGroup
}

// ReplaceExecutionEnvironment atomically switches admission and the provider
// generation used by future Runs. Already queued/running jobs keep the exact
// provider snapshot and lease acquired with their admission generation.
func (a *Application) ReplaceExecutionEnvironment(profile admission.HostProfile, policy admission.Policy, providers map[string]run.InstalledProvider, targets func() (targetruntime.Snapshot, func(), error)) error {
	if a == nil {
		return errors.New("replacement execution environment is invalid")
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if a.state == stateClosed {
		return ErrClosed
	}
	return a.replaceAdmissionEnvironment(profile, policy, providers, targets)
}

func New(config Config) (*Application, error) {
	if !config.Catalog.Valid() || !config.Authoring.Valid() || config.Authoring.CatalogHash() != config.Catalog.Hash() ||
		!config.CompilerBuild.Valid() || !config.ConfigValidators.Valid() || config.BlobVerifier == nil || config.Sources == nil || config.Programs == nil ||
		config.Runs == nil || config.Admitter == nil || config.Executor == nil || config.OwnerCloseTimeout <= 0 {
		return nil, errors.New("application requires trusted contracts, stores, admission, executor, and owner timeout")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Parameters == nil {
		config.Parameters, _ = workflowstore.OpenParameterStore("")
	}
	authoringEngine, err := authoring.New(config.Catalog, config.Authoring, nil, config.NodePackages...)
	if err != nil {
		return nil, fmt.Errorf("construct authoring engine: %w", err)
	}
	providers, err := cloneProviderSnapshot(config.Providers)
	if err != nil {
		return nil, err
	}
	return &Application{
		nodePackages: append([]schema.NodePackageDependency(nil), config.NodePackages...),
		catalog:      config.Catalog, authoring: config.Authoring, authoringEngine: authoringEngine,
		compiler: compiler.New(config.CompilerBuild, config.ConfigValidators), blobVerifier: config.BlobVerifier,
		runImagePlanner: config.RunImagePlanner,
		sources:         config.Sources, programs: config.Programs, runs: config.Runs,
		parameters: config.Parameters,
		admitter:   config.Admitter, providers: providers, targetSnapshot: config.TargetSnapshot, executor: config.Executor,
		resourceOptions: config.ResourceOptions, ownerCloseTimeout: config.OwnerCloseTimeout,
		onRunEvent: config.OnRunEvent, onDebugEvent: config.OnDebugEvent,
		now: config.Now, state: stateNew, wake: make(chan struct{}, 1),
		jobs: make(map[string]*runJob), debug: make(map[string]*compiler.DebugController),
	}, nil
}

// Start recovers stale process-owned states before accepting commands. A
// running Run is interrupted and an undelivered queued Run is cancelled; no
// effect is replayed from a guessed notification state.
func (a *Application) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("application start context is required")
	}
	a.commandMu.Lock()
	if a.state == stateClosed {
		a.commandMu.Unlock()
		return ErrClosed
	}
	if a.state == stateRunning {
		a.commandMu.Unlock()
		return nil
	}
	now := a.now().UTC()
	interrupted, err := a.runs.InterruptRunning(ctx, now)
	if err != nil {
		a.commandMu.Unlock()
		return fmt.Errorf("interrupt stale Runs: %w", err)
	}
	cancelled, err := a.runs.CancelQueued(ctx, now)
	if err != nil {
		a.commandMu.Unlock()
		return fmt.Errorf("cancel stale queued Runs: %w", err)
	}
	if err := a.migrateCompatibleContracts(ctx); err != nil {
		a.commandMu.Unlock()
		return fmt.Errorf("migrate Workflow node contracts: %w", err)
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.worker.Add(MaxConcurrentRuns)
	for range MaxConcurrentRuns {
		go a.workerLoop()
	}
	a.state = stateRunning
	a.commandMu.Unlock()
	for _, record := range append(interrupted, cancelled...) {
		a.emit(record, nil)
	}
	return nil
}

// CompileSource compiles one durable revision. External authoring clients use
// this command instead of submitting an untrusted replacement document.
func (a *Application) CompileSource(ctx context.Context, workflowID string) (compiler.CompileResult, error) {
	if ctx == nil {
		return compiler.CompileResult{}, errors.New("compile Workflow Source context is required")
	}
	a.commandMu.RLock()
	defer a.commandMu.RUnlock()
	if err := a.requireRunning(); err != nil {
		return compiler.CompileResult{}, err
	}
	source, err := a.sources.Load(workflowID)
	if err != nil {
		return compiler.CompileResult{}, err
	}
	return a.compileDraft(ctx, source.Artifact())
}

// CompileDraft validates and compiles an in-memory Source through the same
// trusted Catalog/compiler path without publishing a Source or Program.
func (a *Application) CompileDraft(ctx context.Context, sourceJSON []byte) (compiler.CompileResult, error) {
	if ctx == nil {
		return compiler.CompileResult{}, errors.New("compile Workflow Source context is required")
	}
	a.commandMu.RLock()
	defer a.commandMu.RUnlock()
	if err := a.requireRunning(); err != nil {
		return compiler.CompileResult{}, err
	}
	return a.compileDraft(ctx, append([]byte(nil), sourceJSON...))
}

// PreviewRun performs the exact stored-source compilation used by StartRun and
// returns the frozen capability requirements without admission or effects.
func (a *Application) PreviewRun(ctx context.Context, workflowID string) (RunPreview, error) {
	compiled, err := a.CompileSource(ctx, workflowID)
	preview := RunPreview{
		SourceHash:     compiled.SourceHash,
		Diagnostics:    append([]schema.Diagnostic(nil), compiled.Diagnostics...),
		CapabilityPlan: []capability.PlanEntry{},
	}
	if err != nil {
		return preview, err
	}
	if program, ok := compiled.Program(); ok {
		preview.ProgramHash = program.Hash()
		preview.CapabilityPlan = program.CapabilityPlan().Entries()
	}
	return preview, nil
}

// CreateSource creates the only valid authoring root. IDs and structural
// defaults are host-owned so UI, CLI, and MCP cannot invent divergent source
// envelopes.
func (a *Application) CreateSource(ctx context.Context, name string) (workflowstore.SourceSnapshot, error) {
	return a.CreateSourceWithMetadata(ctx, authoring.WorkflowMetadata{Name: name})
}

func (a *Application) CreateSourceWithMetadata(ctx context.Context, requested authoring.WorkflowMetadata) (workflowstore.SourceSnapshot, error) {
	if ctx == nil {
		return workflowstore.SourceSnapshot{}, errors.New("create Workflow Source context is required")
	}
	metadata, err := authoring.NormalizeWorkflowMetadata(requested)
	if err != nil {
		return workflowstore.SourceSnapshot{}, err
	}
	a.commandMu.RLock()
	defer a.commandMu.RUnlock()
	if err := a.requireRunning(); err != nil {
		return workflowstore.SourceSnapshot{}, err
	}
	runStarted, ok := a.catalog.Lookup(nodes.RunStartedNodeID)
	if !ok {
		return workflowstore.SourceSnapshot{}, errors.New("catalog is missing the RunStarted node")
	}
	source := schema.WorkflowSource{
		Format: schema.Format, Version: schema.Version,
		Workflow: schema.Workflow{
			ID: uuid.NewString(), Name: metadata.Name, Description: metadata.Description,
			Category: metadata.Category, Tags: metadata.Tags,
		},
		Revision: 0, EntryGraph: "main",
		Targets: schema.DefaultWorkflowTargets(), TargetDefaults: []schema.TargetDefault{{Target: "target", Slot: schema.DefaultWorkflowTargetID}},
		Graphs: []schema.Graph{{
			ID: "main", Kind: schema.GraphKindMain,
			Nodes: []schema.Node{{
				ID: "run-started", NodeRef: runStarted.Contract.NodeRef(), Position: schema.Position{X: 120, Y: 160},
				Config: map[string]any{}, Bindings: map[string]schema.InputBinding{},
			}},
			Edges: []schema.Edge{}, Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
		}},
		Resources: []schema.WorkflowResource{}, TargetProfileDefinitions: []schema.TargetProfileDefinition{},
		CredentialRequirements: []schema.CredentialRequirement{}, Dependencies: []schema.NodePackageDependency{}, Variables: []schema.Variable{},
	}
	timestamp := formatWorkflowTimestamp(a.now())
	source.Workflow.CreatedAt = timestamp
	source.Workflow.UpdatedAt = timestamp
	raw, err := artifact.Marshal(source)
	if err != nil {
		return workflowstore.SourceSnapshot{}, err
	}
	return a.sources.Save(ctx, raw, -1)
}

func (a *Application) StartRun(ctx context.Context, request StartRunRequest) (StartRunResult, error) {
	a.pluginMu.RLock()
	defer a.pluginMu.RUnlock()
	if ctx == nil {
		return StartRunResult{}, errors.New("start Run context is required")
	}
	a.commandMu.RLock()
	defer a.commandMu.RUnlock()
	return a.startRun(ctx, request, false, nil)
}

// StartArtifactRun executes immutable Source bytes that another trusted
// module has already authorized. It shares the compiler, admission, ledger,
// provider, and worker path with local editable Source runs.
func (a *Application) StartArtifactRun(ctx context.Context, request StartArtifactRunRequest) (StartRunResult, error) {
	a.pluginMu.RLock()
	defer a.pluginMu.RUnlock()
	if ctx == nil {
		return StartRunResult{}, errors.New("start artifact Run context is required")
	}
	if len(request.SourceArtifact) == 0 {
		return StartRunResult{}, errors.New("start artifact Run requires Workflow Source bytes")
	}
	a.commandMu.RLock()
	defer a.commandMu.RUnlock()
	currentSource, _, err := workflowstore.MigrateSourceArtifact(request.SourceArtifact)
	if err != nil {
		return StartRunResult{}, err
	}
	return a.startRunArtifact(ctx, currentSource, request.Principal, request.Selection, "", false, nil)
}

func (a *Application) StartDebugRun(ctx context.Context, request StartRunRequest, breakpoints []compiler.DebugBreakpoint) (StartRunResult, error) {
	a.pluginMu.RLock()
	defer a.pluginMu.RUnlock()
	if ctx == nil {
		return StartRunResult{}, errors.New("start debug Run context is required")
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.validateDebugStart(breakpoints); err != nil {
		return StartRunResult{}, err
	}
	return a.startRun(ctx, request, true, breakpoints)
}

func (a *Application) startRun(ctx context.Context, request StartRunRequest, debug bool, breakpoints []compiler.DebugBreakpoint) (StartRunResult, error) {
	if err := a.requireRunning(); err != nil {
		return StartRunResult{}, err
	}
	source, err := a.sources.Load(request.WorkflowID)
	if err != nil {
		return StartRunResult{}, err
	}
	return a.startRunArtifact(ctx, source.Artifact(), request.Principal, request.Selection, request.WorkflowID, debug, breakpoints)
}

func (a *Application) startRunArtifact(
	ctx context.Context,
	sourceArtifact []byte,
	principal string,
	selection admission.Selection,
	sourceWorkflowID string,
	debug bool,
	breakpoints []compiler.DebugBreakpoint,
) (StartRunResult, error) {
	if err := a.requireRunning(); err != nil {
		return StartRunResult{}, err
	}
	parameterSource, parameterDiagnostics := schema.ParseSource(sourceArtifact)
	if schema.HasErrors(parameterDiagnostics) {
		return StartRunResult{Diagnostics: parameterDiagnostics}, nil
	}
	parameterValues, err := a.parameters.Load(parameterSource.Workflow.ID)
	if err != nil {
		return StartRunResult{}, err
	}
	var packageIDs []string
	if a.validatePlugins != nil {
		var err error
		packageIDs, err = a.validatePlugins(sourceArtifact)
		if err != nil {
			return StartRunResult{}, err
		}
	}
	leased, err := a.leaseRunTargets()
	if err != nil {
		return StartRunResult{}, err
	}
	leasePublished := false
	defer func() {
		if !leasePublished && leased.release != nil {
			leased.release()
		}
	}()
	// Compile once to use the scheduler's actual active-node projection. Draft
	// nodes must not require a local target merely because they exist in Source.
	compiled, err := a.compiler.CompileDraft(ctx, compiler.CompileRequest{
		SourceJSON: sourceArtifact, Catalog: a.catalog, BlobVerifier: a.blobVerifier, ParameterValues: parameterValues,
	})
	result := StartRunResult{SourceHash: compiled.SourceHash, Diagnostics: append([]schema.Diagnostic(nil), compiled.Diagnostics...)}
	if err != nil || schema.HasErrors(compiled.Diagnostics) {
		return result, err
	}
	program, ok := compiled.Program()
	if !ok {
		return result, errors.New("compiler returned no Program without diagnostics")
	}
	boundTargets, targetDiagnostics, err := a.bindWorkflowTargets(parameterSource, parameterValues, leased.targets, program)
	result.Diagnostics = append(result.Diagnostics, targetDiagnostics...)
	if err != nil || schema.HasErrors(targetDiagnostics) {
		return result, err
	}
	leased.targets = boundTargets
	preparedImages, err := a.prepareRunImages(ctx, sourceArtifact, leased.targets, program)
	if err != nil {
		return result, err
	}
	originalRelease := leased.release
	leased.release = func() { preparedImages.Release(); originalRelease() }
	if len(preparedImages.Overrides) > 0 {
		compiled, err = a.compiler.CompileDraft(ctx, compiler.CompileRequest{
			SourceJSON: sourceArtifact, Catalog: a.catalog, BlobVerifier: a.blobVerifier, ResourceOverrides: preparedImages.Overrides, ParameterValues: parameterValues,
		})
		result.Diagnostics = append([]schema.Diagnostic(nil), compiled.Diagnostics...)
		if err != nil || schema.HasErrors(compiled.Diagnostics) {
			return result, err
		}
		program, ok = compiled.Program()
		if !ok {
			return result, errors.New("compiler returned no Program without diagnostics")
		}
	}
	result.ProgramHash = program.Hash()

	admitted, err := a.admitRun(ctx, program, principal, selection, leased)
	result.Record = admitted.record
	releaseProviders := admitted.release
	if err != nil {
		return result, err
	}
	if a.prepareRunServices != nil {
		ids, prepareErr := a.prepareRunServices(ctx, program.ConfiguredTargetSlots(a.catalog), admitted.targets)
		if prepareErr != nil {
			result.Record, err = a.failRunPreparation(ctx, admitted.record, prepareErr)
			return result, errors.Join(prepareErr, err)
		}
		packageIDs = append(packageIDs, ids...)
	}
	runID := admitted.record.Admission().RunID
	var control *compiler.DebugController
	if debug {
		control, err = compiler.NewDebugController(compiler.DebugControllerOptions{
			StartPaused: true, Breakpoints: breakpoints,
			OnChanged: func(snapshot compiler.DebugSnapshot) {
				if a.onDebugEvent != nil {
					a.onDebugEvent(DebugEvent{RunID: runID, Snapshot: snapshot})
				}
			},
		})
		if err != nil {
			return result, err
		}
	}
	leasePublished = true
	a.enqueue(admitted.record, sourceWorkflowID, admitted.providers, admitted.targets, releaseProviders, control, packageIDs)
	return result, nil
}

func (a *Application) GetRun(runID string) (run.Record, error) { return a.runs.Load(runID) }

func (a *Application) ListRuns() ([]run.Record, error) { return a.runs.List() }

func (a *Application) GetRunTimelineSnapshot(ctx context.Context, runID string) (run.TimelinePage, error) {
	return a.runs.TimelineSnapshot(ctx, runID)
}

func (a *Application) GetRunTimelinePage(ctx context.Context, runID string, page, pageSize int) (run.TimelinePage, error) {
	return a.runs.TimelinePage(ctx, runID, page, pageSize)
}

func (a *Application) GetSource(workflowID string) (workflowstore.SourceSnapshot, error) {
	return a.sources.Load(workflowID)
}

func (a *Application) ListSources() []workflowstore.SourceSnapshot { return a.sources.List() }

func (a *Application) ListSourceRecoveries() []workflowstore.SourceRecovery {
	return a.sources.ListRecoveries()
}

func (a *Application) RepairSourceRecovery(ctx context.Context, recoveryID artifact.Digest, raw []byte) (workflowstore.SourceSnapshot, error) {
	return a.sources.RepairRecovery(ctx, recoveryID, raw)
}

func (a *Application) DeleteSourceRecovery(ctx context.Context, recoveryID artifact.Digest) error {
	return a.sources.DeleteRecovery(ctx, recoveryID)
}

func (a *Application) WithDurableBlobReferences(ctx context.Context, visit func([]blob.BlobRef) error) error {
	if ctx == nil || visit == nil {
		return errors.New("durable blob inventory requires context and visitor")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.requireRunning(); err != nil {
		return err
	}
	refs := make([]blob.BlobRef, 0)
	for _, listed := range a.sources.List() {
		snapshot, err := a.sources.Load(listed.WorkflowID())
		if err != nil {
			return err
		}
		document, diagnostics := schema.ParseSource(snapshot.Artifact())
		if len(diagnostics) != 0 {
			return errors.New("stored Workflow Source failed blob inventory reopen")
		}
		sourceRefs, err := schema.BlobReferences(document)
		if err != nil {
			return err
		}
		refs = append(refs, sourceRefs...)
	}
	programs, err := a.programs.List()
	if err != nil {
		return err
	}
	for _, program := range programs {
		programRefs, err := program.BlobReferences(a.catalog)
		if err != nil {
			return err
		}
		refs = append(refs, programRefs...)
	}
	runRefs, err := a.runs.BlobReferences(ctx)
	if err != nil {
		return err
	}
	refs = append(refs, runRefs...)
	return visit(uniqueBlobReferences(refs))
}

func uniqueBlobReferences(source []blob.BlobRef) []blob.BlobRef {
	seen := make(map[blob.BlobRef]struct{}, len(source))
	result := make([]blob.BlobRef, 0, len(source))
	for _, ref := range source {
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Digest == result[j].Digest {
			return result[i].MediaType < result[j].MediaType
		}
		return result[i].Digest < result[j].Digest
	})
	return result
}

func (a *Application) PublishImportedSource(ctx context.Context, raw []byte, baseRevision int64, expectedHash artifact.Digest) (workflowstore.SourceSnapshot, error) {
	if ctx == nil {
		return workflowstore.SourceSnapshot{}, errors.New("import Workflow Source context is required")
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.requireRunning(); err != nil {
		return workflowstore.SourceSnapshot{}, err
	}
	document, diagnostics := schema.ParseSource(raw)
	if len(diagnostics) != 0 {
		return workflowstore.SourceSnapshot{}, errors.New("imported Workflow Source is invalid")
	}
	if baseRevision == -1 {
		if expectedHash != "" || document.Revision != 0 {
			return workflowstore.SourceSnapshot{}, errors.New("new imported Workflow Source has invalid identity")
		}
		timestamp := formatWorkflowTimestamp(a.now())
		document.Workflow.CreatedAt = timestamp
		document.Workflow.UpdatedAt = timestamp
	} else if baseRevision >= 0 {
		if !expectedHash.Valid() || document.Revision != baseRevision+1 {
			return workflowstore.SourceSnapshot{}, errors.New("replacement import requires exact next revision")
		}
		current, err := a.sources.Load(document.Workflow.ID)
		if err != nil {
			return workflowstore.SourceSnapshot{}, err
		}
		if current.Revision() != baseRevision || current.Hash() != expectedHash {
			return workflowstore.SourceSnapshot{}, workflowstore.ErrSourceConflict
		}
		currentDocument, currentDiagnostics := schema.ParseSource(current.Artifact())
		if len(currentDiagnostics) != 0 {
			return workflowstore.SourceSnapshot{}, errors.New("stored Workflow Source failed strict reopen")
		}
		document.Workflow.CreatedAt = currentDocument.Workflow.CreatedAt
		document.Workflow.UpdatedAt = currentDocument.Workflow.UpdatedAt
		updatedDocument, _, err := stampWorkflowUpdate(document, a.now())
		if err != nil {
			return workflowstore.SourceSnapshot{}, err
		}
		document = updatedDocument
		if active := a.ActiveSourceRuns(document.Workflow.ID); len(active) != 0 {
			return workflowstore.SourceSnapshot{}, fmt.Errorf("workflow source has %d active run(s)", len(active))
		}
	} else {
		return workflowstore.SourceSnapshot{}, errors.New("imported Workflow Source base revision is invalid")
	}
	canonical, err := artifact.Marshal(document)
	if err != nil {
		return workflowstore.SourceSnapshot{}, err
	}
	return a.sources.Save(ctx, canonical, baseRevision)
}

// DeleteSource deletes only the exact revision/hash reviewed by the caller
// and refuses while an in-process Run still retains the Source identity.
func (a *Application) DeleteSource(ctx context.Context, workflowID string, revision int64, hash artifact.Digest) error {
	if ctx == nil {
		return errors.New("delete Workflow Source context is required")
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.requireRunning(); err != nil {
		return err
	}
	if active := a.ActiveSourceRuns(workflowID); len(active) != 0 {
		return fmt.Errorf("workflow source has %d active run(s)", len(active))
	}
	return a.sources.Delete(ctx, workflowID, revision, hash)
}

func (a *Application) CatalogArtifact() []byte { return a.catalog.Bytes() }

func (a *Application) AuthoringProjection() nodeauthoring.Snapshot { return a.authoring }

func (a *Application) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("application close context is required")
	}
	a.commandMu.Lock()
	if a.state == stateClosed {
		a.commandMu.Unlock()
		return nil
	}
	a.state = stateClosed
	a.commandMu.Unlock()
	return a.close(ctx)
}

// requireRunning is called only while commandMu is held.
func (a *Application) requireRunning() error {
	switch a.state {
	case stateRunning:
		return nil
	case stateClosed:
		return ErrClosed
	default:
		return ErrNotStarted
	}
}
