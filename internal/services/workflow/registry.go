package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/yottaapp/yotta/internal/workflowstore"
	"os"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/nativeoidc"
	"github.com/yottaapp/yotta/internal/registryclient"
	"github.com/yottaapp/yotta/internal/workflowbundle"
	"github.com/yottaapp/yotta/pkg/version"
)

type RegistryCreatorView struct {
	QualityAuthor bool   `json:"qualityAuthor,omitempty"`
	Picture       string `json:"picture,omitempty"`
	UserKey       string `json:"userKey"`
	DisplayName   string `json:"displayName,omitempty"`
}

func (s *Service) RegistryCommerce(ctx context.Context, workflowID string) (registryclient.CommerceView, error) {
	client, ok := s.registry.(interface {
		Commerce(context.Context, string) (registryclient.CommerceView, error)
	})
	if !ok {
		return registryclient.CommerceView{}, unavailable("registry")
	}
	view, err := client.Commerce(ctx, workflowID)
	if err != nil {
		return registryclient.CommerceView{}, registryError("commerce", err)
	}
	return view, nil
}

type RegistryWorkflowReleaseView struct {
	Sales              *registryclient.WorkflowSales      `json:"sales,omitempty"`
	PublicationStatus  string                             `json:"publicationStatus,omitempty"`
	SubmissionID       string                             `json:"submissionId,omitempty"`
	Official           bool                               `json:"official"`
	Recommended        bool                               `json:"recommended"`
	DownloadCount      int64                              `json:"downloadCount"`
	Dependencies       []registryclient.DependencySummary `json:"dependencies"`
	Listing            registryclient.Listing             `json:"listing"`
	Facts              registryclient.BundleFacts         `json:"facts"`
	ReleaseID          string                             `json:"releaseId"`
	PublisherNamespace string                             `json:"publisherNamespace"`
	WorkflowID         string                             `json:"workflowId"`
	ReleaseVersion     string                             `json:"releaseVersion"`
	SourceHash         string                             `json:"sourceHash"`
	BundleDigest       string                             `json:"bundleDigest"`
	Title              string                             `json:"title"`
	Summary            string                             `json:"summary"`
	ReleaseNotes       string                             `json:"releaseNotes,omitempty"`
	Examples           []PublishRegistryExample           `json:"examples"`
	Creator            RegistryCreatorView                `json:"creator"`
	Availability       string                             `json:"availability"`
	PublishedAt        string                             `json:"publishedAt"`
}

type RegistrySearchPageView struct {
	Facets     registryclient.Facets         `json:"facets"`
	Items      []RegistryWorkflowReleaseView `json:"items"`
	NextCursor string                        `json:"nextCursor,omitempty"`
}

type PublishRegistryExample struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type PublishRegistryRequest struct {
	ResubmissionID string                        `json:"resubmissionId,omitempty"`
	Sales          *registryclient.WorkflowSales `json:"sales,omitempty"`
	Listing        registryclient.Listing        `json:"listing"`
	WorkflowID     string                        `json:"workflowId"`
	ReleaseVersion string                        `json:"releaseVersion"`
	Title          string                        `json:"title"`
	Summary        string                        `json:"summary"`
	ReleaseNotes   string                        `json:"releaseNotes"`
	Examples       []PublishRegistryExample      `json:"examples"`
}

func (s *Service) PublishSourceToRegistry(
	ctx context.Context, request PublishRegistryRequest,
) (RegistryWorkflowReleaseView, error) {
	if sales := request.Sales; sales != nil && (sales.PriceCents < 0 || sales.PriceCents > 2147483647 || sales.Currency != "CNY" || sales.Revision < 0) {
		return RegistryWorkflowReleaseView{}, projectError("workflow.registry.invalid_sales", apperr.CategoryValidation, nil, false, errors.New("invalid publication sales"))
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(request.Title)); n < 1 || n > 160 {
		return RegistryWorkflowReleaseView{}, projectError("workflow.registry.invalid_title", apperr.CategoryValidation, nil, false, errors.New("title length outside 1..160"))
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(request.Summary)); n < 1 || n > 1000 {
		return RegistryWorkflowReleaseView{}, projectError("workflow.registry.invalid_summary", apperr.CategoryValidation, nil, false, errors.New("summary length outside 1..1000"))
	}
	if !registryclient.ValidWorkflowReleaseVersion(request.ReleaseVersion) {
		return RegistryWorkflowReleaseView{}, projectError("workflow.registry.invalid_version", apperr.CategoryValidation, nil, false, errors.New("workflow version requires three non-negative integers"))
	}
	if s.bundles == nil || s.registry == nil {
		return RegistryWorkflowReleaseView{}, unavailable("registry")
	}
	info, bundle, err := s.bundles.ExportBytes(ctx, request.WorkflowID)
	if err != nil {
		return RegistryWorkflowReleaseView{}, bundleError("publish", err)
	}
	examples := make([]registryclient.Example, 0, len(request.Examples))
	for _, example := range request.Examples {
		examples = append(examples, registryclient.Example{Title: example.Title, Description: example.Description})
	}
	release, err := s.registry.PublishWorkflow(ctx, registryclient.PublishRequest{
		Sales:   request.Sales,
		Listing: request.Listing,
		Bundle:  bytes.NewReader(bundle), Filename: request.WorkflowID + ".yotta-workflow",
		IdempotencyKey: publicationKey(request, string(info.SourceHash)),
		ReleaseVersion: request.ReleaseVersion, Title: request.Title, Summary: request.Summary,
		ReleaseNotes: request.ReleaseNotes, Examples: examples,
	})
	if err != nil {
		return RegistryWorkflowReleaseView{}, registryError("publish", err)
	}
	return registryReleaseView(release), nil
}

func publicationKey(request PublishRegistryRequest, sourceHash string) string {
	raw, _ := json.Marshal(request)
	digest := sha256.Sum256(append(raw, []byte("\x00"+sourceHash)...))
	return hex.EncodeToString(digest[:])
}

func (s *Service) SearchRegistry(ctx context.Context, search string, limit int) (RegistrySearchPageView, error) {
	if s.registry == nil {
		return RegistrySearchPageView{}, unavailable("registry")
	}
	page, err := s.registry.Search(ctx, search, limit)
	if err != nil {
		return RegistrySearchPageView{}, registryError("search", err)
	}
	view := RegistrySearchPageView{NextCursor: page.NextCursor, Items: make([]RegistryWorkflowReleaseView, 0, len(page.Items))}
	for _, item := range page.Items {
		if item.Kind == "workflow" {
			view.Items = append(view.Items, registryReleaseView(item.Workflow))
		}
	}
	return view, nil
}

func (s *Service) InstallRegistryWorkflow(ctx context.Context, releaseID string) (SourceView, error) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if s.bundles == nil || s.registry == nil {
		return SourceView{}, unavailable("registry")
	}
	plan, err := s.registry.CreateInstallPlan(ctx, releaseID, registryclient.Environment{
		YottaVersion: registryclient.NormalizeEnvironmentVersion(version.Version), OperatingSystem: runtime.GOOS, Architecture: runtime.GOARCH,
	})
	if err != nil {
		return SourceView{}, registryError("plan", err)
	}
	if len(plan.IncompatibleRequirements) != 0 {
		return SourceView{}, projectError("workflow.registry.incompatible", apperr.CategoryDomain, nil, false, errors.New("registry install plan is incompatible"))
	}
	records, err := s.readRegistryInstallations()
	if err != nil {
		return SourceView{}, registryError("installed", err)
	}
	release := plan.ResolvedWorkflowRelease
	request := workflowbundle.ImportRequest{Mode: workflowbundle.ImportRegistry}
	current, currentErr := s.application.GetSource(release.WorkflowID)
	if currentErr == nil {
		if record, tracked := records[release.WorkflowID]; tracked && !registryclient.IsNewerWorkflowVersion(release.ReleaseVersion, record.ReleaseVersion) {
			return sourceView(current, false)
		}
		if string(current.Hash()) == release.SourceHash {
			records[release.WorkflowID] = RegistryInstallation{WorkflowID: release.WorkflowID, ReleaseID: releaseID, ReleaseVersion: release.ReleaseVersion, SourceHash: string(current.Hash())}
			if err := s.saveRegistryInstallations(records); err != nil {
				return SourceView{}, registryError("record_install", err)
			}
			return sourceView(current, false)
		}
		record, tracked := records[release.WorkflowID]
		if !tracked || record.SourceHash != string(current.Hash()) {
			return SourceView{}, localRegistryChanges()
		}
		request.Mode, request.TargetWorkflowID = workflowbundle.ImportReplace, release.WorkflowID
		request.ExpectedRevision, request.ExpectedSourceHash = current.Revision(), current.Hash()
	} else if !errors.Is(currentErr, workflowstore.ErrSourceNotFound) {
		return SourceView{}, sourceError("install", currentErr)
	}
	var bundle []byte
	if downloader, ok := s.registry.(interface {
		DownloadWorkflow(context.Context, string, string) ([]byte, error)
	}); ok {
		bundle, err = downloader.DownloadWorkflow(ctx, releaseID, release.BundleDigest)
	} else {
		bundle, err = s.registry.DownloadArtifact(ctx, release.BundleDigest)
	}
	if err != nil {
		return SourceView{}, registryError("download", err)
	}
	temporary, err := os.CreateTemp("", ".yotta-registry-*.yotta-workflow")
	if err != nil {
		return SourceView{}, projectError("workflow.registry.install_failed", apperr.CategoryInfrastructure, nil, true, err)
	}
	path := temporary.Name()
	defer os.Remove(path)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(bundle)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return SourceView{}, projectError("workflow.registry.install_failed", apperr.CategoryInfrastructure, nil, true, err)
	}
	info, err := s.bundles.Inspect(ctx, path)
	if err != nil {
		return SourceView{}, bundleError("registry_inspect", err)
	}
	if info.WorkflowID != release.WorkflowID || string(info.PublishedSourceHash) != release.SourceHash {
		return SourceView{}, registryError("identity", errors.New("release and bundle identity differ"))
	}
	request.Path = path
	result, err := s.bundles.Import(ctx, request)
	if err != nil {
		return SourceView{}, bundleError("registry_import", err)
	}
	records[release.WorkflowID] = RegistryInstallation{WorkflowID: release.WorkflowID, ReleaseID: releaseID, ReleaseVersion: release.ReleaseVersion, SourceHash: string(result.Source.Hash())}
	if err := s.saveRegistryInstallations(records); err != nil {
		return SourceView{}, registryError("record_install", err)
	}
	return sourceView(result.Source, false)
}

func registryReleaseView(release registryclient.WorkflowRelease) RegistryWorkflowReleaseView {
	examples := make([]PublishRegistryExample, 0, len(release.Examples))
	for _, example := range release.Examples {
		examples = append(examples, PublishRegistryExample{Title: example.Title, Description: example.Description})
	}
	return RegistryWorkflowReleaseView{
		Sales: release.Sales, PublicationStatus: release.PublicationStatus, SubmissionID: release.SubmissionID,
		DownloadCount: release.DownloadCount,
		Official:      release.Official, Recommended: release.Recommended,
		Dependencies: release.Dependencies,
		Listing:      release.Listing, Facts: release.Facts,
		ReleaseID: release.ReleaseID, PublisherNamespace: release.PublisherNamespace,
		WorkflowID: release.WorkflowID, ReleaseVersion: release.ReleaseVersion,
		SourceHash: release.SourceHash, BundleDigest: release.BundleDigest,
		Title: release.Title, Summary: release.Summary, ReleaseNotes: release.ReleaseNotes,
		Examples:     examples,
		Creator:      RegistryCreatorView{QualityAuthor: release.Creator.QualityAuthor, UserKey: release.Creator.UserKey, DisplayName: release.Creator.DisplayName, Picture: release.Creator.Picture},
		Availability: release.Availability, PublishedAt: release.PublishedAt,
	}
}

func registryError(operation string, cause error) error {
	if errors.Is(cause, registryclient.ErrAuthenticationRequired) || errors.Is(cause, nativeoidc.ErrAuthenticationRequired) {
		return projectError("workflow.registry.authentication_required", apperr.CategoryPolicy, nil, false, cause)
	}
	if errors.Is(cause, context.Canceled) {
		return projectError("workflow.registry.cancelled", apperr.CategoryInfrastructure, map[string]any{"operation": operation}, false, cause)
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return projectError("workflow.registry.timeout", apperr.CategoryInfrastructure, map[string]any{"operation": operation}, true, cause)
	}
	var problem registryclient.Problem
	if errors.As(cause, &problem) {
		switch problem.Code {
		case "registry.submission_pending":
			return projectError("workflow.registry.submission_pending", apperr.CategoryDomain, nil, false, cause)
		case "registry.idempotency_conflict":
			return projectError("workflow.registry.idempotency_conflict", apperr.CategoryDomain, nil, false, cause)
		case "registry.review_required":
			return projectError("workflow.registry.review_required", apperr.CategoryDomain, nil, false, cause)
		case "registry.management.invalid":
			return projectError("workflow.registry.invalid_sales", apperr.CategoryValidation, nil, false, cause)
		case "registry.management.conflict":
			return projectError("workflow.registry.sales_changed", apperr.CategoryDomain, nil, true, cause)
		case "commerce.payment_cancel_unsupported":
			return projectError("workflow.checkout.cancel_unsupported", apperr.CategoryPolicy, nil, false, cause)
		case "commerce.payment_session_expired":
			return projectError("workflow.checkout.session_expired", apperr.CategoryDomain, nil, true, cause)
		case "commerce.order_invalid_state":
			return projectError("workflow.checkout.order_changed", apperr.CategoryDomain, nil, true, cause)
		case "registry.purchase_required":
			return projectError("workflow.registry.purchase_required", apperr.CategoryPolicy, nil, false, cause)
		case "registry.submission.daily_limit":
			return projectError("workflow.registry.submission_daily_limit", apperr.CategoryPolicy, nil, false, cause)
		case "registry.version_not_increasing":
			return projectError("workflow.registry.version_not_increasing", apperr.CategoryValidation, nil, false, cause)
		case "registry.invalid_title":
			return projectError("workflow.registry.invalid_title", apperr.CategoryValidation, nil, false, cause)
		case "registry.invalid_summary":
			return projectError("workflow.registry.invalid_summary", apperr.CategoryValidation, nil, false, cause)
		case "registry.invalid_presentation":
			return projectError("workflow.registry.invalid_presentation", apperr.CategoryValidation, nil, false, cause)
		case "registry.bundle_invalid":
			return projectError("workflow.registry.bundle_invalid", apperr.CategoryDomain, nil, false, cause)
		case "registry.invalid_listing":
			return projectError("workflow.registry.invalid_listing", apperr.CategoryValidation, nil, false, cause)
		case "registry.invalid_release_version":
			return projectError("workflow.registry.invalid_version", apperr.CategoryValidation, nil, false, cause)
		case "registry.workflow_not_owner":
			return projectError("workflow.registry.not_owner", apperr.CategoryPolicy, nil, false, cause)
		case "registry.release_version_conflict":
			return projectError("workflow.registry.release_version_conflict", apperr.CategoryDomain, nil, false, cause)
		case "registry.authentication_required":
			return projectError("workflow.registry.authentication_required", apperr.CategoryPolicy, nil, false, cause)
		case "registry.workflow_rejected", "registry.invalid_publication", "registry.bundle_required":
			return projectError("workflow.registry.workflow_rejected", apperr.CategoryDomain, nil, false, cause)
		case "registry.bundle_too_large":
			return projectError("workflow.registry.bundle_too_large", apperr.CategoryValidation, nil, false, cause)
		case "registry.invalid_search":
			return projectError("workflow.registry.invalid_search", apperr.CategoryValidation, nil, false, cause)
		}
	}
	return projectError("workflow.registry.unavailable", apperr.CategoryInfrastructure, map[string]any{"operation": operation}, true, cause)
}
