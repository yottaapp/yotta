// Package workflow exposes the Yotta application commands to Wails.
// It is a projection layer; execution and storage remain owned by Application.
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/yottaapp/yotta/internal/apperr"
	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/authoringcontext"
	"github.com/yottaapp/yotta/internal/communityclient"
	"github.com/yottaapp/yotta/internal/durablefs"
	"github.com/yottaapp/yotta/internal/nativeoidc"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/registryclient"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowbundle"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

type Service struct {
	wallet            *walletSession
	walletPageURL     string
	walletPageBrowser nativeoidc.Browser
	observation       *authoringcontext.Service
	community         *communityclient.Client
	application       *appcore.Application
	authoring         nodeauthoring.Snapshot
	bundles           *workflowbundle.Manager
	references        ReferenceResolver
	registry          RegistryClient
	account           *nativeoidc.Session
	registryStatePath string
	registryMu        sync.Mutex
}

type ReferenceResolver func(workflowID string) []SourceReference

// GetNodePackageDependencies supplies authoring metadata separately from the
// immutable Node Authoring Projection schema.
func (s *Service) GetNodePackageDependencies() []schema.NodePackageDependency {
	return s.application.NodePackageDependencies()
}

type Option func(*Service)

func WithAuthoringContext(observation *authoringcontext.Service) Option {
	return func(s *Service) { s.observation = observation }
}

// SetEditorContext publishes only the active editor identity and save state.
func (s *Service) SetEditorContext(workflowID, graphID string, dirty bool) {
	if s.observation != nil {
		s.observation.SetEditor(authoringcontext.Editor{WorkflowID: workflowID, GraphID: graphID, Dirty: dirty})
	}
}

func WithRegistryAccount(account *nativeoidc.Session) Option {
	return func(s *Service) { s.account = account }
}
func WithRegistryState(path string) Option { return func(s *Service) { s.registryStatePath = path } }

func WithReferenceResolver(resolver ReferenceResolver) Option {
	return func(service *Service) { service.references = resolver }
}

func WithBundleManager(manager *workflowbundle.Manager) Option {
	return func(service *Service) { service.bundles = manager }
}

type RegistryClient interface {
	PublishWorkflow(context.Context, registryclient.PublishRequest) (registryclient.WorkflowRelease, error)
	Search(context.Context, string, int) (registryclient.SearchPage, error)
	GetWorkflowRelease(context.Context, string) (registryclient.WorkflowRelease, error)
	CreateInstallPlan(context.Context, string, registryclient.Environment) (registryclient.InstallPlan, error)
	DownloadArtifact(context.Context, string) ([]byte, error)
}

func WithRegistryClient(client RegistryClient) Option {
	return func(service *Service) { service.registry = client }
}

func NewService(application *appcore.Application, options ...Option) (*Service, error) {
	if application == nil {
		return nil, errors.New("workflow service requires Application")
	}
	projection := application.AuthoringProjection()
	if !projection.Valid() {
		return nil, errors.New("workflow service requires trusted Authoring Projection")
	}
	service := &Service{application: application, authoring: projection}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

type SourceView struct {
	WorkflowID  string          `json:"workflowId"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Category    string          `json:"category,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	CreatedAt   string          `json:"createdAt,omitempty"`
	UpdatedAt   string          `json:"updatedAt,omitempty"`
	NodeCount   int             `json:"nodeCount"`
	Revision    int64           `json:"revision"`
	SourceHash  artifact.Digest `json:"sourceHash"`
	SourceJSON  string          `json:"sourceJson,omitempty"`
}

type SourceQuery struct {
	Search       string   `json:"search"`
	Category     string   `json:"category"`
	Tags         []string `json:"tags"`
	CreatedSince string   `json:"createdSince"`
	UpdatedSince string   `json:"updatedSince"`
	Sort         string   `json:"sort"`
	Page         int      `json:"page"`
	PageSize     int      `json:"pageSize"`
}

type FacetValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type SourcePage struct {
	Items      []SourceView `json:"items"`
	Total      int          `json:"total"`
	Page       int          `json:"page"`
	PageSize   int          `json:"pageSize"`
	Categories []FacetValue `json:"categories"`
	Tags       []FacetValue `json:"tags"`
}

type CreateSourceRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
}

type UpdateSourceMetadataRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
}

type BatchUpdateSourceMetadataRequest struct {
	WorkflowID   string   `json:"workflowId"`
	BaseRevision int64    `json:"baseRevision"`
	Category     string   `json:"category"`
	Tags         []string `json:"tags"`
}

type BatchUpdateSourceMetadataResult struct {
	WorkflowID string           `json:"workflowId"`
	Updated    bool             `json:"updated"`
	Problem    *apperr.Envelope `json:"problem,omitempty"`
}

type SourceRecoveryView struct {
	RecoveryID   artifact.Digest `json:"recoveryId"`
	OriginalName string          `json:"originalName"`
	Reason       string          `json:"reason"`
	SourceJSON   string          `json:"sourceJson"`
}

type SourceReference struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

type DeleteSourceRequest struct {
	WorkflowID string          `json:"workflowId"`
	Revision   int64           `json:"revision"`
	SourceHash artifact.Digest `json:"sourceHash"`
}

type DeleteSourcePreview struct {
	WorkflowID string            `json:"workflowId"`
	Name       string            `json:"name"`
	References []SourceReference `json:"references"`
}

type DeleteSourceResult struct {
	WorkflowID string            `json:"workflowId"`
	Deleted    bool              `json:"deleted"`
	References []SourceReference `json:"references"`
	Problem    *apperr.Envelope  `json:"problem,omitempty"`
}

type BundleInfoView struct {
	PublishedSourceHash artifact.Digest   `json:"publishedSourceHash"`
	Panels              []BundlePanelView `json:"panels"`
	WorkflowID          string            `json:"workflowId"`
	Name                string            `json:"name"`
	Revision            int64             `json:"revision"`
	SourceHash          artifact.Digest   `json:"sourceHash"`
	BlobCount           int               `json:"blobCount"`
	BlobBytes           int64             `json:"blobBytes"`
}

type BundlePanelView struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	ComponentCount int    `json:"componentCount"`
	Plugin         bool   `json:"plugin"`
}

func (s *Service) PreviewSourceBundle(ctx context.Context, workflowID string) (BundleInfoView, error) {
	if s.bundles == nil {
		return BundleInfoView{}, unavailable("bundle")
	}
	info, err := s.bundles.Preview(ctx, workflowID)
	if err != nil {
		return BundleInfoView{}, bundleError("preview", err)
	}
	return bundleInfoView(info), nil
}

type BundleExportResult struct {
	WorkflowID string           `json:"workflowId"`
	Path       string           `json:"path,omitempty"`
	Exported   bool             `json:"exported"`
	Problem    *apperr.Envelope `json:"problem,omitempty"`
}

type CompileView struct {
	SourceHash  artifact.Digest     `json:"sourceHash,omitempty"`
	ProgramHash artifact.Digest     `json:"programHash,omitempty"`
	Diagnostics []schema.Diagnostic `json:"diagnostics"`
}

type RunView struct {
	RunID          string          `json:"runId"`
	WorkflowID     string          `json:"workflowId,omitempty"`
	SourceHash     artifact.Digest `json:"sourceHash,omitempty"`
	SourceRevision int64           `json:"sourceRevision,omitempty"`
	Status         string          `json:"status"`
	Generation     uint64          `json:"generation"`
	RecordDigest   artifact.Digest `json:"recordDigest"`
	ProgramHash    artifact.Digest `json:"programHash"`
	QueuedAt       string          `json:"queuedAt"`
	Failure        *FailureView    `json:"failure,omitempty"`
	Timeline       []TimelineEntry `json:"timeline"`
	TimelinePage   int             `json:"timelinePage"`
	TimelinePages  int             `json:"timelinePages"`
	TimelineTotal  int             `json:"timelineTotal"`
}

type RunTimelineExportResult struct {
	Path    string `json:"path"`
	Entries int    `json:"entries"`
}

type FailureView struct {
	Code      string         `json:"code"`
	Params    map[string]any `json:"params,omitempty"`
	Category  string         `json:"category"`
	Retryable bool           `json:"retryable"`
	GraphID   string         `json:"graphId,omitempty"`
	NodeID    string         `json:"nodeId,omitempty"`
	Attempt   int            `json:"attempt,omitempty"`
}

type SummaryView struct {
	Code     string            `json:"code"`
	Counters map[string]int64  `json:"counters"`
	Facts    map[string]string `json:"facts,omitempty"`
}

type TimelineEntry struct {
	Sequence       uint64         `json:"sequence"`
	Kind           string         `json:"kind"`
	GraphPath      []string       `json:"graphPath"`
	NodeID         string         `json:"nodeId"`
	EffectID       string         `json:"effectId,omitempty"`
	Attempt        int            `json:"attempt"`
	Action         string         `json:"action,omitempty"`
	AttemptOutcome string         `json:"attemptOutcome,omitempty"`
	ActionOutcome  string         `json:"actionOutcome,omitempty"`
	OccurredAt     string         `json:"occurredAt"`
	ErrorCode      string         `json:"errorCode,omitempty"`
	ErrorParams    map[string]any `json:"errorParams,omitempty"`
	StatusCode     string         `json:"statusCode,omitempty"`
	StatusCategory string         `json:"statusCategory,omitempty"`
	Summary        SummaryView    `json:"summary"`
}

type StartRunView struct {
	SourceHash  artifact.Digest         `json:"sourceHash,omitempty"`
	ProgramHash artifact.Digest         `json:"programHash,omitempty"`
	Diagnostics []schema.Diagnostic     `json:"diagnostics"`
	Run         *RunView                `json:"run,omitempty"`
	Debug       *compiler.DebugSnapshot `json:"debug,omitempty"`
	Readiness   RunReadinessView        `json:"readiness"`
}

type RunReadinessView struct {
	State         string `json:"state"`
	Code          string `json:"code,omitempty"`
	GraphID       string `json:"graphId,omitempty"`
	NodeID        string `json:"nodeId,omitempty"`
	FromNodeID    string `json:"fromNodeId,omitempty"`
	FromPortID    string `json:"fromPortId,omitempty"`
	ToNodeID      string `json:"toNodeId,omitempty"`
	ToPortID      string `json:"toPortId,omitempty"`
	RequirementID string `json:"requirementId,omitempty"`
	Slot          string `json:"slot,omitempty"`
}

type PatchView struct {
	Source         SourceView                `json:"source"`
	GeneratedNodes []authoring.GeneratedNode `json:"generatedNodes"`
}

func (s *Service) ApplyPatch(workflowID string, baseRevision int64, commands []authoring.Command) (PatchView, error) {
	result, err := s.application.ApplyPatch(context.Background(), authoring.PatchRequest{
		WorkflowID: workflowID, BaseRevision: baseRevision, Commands: commands,
	})
	if err != nil {
		if errors.Is(err, workflowstore.ErrSourceConflict) {
			return PatchView{}, apperr.New("workflow.revision.conflict", map[string]any{"baseRevision": baseRevision})
		}
		return PatchView{}, sourceError("apply_patch", err)
	}
	view, err := sourceView(result.Source, true)
	if err != nil {
		return PatchView{}, sourceError("project", err)
	}
	return PatchView{Source: view, GeneratedNodes: result.GeneratedNodes}, nil
}

func (s *Service) CreateSource(name string) (SourceView, error) {
	snapshot, err := s.application.CreateSource(context.Background(), name)
	if err != nil {
		return SourceView{}, sourceError("create", err)
	}
	return sourceView(snapshot, true)
}

func (s *Service) CreateSourceWithMetadata(request CreateSourceRequest) (SourceView, error) {
	snapshot, err := s.application.CreateSourceWithMetadata(context.Background(), authoring.WorkflowMetadata{
		Name: request.Name, Description: request.Description, Category: request.Category, Tags: request.Tags,
	})
	if err != nil {
		return SourceView{}, sourceError("create", err)
	}
	return sourceView(snapshot, true)
}

func (s *Service) UpdateSourceMetadata(workflowID string, baseRevision int64, request UpdateSourceMetadataRequest) (SourceView, error) {
	patch, err := s.ApplyPatch(workflowID, baseRevision, []authoring.Command{{
		Kind: authoring.CommandUpdateWorkflowMetadata,
		UpdateWorkflowMetadata: &authoring.UpdateWorkflowMetadataCommand{
			Name: request.Name, Description: request.Description, Category: request.Category, Tags: request.Tags,
		},
	}})
	if err != nil {
		return SourceView{}, sourceError("update_metadata", err)
	}
	return patch.Source, nil
}

func (s *Service) GetSource(workflowID string) (SourceView, error) {
	snapshot, err := s.application.GetSource(workflowID)
	if err != nil {
		return SourceView{}, sourceError("get", err)
	}
	return sourceView(snapshot, true)
}

func (s *Service) ListSources() ([]SourceView, error) {
	snapshots := s.application.ListSources()
	result := make([]SourceView, 0, len(snapshots))
	for _, snapshot := range snapshots {
		view, err := sourceView(snapshot, false)
		if err != nil {
			return nil, sourceError("list", err)
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *Service) ListSourceRecoveries() []SourceRecoveryView {
	recoveries := s.application.ListSourceRecoveries()
	result := make([]SourceRecoveryView, 0, len(recoveries))
	for _, recovery := range recoveries {
		result = append(result, SourceRecoveryView{
			RecoveryID: recovery.ID, OriginalName: recovery.OriginalName,
			Reason: recovery.Reason, SourceJSON: string(recovery.Artifact()),
		})
	}
	return result
}

func (s *Service) RepairSourceRecovery(recoveryID artifact.Digest, sourceJSON string) (SourceView, error) {
	snapshot, err := s.application.RepairSourceRecovery(context.Background(), recoveryID, []byte(sourceJSON))
	if err != nil {
		return SourceView{}, sourceError("repair_recovery", err)
	}
	return sourceView(snapshot, false)
}

func (s *Service) DeleteSourceRecovery(recoveryID artifact.Digest) error {
	return sourceError("delete_recovery", s.application.DeleteSourceRecovery(context.Background(), recoveryID))
}

func (s *Service) InspectSourceBundle(archivePath string) (BundleInfoView, error) {
	if s.bundles == nil {
		return BundleInfoView{}, unavailable("bundle")
	}
	info, err := s.bundles.Inspect(context.Background(), archivePath)
	if err != nil {
		return BundleInfoView{}, bundleError("inspect", err)
	}
	return bundleInfoView(info), nil
}

func (s *Service) ImportSourceBundle(archivePath string) (SourceView, error) {
	if s.bundles == nil {
		return SourceView{}, unavailable("bundle")
	}
	result, err := s.bundles.Import(context.Background(), workflowbundle.ImportRequest{Path: archivePath, Mode: workflowbundle.ImportCopy})
	if err != nil {
		return SourceView{}, bundleError("import", err)
	}
	return sourceView(result.Source, false)
}

func (s *Service) ReplaceSourceFromBundle(archivePath, targetWorkflowID string, expectedRevision int64, expectedSourceHash artifact.Digest) (SourceView, error) {
	if s.bundles == nil {
		return SourceView{}, unavailable("bundle")
	}
	result, err := s.bundles.Import(context.Background(), workflowbundle.ImportRequest{
		Path: archivePath, Mode: workflowbundle.ImportReplace, TargetWorkflowID: targetWorkflowID,
		ExpectedRevision: expectedRevision, ExpectedSourceHash: expectedSourceHash,
	})
	if err != nil {
		return SourceView{}, bundleError("replace", err)
	}
	return sourceView(result.Source, false)
}

func (s *Service) ExportSourceBundle(workflowID, destination string) (BundleExportResult, error) {
	if s.bundles == nil {
		return BundleExportResult{}, unavailable("bundle")
	}
	result, err := s.bundles.Export(context.Background(), workflowID, destination)
	if err != nil {
		return BundleExportResult{}, bundleError("export", err)
	}
	return BundleExportResult{WorkflowID: workflowID, Path: result.Path, Exported: true}, nil
}

func (s *Service) CompileSource(workflowID string) (CompileView, error) {
	result, err := s.application.CompileSource(context.Background(), workflowID)
	return compileView(result), projectError("workflow.compile.failed", apperr.CategoryDomain, nil, false, err)
}

// CheckDraft validates the editor's current in-memory Source without saving it.
func (s *Service) CheckDraft(sourceJSON string) (CompileView, error) {
	result, err := s.application.CompileDraft(context.Background(), []byte(sourceJSON))
	return compileView(result), projectError("workflow.compile.failed", apperr.CategoryDomain, nil, false, err)
}

func compileView(result compiler.CompileResult) CompileView {
	view := CompileView{SourceHash: result.SourceHash, Diagnostics: append([]schema.Diagnostic(nil), result.Diagnostics...)}
	if program, ok := result.Program(); ok {
		view.ProgramHash = program.Hash()
	}
	return view
}

func (s *Service) PreviewRun(workflowID string) (appcore.RunPreview, error) {
	result, err := s.application.PreviewRun(context.Background(), workflowID)
	return result, runError("preview", err)
}

func (s *Service) StartRun(workflowID string) (StartRunView, error) {
	result, err := s.application.StartRun(context.Background(), appcore.StartRunRequest{WorkflowID: workflowID, Principal: "local-user"})
	view := StartRunView{
		SourceHash: result.SourceHash, ProgramHash: result.ProgramHash,
		Diagnostics: append([]schema.Diagnostic(nil), result.Diagnostics...),
		Readiness:   runReadinessView(result, err),
	}
	if result.Record.Valid() {
		run := runView(result.Record)
		view.Run = &run
	}
	if readinessErrorHandled(view.Readiness) {
		return view, nil
	}
	return view, runError("start", err)
}

func (s *Service) StartDebugRun(workflowID string, breakpoints []compiler.DebugBreakpoint) (StartRunView, error) {
	result, err := s.application.StartDebugRun(context.Background(), appcore.StartRunRequest{WorkflowID: workflowID, Principal: "local-user"}, breakpoints)
	view := StartRunView{
		SourceHash: result.SourceHash, ProgramHash: result.ProgramHash,
		Diagnostics: append([]schema.Diagnostic(nil), result.Diagnostics...),
		Readiness:   runReadinessView(result, err),
	}
	if result.Record.Valid() {
		run := runView(result.Record)
		view.Run = &run
		if snapshot, snapshotErr := s.application.GetDebugSnapshot(run.RunID); snapshotErr == nil {
			view.Debug = &snapshot
		}
	}
	if readinessErrorHandled(view.Readiness) {
		return view, nil
	}
	return view, runError("start_debug", err)
}

func runReadinessView(result appcore.StartRunResult, startErr error) RunReadinessView {
	readiness := appcore.ClassifyRunStart(result, startErr)
	return RunReadinessView{
		State: string(readiness.State), Code: readiness.Code, GraphID: readiness.GraphID,
		NodeID: readiness.NodeID, FromNodeID: readiness.FromNodeID, FromPortID: readiness.FromPortID,
		ToNodeID: readiness.ToNodeID, ToPortID: readiness.ToPortID,
		RequirementID: readiness.RequirementID, Slot: readiness.Slot,
	}
}

func readinessErrorHandled(readiness RunReadinessView) bool {
	switch readiness.State {
	case "workflow-invalid", "target-required", "credential-required", "environment-unavailable", "not-started":
		return true
	default:
		return false
	}
}

func (s *Service) GetDebugSnapshot(runID string) (compiler.DebugSnapshot, error) {
	result, err := s.application.GetDebugSnapshot(runID)
	return result, runError("debug_snapshot", err)
}

func (s *Service) ControlDebugRun(runID, action string) (compiler.DebugSnapshot, error) {
	result, err := s.application.ControlDebugRun(context.Background(), runID, appcore.DebugAction(action))
	return result, runError("debug_control", err)
}

func (s *Service) SetDebugBreakpoints(runID string, breakpoints []compiler.DebugBreakpoint) (compiler.DebugSnapshot, error) {
	result, err := s.application.SetDebugBreakpoints(context.Background(), runID, breakpoints)
	return result, runError("debug_breakpoints", err)
}

// GetActiveSourceRuns returns the queued/running Run identities for a bounded
// set of Workflow Sources so secondary windows can restore live state after
// mounting or being shown again.
func (s *Service) GetActiveSourceRuns(workflowIDs []string) (map[string][]string, error) {
	if len(workflowIDs) > 100 {
		return nil, projectError("workflow.run_query.invalid", apperr.CategoryValidation, map[string]any{"reason": "limit"}, false, errors.New("active source Run query exceeds 100 workflows"))
	}
	result := make(map[string][]string, len(workflowIDs))
	seen := make(map[string]struct{}, len(workflowIDs))
	for _, workflowID := range workflowIDs {
		workflowID = strings.TrimSpace(workflowID)
		if workflowID == "" {
			return nil, projectError("workflow.run_query.invalid", apperr.CategoryValidation, map[string]any{"reason": "empty_id"}, false, errors.New("active source Run query contains an empty workflow ID"))
		}
		if _, duplicate := seen[workflowID]; duplicate {
			continue
		}
		seen[workflowID] = struct{}{}
		if runIDs := s.application.ActiveSourceRuns(workflowID); len(runIDs) != 0 {
			result[workflowID] = runIDs
		}
	}
	return result, nil
}

func (s *Service) CancelRun(runID string) (RunView, error) {
	record, err := s.application.CancelRun(context.Background(), runID)
	if err != nil {
		return RunView{}, runError("cancel", err)
	}
	return runView(record), nil
}

func (s *Service) CancelAllRuns() error {
	return runError("cancel_all", s.application.CancelAll(context.Background()))
}

func (s *Service) GetRunTimeline(runID string) (RunView, error) {
	return s.GetRunTimelinePage(runID, 1, 200)
}

func (s *Service) GetRunTimelinePage(runID string, page, pageSize int) (RunView, error) {
	timeline, err := s.application.GetRunTimelinePage(context.Background(), runID, page, pageSize)
	if err != nil {
		return RunView{}, runError("timeline_page", err)
	}
	return runTimelinePageView(timeline), nil
}

func (s *Service) ExportRunTimeline(runID, destination string) (RunTimelineExportResult, error) {
	if strings.TrimSpace(destination) == "" {
		return RunTimelineExportResult{}, projectError("workflow.timeline.destination_required", apperr.CategoryValidation, nil, false, errors.New("run timeline export destination is required"))
	}
	snapshot, err := s.application.GetRunTimelineSnapshot(context.Background(), runID)
	if err != nil {
		return RunTimelineExportResult{}, runError("timeline_export", err)
	}
	view := runTimelinePageView(snapshot)
	document := struct {
		Format  string  `json:"format"`
		Version string  `json:"version"`
		Run     RunView `json:"run"`
	}{
		Format: "yotta.run-timeline", Version: "1", Run: view,
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return RunTimelineExportResult{}, projectError("workflow.timeline.export_failed", apperr.CategoryInfrastructure, nil, false, err)
	}
	raw = append(raw, '\n')
	if err := durablefs.WriteFile(destination, raw, 0o600); err != nil {
		return RunTimelineExportResult{}, projectError("workflow.timeline.export_failed", apperr.CategoryInfrastructure, nil, true, err)
	}
	return RunTimelineExportResult{Path: destination, Entries: len(view.Timeline)}, nil
}

func (s *Service) GetCatalog() string { return string(s.application.CatalogArtifact()) }

func (s *Service) GetAuthoringProjection() string { return string(s.authoring.Bytes()) }

func sourceView(snapshot workflowstore.SourceSnapshot, includeSource bool) (SourceView, error) {
	document, diagnostics := schema.ParseSource(snapshot.Artifact())
	if len(diagnostics) != 0 {
		return SourceView{}, errors.New("stored Workflow Source failed strict reopen")
	}
	view := SourceView{
		WorkflowID: snapshot.WorkflowID(), Name: document.Workflow.Name,
		Description: document.Workflow.Description, Category: document.Workflow.Category,
		Tags: append([]string(nil), document.Workflow.Tags...), CreatedAt: document.Workflow.CreatedAt,
		UpdatedAt: document.Workflow.UpdatedAt, NodeCount: sourceNodeCount(document),
		Revision: snapshot.Revision(), SourceHash: snapshot.Hash(),
	}
	if includeSource {
		view.SourceJSON = string(snapshot.Artifact())
	}
	return view, nil
}

func sourceNodeCount(source schema.WorkflowSource) int {
	total := 0
	for _, graph := range source.Graphs {
		total += len(graph.Nodes) + len(graph.Calls)
	}
	return total
}

func bundleInfoView(info workflowbundle.Info) BundleInfoView {
	panels := []BundlePanelView{}
	for _, p := range info.Panels {
		count := 0
		if p.Managed != nil {
			count = len(p.Managed.Components)
		} else {
			count = len(p.Plugin.Definition.Components)
		}
		panels = append(panels, BundlePanelView{ID: p.ID, Title: p.Title, ComponentCount: count, Plugin: p.Plugin != nil})
	}
	return BundleInfoView{
		Panels:     panels,
		WorkflowID: info.WorkflowID, Name: info.Name, Revision: info.Revision,
		PublishedSourceHash: info.PublishedSourceHash, SourceHash: info.SourceHash, BlobCount: info.BlobCount, BlobBytes: info.BlobBytes,
	}
}

func runView(record run.Record) RunView {
	return runViewPage(record, 1, 200)
}

func runViewPage(record run.Record, page, pageSize int) RunView {
	admission := record.Admission()
	entries := record.Journal()
	pageEntries, currentPage, pages := timelinePage(entries, page, pageSize)
	view := RunView{
		RunID: admission.RunID, WorkflowID: admission.WorkflowID, SourceHash: admission.SourceHash, SourceRevision: admission.SourceRevision,
		Status: string(record.Status()), Generation: record.Generation(), RecordDigest: record.Digest(),
		ProgramHash: admission.ProgramHash, QueuedAt: admission.QueuedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		Timeline: timelineView(pageEntries), TimelinePage: currentPage, TimelinePages: pages, TimelineTotal: int(record.JournalCount()),
	}
	if failure, ok := record.Failure(); ok {
		var params map[string]any
		if len(failure.Params) != 0 {
			_ = json.Unmarshal(failure.Params, &params)
		}
		view.Failure = &FailureView{
			Code: failure.Code, Params: params, Category: failure.Category, Retryable: failure.Retryable,
			GraphID: failure.GraphID, NodeID: failure.NodeID, Attempt: failure.Attempt,
		}
	}
	return view
}

func runTimelinePageView(timeline run.TimelinePage) RunView {
	summary := timeline.Summary
	admission := summary.Admission
	view := RunView{
		RunID: admission.RunID, WorkflowID: admission.WorkflowID, SourceHash: admission.SourceHash, SourceRevision: admission.SourceRevision,
		Status:     string(summary.Status),
		Generation: summary.Generation, RecordDigest: summary.Digest,
		ProgramHash: admission.ProgramHash,
		QueuedAt:    admission.QueuedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		Timeline:    timelineView(timeline.Entries), TimelinePage: timeline.Page,
		TimelinePages: timeline.Pages, TimelineTotal: timeline.Total,
	}
	if summary.Failure != nil {
		failure := summary.Failure
		var params map[string]any
		if len(failure.Params) != 0 {
			_ = json.Unmarshal(failure.Params, &params)
		}
		view.Failure = &FailureView{
			Code: failure.Code, Params: params, Category: failure.Category, Retryable: failure.Retryable,
			GraphID: failure.GraphID, NodeID: failure.NodeID, Attempt: failure.Attempt,
		}
	}
	return view
}

func timelinePage(entries []run.JournalEntry, page, pageSize int) ([]run.JournalEntry, int, int) {
	if pageSize <= 0 {
		pageSize = 200
	}
	if pageSize > 500 {
		pageSize = 500
	}
	pages := (len(entries) + pageSize - 1) / pageSize
	if pages == 0 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	end := len(entries) - (page-1)*pageSize
	if end < 0 {
		end = 0
	}
	start := end - pageSize
	if start < 0 {
		start = 0
	}
	return entries[start:end], page, pages
}

func timelineView(entries []run.JournalEntry) []TimelineEntry {
	result := make([]TimelineEntry, 0, len(entries))
	for _, entry := range entries {
		var errorParams map[string]any
		if len(entry.ErrorParams) != 0 {
			_ = json.Unmarshal(entry.ErrorParams, &errorParams)
		}
		result = append(result, TimelineEntry{
			Sequence: entry.Sequence, Kind: string(entry.Kind), GraphPath: append([]string(nil), entry.GraphPath...),
			NodeID: entry.NodeID, EffectID: entry.EffectID, Attempt: entry.Attempt, Action: entry.Action,
			AttemptOutcome: string(entry.AttemptOutcome), ActionOutcome: string(entry.ActionOutcome),
			OccurredAt: entry.OccurredAt.Format("2006-01-02T15:04:05.999999999Z07:00"), ErrorCode: entry.ErrorCode,
			ErrorParams: errorParams,
			StatusCode:  entry.StatusCode, StatusCategory: string(entry.StatusCategory),
			Summary: SummaryView{
				Code: entry.Summary.Code, Counters: entry.Summary.Counters, Facts: entry.Summary.Facts,
			},
		})
	}
	return result
}
