// Package workflowbundle owns portable Workflow Source archives.
package workflowbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/durablefs"
	"github.com/yottaapp/yotta/internal/panel"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

const (
	Format                = "yotta.workflow-bundle"
	Version               = 3
	LegacyVersion         = 1
	ManifestPath          = "yotta-workflow-bundle.json"
	SourcePath            = "workflow.json"
	Extension             = ".yotta-workflow"
	SourceTrustUnverified = "unverified"
	maxArchiveBytes       = int64(1 << 30)
	maxExpandedBytes      = int64(1 << 30)
	maxSourceBytes        = int64(16 << 20)
	maxManifestBytes      = int64(1 << 20)
	maxEvidenceBytes      = int64(4 << 20)
	maxEntries            = 4100
	privateFileMode       = 0o600
	archiveEncryptionFlag = 1 << 0
)

type EvidenceKind string

const (
	EvidencePublisherAttestation     EvidenceKind = "publisher-attestation"
	EvidencePlatformPublicationProof EvidenceKind = "platform-publication-proof"
)

var evidenceMediaTypePattern = regexp.MustCompile(`^application/[a-z0-9][a-z0-9!#$&^_.+-]{0,126}(?:\+[a-z0-9][a-z0-9!#$&^_.+-]{0,63})?$`)

type SourceRepository interface {
	GetSource(string) (workflowstore.SourceSnapshot, error)
	PublishImportedSource(context.Context, []byte, int64, artifact.Digest) (workflowstore.SourceSnapshot, error)
}

type BlobStore interface {
	PutRetained(context.Context, string, io.Reader) (blob.BlobRef, blob.Retention, error)
	WriteTo(context.Context, blob.BlobRef, io.Writer) error
	Verify(context.Context, blob.BlobRef) error
}

type Manager struct {
	panels  *panel.Service
	sources SourceRepository
	blobs   BlobStore
	newID   func() string
}

type Manifest struct {
	Panels       []panel.PortableResource       `json:"panels,omitempty"`
	Format       string                         `json:"format"`
	Version      int                            `json:"version"`
	SourceTrust  string                         `json:"sourceTrust,omitempty"`
	WorkflowID   string                         `json:"workflowId"`
	SourceHash   artifact.Digest                `json:"sourceHash"`
	Dependencies []schema.NodePackageDependency `json:"dependencies"`
	Blobs        []blob.BlobRef                 `json:"blobs"`
	Evidence     []EvidenceRef                  `json:"evidence"`
}

type EvidenceInput struct {
	Kind      EvidenceKind
	MediaType string
	Bytes     []byte
}

type EvidenceRef struct {
	Kind      EvidenceKind    `json:"kind"`
	Path      string          `json:"path"`
	MediaType string          `json:"mediaType"`
	Digest    artifact.Digest `json:"digest"`
	Size      int64           `json:"size"`
}

type Info struct {
	PublishedSourceHash        artifact.Digest          `json:"publishedSourceHash"`
	Panels                     []panel.PortableResource `json:"panels,omitempty"`
	WorkflowID                 string                   `json:"workflowId"`
	Name                       string                   `json:"name"`
	Revision                   int64                    `json:"revision"`
	SourceHash                 artifact.Digest          `json:"sourceHash"`
	ResourceCount              int                      `json:"resourceCount"`
	TargetProfileCount         int                      `json:"targetProfileCount"`
	CredentialRequirementCount int                      `json:"credentialRequirementCount"`
	DependencyCount            int                      `json:"dependencyCount"`
	BlobCount                  int                      `json:"blobCount"`
	BlobBytes                  int64                    `json:"blobBytes"`
	SourceTrust                string                   `json:"sourceTrust"`
	Evidence                   []EvidenceRef            `json:"evidence"`
}

type ExportResult struct {
	Info Info   `json:"info"`
	Path string `json:"path"`
}

type exportPayload struct {
	info     Info
	manifest []byte
	source   []byte
	refs     []blob.BlobRef
	evidence []EvidenceInput
}

type ImportMode string

const (
	ImportCopy     ImportMode = "copy"
	ImportReplace  ImportMode = "replace"
	ImportRegistry ImportMode = "registry"
)

type ImportRequest struct {
	Path               string          `json:"path"`
	Mode               ImportMode      `json:"mode"`
	TargetWorkflowID   string          `json:"targetWorkflowId,omitempty"`
	ExpectedRevision   int64           `json:"expectedRevision,omitempty"`
	ExpectedSourceHash artifact.Digest `json:"expectedSourceHash,omitempty"`
}

type ImportResult struct {
	Original Info                         `json:"original"`
	Source   workflowstore.SourceSnapshot `json:"-"`
}

func New(sources SourceRepository, blobs BlobStore, panels ...*panel.Service) (*Manager, error) {
	if sources == nil || blobs == nil {
		return nil, errors.New("workflow bundle manager requires source and blob stores")
	}
	m := &Manager{sources: sources, blobs: blobs, newID: uuid.NewString}
	if len(panels) > 0 {
		m.panels = panels[0]
	}
	return m, nil
}

func (m *Manager) Export(ctx context.Context, workflowID, destination string) (ExportResult, error) {
	return m.ExportWithEvidence(ctx, workflowID, destination, nil)
}

// ExportWithEvidence packages exact, opaque proof bytes without interpreting
// or trusting them. Inspect and Import continue to report the archive as
// unverified until a separate trust boundary verifies those bytes.
func (m *Manager) ExportWithEvidence(
	ctx context.Context,
	workflowID, destination string,
	evidence []EvidenceInput,
) (ExportResult, error) {
	if ctx == nil || strings.TrimSpace(workflowID) == "" || strings.TrimSpace(destination) == "" {
		return ExportResult{}, errors.New("workflow bundle export requires context, workflow ID, and destination")
	}
	payload, err := m.prepareExport(ctx, workflowID, evidence)
	if err != nil {
		return ExportResult{}, err
	}
	if err := m.writeArchive(ctx, destination, payload.manifest, payload.source, payload.refs, payload.evidence); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{Info: payload.info, Path: destination}, nil
}

// ExportBytes builds the same deterministic archive as Export without creating
// a user-visible or temporary destination file.
func (m *Manager) ExportBytes(ctx context.Context, workflowID string) (Info, []byte, error) {
	if ctx == nil || strings.TrimSpace(workflowID) == "" {
		return Info{}, nil, errors.New("workflow bundle export requires context and workflow ID")
	}
	payload, err := m.prepareExport(ctx, workflowID, nil)
	if err != nil {
		return Info{}, nil, err
	}
	var output bytes.Buffer
	if err := m.writeArchiveContents(ctx, &output, payload.manifest, payload.source, payload.refs, payload.evidence); err != nil {
		return Info{}, nil, err
	}
	return payload.info, output.Bytes(), nil
}

func (m *Manager) prepareExport(ctx context.Context, workflowID string, evidence []EvidenceInput) (exportPayload, error) {
	snapshot, err := m.sources.GetSource(workflowID)
	if err != nil {
		return exportPayload{}, err
	}
	document, canonical, digest, refs, err := inspectSource(snapshot.Artifact())
	if err != nil {
		return exportPayload{}, err
	}
	if digest != snapshot.Hash() || !bytes.Equal(canonical, snapshot.Artifact()) {
		return exportPayload{}, errors.New("stored Workflow Source identity changed before export")
	}
	for _, ref := range refs {
		if err := m.blobs.Verify(ctx, ref); err != nil {
			return exportPayload{}, fmt.Errorf("verify export blob %s: %w", ref.Digest, err)
		}
	}
	evidence, evidenceRefs, err := normalizeEvidence(evidence)
	if err != nil {
		return exportPayload{}, err
	}
	panels, err := m.collectPanels(document)
	if err != nil {
		return exportPayload{}, err
	}
	manifest := Manifest{
		Panels: panels,
		Format: Format, Version: Version, SourceTrust: SourceTrustUnverified,
		WorkflowID: workflowID, SourceHash: digest,
		Dependencies: append([]schema.NodePackageDependency{}, document.Dependencies...),
		Blobs:        refs, Evidence: evidenceRefs,
	}
	// Workflows without companion panels retain the existing portable encoding.
	if len(panels) == 0 {
		manifest.Version = 2
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return exportPayload{}, err
	}
	return exportPayload{
		info: sourceInfo(document, digest, refs, manifest), manifest: manifestRaw,
		source: canonical, refs: refs, evidence: evidence,
	}, nil
}

func (m *Manager) Inspect(ctx context.Context, archivePath string) (Info, error) {
	bundle, err := m.openArchive(ctx, archivePath)
	if err != nil {
		return Info{}, err
	}
	defer bundle.close()
	return bundle.info, nil
}

// InspectBytes validates a complete Workflow Bundle without requiring stores or
// application bootstrap. It is the internal authority used by public contract adapters.
func InspectBytes(ctx context.Context, raw []byte) (Info, Manifest, error) {
	if ctx == nil || len(raw) == 0 || int64(len(raw)) > maxArchiveBytes {
		return Info{}, Manifest{}, errors.New("workflow bundle bytes and context are required")
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return Info{}, Manifest{}, fmt.Errorf("open workflow bundle: %w", err)
	}
	fail := func(err error) (*openedBundle, error) { return nil, err }
	bundle, err := inspectArchive(ctx, reader, nil, fail)
	if err != nil {
		return Info{}, Manifest{}, err
	}
	return bundle.info, bundle.manifest, nil
}

func (m *Manager) Import(ctx context.Context, request ImportRequest) (ImportResult, error) {
	if ctx == nil {
		return ImportResult{}, errors.New("workflow bundle import requires context")
	}
	bundle, err := m.openArchive(ctx, request.Path)
	if err != nil {
		return ImportResult{}, err
	}
	defer bundle.close()
	document := bundle.document
	baseRevision := int64(-1)
	expectedHash := artifact.Digest("")
	switch request.Mode {
	case ImportRegistry:
		// An installation retains the published identity. Only explicit cloning rewrites it.
		document.Revision = 0
	case "", ImportCopy:
		document.Workflow.ID = m.newID()
		document.Revision = 0
	case ImportReplace:
		if strings.TrimSpace(request.TargetWorkflowID) == "" || request.ExpectedRevision < 0 || !request.ExpectedSourceHash.Valid() {
			return ImportResult{}, errors.New("replace import requires exact target revision and source hash")
		}
		document.Workflow.ID = request.TargetWorkflowID
		document.Revision = request.ExpectedRevision + 1
		baseRevision = request.ExpectedRevision
		expectedHash = request.ExpectedSourceHash
	default:
		return ImportResult{}, fmt.Errorf("unsupported workflow bundle import mode %q", request.Mode)
	}
	// Blob staging precedes durable panel and workflow writes.
	var canonical []byte
	serialize := func() error {
		raw, err := json.Marshal(document)
		if err != nil {
			return err
		}
		_, encoded, _, diagnostics, err := schema.CanonicalSource(raw)
		if err != nil {
			return err
		}
		if len(diagnostics) != 0 {
			return errors.New("rewritten Workflow Source is invalid")
		}
		canonical = encoded
		return nil
	}
	retentions := make([]blob.Retention, 0, len(bundle.manifest.Blobs))
	defer func() {
		for _, retention := range retentions {
			retention.Release()
		}
	}()
	for _, ref := range bundle.manifest.Blobs {
		entry := bundle.blobEntries[ref.Digest]
		reader, err := entry.Open()
		if err != nil {
			return ImportResult{}, err
		}
		stored, retention, putErr := m.blobs.PutRetained(ctx, ref.MediaType, reader)
		closeErr := reader.Close()
		if putErr != nil {
			return ImportResult{}, fmt.Errorf("import blob %s: %w", ref.Digest, putErr)
		}
		if closeErr != nil {
			return ImportResult{}, closeErr
		}
		if stored.Digest != ref.Digest || stored.Size != ref.Size {
			return ImportResult{}, fmt.Errorf("import blob %s changed identity", ref.Digest)
		}
		retentions = append(retentions, retention)
	}
	var published workflowstore.SourceSnapshot
	publish := func(mapping map[string]string) error {
		rewritePanels(&document, mapping)
		if err := serialize(); err != nil {
			return err
		}
		var err error
		published, err = m.sources.PublishImportedSource(ctx, canonical, baseRevision, expectedHash)
		if err != nil {
			if current, readErr := m.sources.GetSource(document.Workflow.ID); readErr == nil && bytes.Equal(current.Artifact(), canonical) {
				published = current
				return nil
			}
		}
		return err
	}
	if len(bundle.manifest.Panels) > 0 {
		if m.panels == nil {
			return ImportResult{}, panelProblem("portable_missing")
		}
		err = m.panels.ImportResources(ctx, document.Workflow.ID, bundle.manifest.Panels, publish)
	} else {
		err = publish(nil)
	}
	if err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Original: bundle.info, Source: published}, nil
}

type openedBundle struct {
	closeFunc   func() error
	manifest    Manifest
	document    schema.WorkflowSource
	info        Info
	blobEntries map[artifact.Digest]*zip.File
	evidence    map[EvidenceKind]*zip.File
}

func (b *openedBundle) close() {
	if b.closeFunc != nil {
		_ = b.closeFunc()
	}
}

func (m *Manager) openArchive(ctx context.Context, archivePath string) (*openedBundle, error) {
	if ctx == nil || strings.TrimSpace(archivePath) == "" {
		return nil, errors.New("workflow bundle path and context are required")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*openedBundle, error) {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArchiveBytes {
		return fail(errors.New("workflow bundle archive size is invalid"))
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return fail(fmt.Errorf("open workflow bundle: %w", err))
	}
	return inspectArchive(ctx, reader, file.Close, fail)
}

func inspectArchive(
	ctx context.Context,
	reader *zip.Reader,
	closeFunc func() error,
	fail func(error) (*openedBundle, error),
) (*openedBundle, error) {
	entries, err := indexEntries(reader.File)
	if err != nil {
		return fail(err)
	}
	manifestEntry, ok := entries[ManifestPath]
	if !ok {
		return fail(errors.New("workflow bundle manifest is missing"))
	}
	manifestRaw, err := readEntry(ctx, manifestEntry, maxManifestBytes)
	if err != nil {
		return fail(err)
	}
	var manifest Manifest
	if err := decodeStrict(manifestRaw, &manifest); err != nil {
		return fail(fmt.Errorf("decode workflow bundle manifest: %w", err))
	}
	if err := validateManifest(manifest); err != nil {
		return fail(err)
	}
	sourceEntry, ok := entries[SourcePath]
	if !ok {
		return fail(errors.New("workflow bundle Source is missing"))
	}
	sourceRaw, err := readEntry(ctx, sourceEntry, maxSourceBytes)
	if err != nil {
		return fail(err)
	}
	originalCanonical, originalDigest, err := schema.CanonicalSourceArtifact(sourceRaw)
	if err != nil || !bytes.Equal(sourceRaw, originalCanonical) || originalDigest != manifest.SourceHash {
		return fail(errors.New("workflow bundle Source identity does not match its manifest"))
	}
	migratedSource, _, err := workflowstore.MigrateSourceArtifact(sourceRaw)
	if err != nil {
		return fail(fmt.Errorf("migrate workflow bundle Source: %w", err))
	}
	document, _, digest, refs, err := inspectSource(migratedSource)
	if err != nil {
		return fail(err)
	}
	if err := validatePanelReferences(document, manifest.Panels); err != nil {
		return fail(err)
	}
	if document.Workflow.ID != manifest.WorkflowID ||
		!equalDependencies(document.Dependencies, manifest.Dependencies) || !equalRefs(refs, manifest.Blobs) {
		return fail(errors.New("workflow bundle Source identity does not match its manifest"))
	}
	blobEntries := make(map[artifact.Digest]*zip.File)
	for _, ref := range manifest.Blobs {
		entryPath := blobEntryPath(ref.Digest)
		entry, ok := entries[entryPath]
		if !ok {
			return fail(fmt.Errorf("workflow bundle blob %s is missing", ref.Digest))
		}
		if entry.UncompressedSize64 != uint64(ref.Size) {
			return fail(fmt.Errorf("workflow bundle blob %s size does not match", ref.Digest))
		}
		if existing := blobEntries[ref.Digest]; existing != nil && existing != entry {
			return fail(fmt.Errorf("workflow bundle blob %s has conflicting entries", ref.Digest))
		}
		blobEntries[ref.Digest] = entry
	}
	evidenceEntries := make(map[EvidenceKind]*zip.File, len(manifest.Evidence))
	for _, ref := range manifest.Evidence {
		entry, ok := entries[ref.Path]
		if !ok {
			return fail(fmt.Errorf("workflow bundle evidence %q is missing", ref.Kind))
		}
		if entry.UncompressedSize64 != uint64(ref.Size) {
			return fail(fmt.Errorf("workflow bundle evidence %q size does not match", ref.Kind))
		}
		if err := verifyEntry(ctx, entry, ref.Digest); err != nil {
			return fail(fmt.Errorf("verify workflow bundle evidence %q: %w", ref.Kind, err))
		}
		evidenceEntries[ref.Kind] = entry
	}
	if len(entries) != len(blobEntries)+len(evidenceEntries)+2 {
		return fail(errors.New("workflow bundle archive contains undeclared entries"))
	}
	for digest, entry := range blobEntries {
		if err := verifyEntry(ctx, entry, digest); err != nil {
			return fail(err)
		}
	}
	return &openedBundle{
		closeFunc: closeFunc, manifest: manifest, document: document,
		info:        sourceInfo(document, digest, refs, manifest),
		blobEntries: blobEntries, evidence: evidenceEntries,
	}, nil
}

func (m *Manager) writeArchive(
	ctx context.Context,
	destination string,
	manifest, source []byte,
	refs []blob.BlobRef,
	evidence []EvidenceInput,
) error {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	directory := filepath.Dir(destination)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return errors.New("workflow bundle destination directory does not exist")
	}
	if current, statErr := os.Lstat(destination); statErr == nil && (!current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0) {
		return errors.New("workflow bundle destination is not a regular file")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	temp, err := os.CreateTemp(directory, ".yotta-workflow-")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(privateFileMode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := m.writeArchiveContents(ctx, temp, manifest, source, refs, evidence); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := durablefs.Replace(tempPath, destination); err != nil {
		return fmt.Errorf("publish workflow bundle: %w", err)
	}
	return nil
}

func (m *Manager) writeArchiveContents(
	ctx context.Context,
	destination io.Writer,
	manifest, source []byte,
	refs []blob.BlobRef,
	evidence []EvidenceInput,
) error {
	archive := zip.NewWriter(destination)
	writeBytes := func(name string, raw []byte) error {
		writer, err := archive.CreateHeader(archiveHeader(name))
		if err != nil {
			return err
		}
		_, err = writer.Write(raw)
		return err
	}
	if err := writeBytes(ManifestPath, manifest); err != nil {
		_ = archive.Close()
		return err
	}
	if err := writeBytes(SourcePath, source); err != nil {
		_ = archive.Close()
		return err
	}
	written := make(map[artifact.Digest]struct{})
	for _, ref := range refs {
		if _, ok := written[ref.Digest]; ok {
			continue
		}
		writer, err := archive.CreateHeader(archiveHeader(blobEntryPath(ref.Digest)))
		if err != nil {
			_ = archive.Close()
			return err
		}
		if err := m.blobs.WriteTo(ctx, ref, writer); err != nil {
			_ = archive.Close()
			return err
		}
		written[ref.Digest] = struct{}{}
	}
	for _, item := range evidence {
		writer, err := archive.CreateHeader(archiveHeader(evidencePath(item.Kind)))
		if err != nil {
			_ = archive.Close()
			return err
		}
		if _, err := writer.Write(item.Bytes); err != nil {
			_ = archive.Close()
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return nil
}

func inspectSource(raw []byte) (schema.WorkflowSource, []byte, artifact.Digest, []blob.BlobRef, error) {
	document, canonical, digest, diagnostics, err := schema.CanonicalSource(raw)
	if err != nil {
		return schema.WorkflowSource{}, nil, "", nil, err
	}
	if len(diagnostics) != 0 {
		return schema.WorkflowSource{}, nil, "", nil, errors.New("workflow bundle Source is invalid")
	}
	refs, err := schema.BlobReferences(document)
	if err != nil {
		return schema.WorkflowSource{}, nil, "", nil, err
	}
	return document, canonical, digest, refs, nil
}

func sourceInfo(
	document schema.WorkflowSource,
	digest artifact.Digest,
	refs []blob.BlobRef,
	manifest Manifest,
) Info {
	var bytes int64
	seen := make(map[artifact.Digest]struct{})
	for _, ref := range refs {
		if _, ok := seen[ref.Digest]; ok {
			continue
		}
		seen[ref.Digest] = struct{}{}
		bytes += ref.Size
	}
	return Info{
		PublishedSourceHash: manifest.SourceHash,
		Panels:              manifest.Panels,
		WorkflowID:          document.Workflow.ID, Name: document.Workflow.Name, Revision: document.Revision, SourceHash: digest,
		ResourceCount: len(document.Resources), TargetProfileCount: len(document.TargetProfileDefinitions),
		CredentialRequirementCount: len(document.CredentialRequirements), DependencyCount: len(document.Dependencies),
		BlobCount: len(seen), BlobBytes: bytes,
		SourceTrust: sourceTrust(manifest), Evidence: append([]EvidenceRef(nil), manifest.Evidence...),
	}
}

func validateManifest(manifest Manifest) error {
	if len(manifest.Panels) > 128 || manifest.Version < 3 && len(manifest.Panels) > 0 {
		return panelProblem("invalid_definition")
	}
	for i, r := range manifest.Panels {
		if i > 0 && manifest.Panels[i-1].ID >= r.ID {
			return panelProblem("invalid_definition")
		}
		if err := panel.ValidatePortable(r); err != nil {
			return err
		}
	}
	if manifest.Format != Format || strings.TrimSpace(manifest.WorkflowID) == "" || !manifest.SourceHash.Valid() ||
		manifest.Dependencies == nil || len(manifest.Dependencies) > schema.MaxDependencies ||
		manifest.Blobs == nil || len(manifest.Blobs) > maxEntries-4 {
		return errors.New("workflow bundle manifest identity is invalid")
	}
	switch manifest.Version {
	case LegacyVersion:
		if manifest.SourceTrust != "" || len(manifest.Evidence) != 0 {
			return errors.New("legacy workflow bundle cannot declare release evidence")
		}
	case 2, Version:
		if manifest.SourceTrust != SourceTrustUnverified || manifest.Evidence == nil {
			return errors.New("workflow bundle must explicitly remain unverified before trust evaluation")
		}
	default:
		return errors.New("unsupported workflow bundle manifest version")
	}
	for i, ref := range manifest.Blobs {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("workflow bundle blob %d is invalid: %w", i, err)
		}
		if i > 0 {
			previous := manifest.Blobs[i-1]
			if previous.Digest > ref.Digest || previous.Digest == ref.Digest && previous.MediaType >= ref.MediaType {
				return errors.New("workflow bundle blob manifest is not uniquely sorted")
			}
		}
	}
	for index, ref := range manifest.Evidence {
		if err := validateEvidenceRef(ref); err != nil {
			return fmt.Errorf("workflow bundle evidence %d is invalid: %w", index, err)
		}
		if index > 0 && manifest.Evidence[index-1].Kind >= ref.Kind {
			return errors.New("workflow bundle evidence is not uniquely sorted")
		}
	}
	return nil
}

func normalizeEvidence(inputs []EvidenceInput) ([]EvidenceInput, []EvidenceRef, error) {
	normalized := append([]EvidenceInput(nil), inputs...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Kind < normalized[j].Kind })
	refs := make([]EvidenceRef, 0, len(normalized))
	for index, input := range normalized {
		if index > 0 && normalized[index-1].Kind == input.Kind {
			return nil, nil, errors.New("workflow bundle evidence kind is duplicated")
		}
		if len(input.Bytes) == 0 || int64(len(input.Bytes)) > maxEvidenceBytes ||
			!evidenceMediaTypePattern.MatchString(input.MediaType) {
			return nil, nil, errors.New("workflow bundle evidence input is invalid")
		}
		digest := rawDigest(input.Bytes)
		ref := EvidenceRef{
			Kind: input.Kind, Path: evidencePath(input.Kind), MediaType: input.MediaType,
			Digest: digest, Size: int64(len(input.Bytes)),
		}
		if err := validateEvidenceRef(ref); err != nil {
			return nil, nil, err
		}
		refs = append(refs, ref)
		normalized[index].Bytes = append([]byte(nil), input.Bytes...)
	}
	return normalized, refs, nil
}

func validateEvidenceRef(ref EvidenceRef) error {
	if ref.Kind != EvidencePublisherAttestation && ref.Kind != EvidencePlatformPublicationProof ||
		ref.Path != evidencePath(ref.Kind) || !evidenceMediaTypePattern.MatchString(ref.MediaType) ||
		!ref.Digest.Valid() || ref.Size <= 0 || ref.Size > maxEvidenceBytes {
		return errors.New("evidence reference is invalid")
	}
	return nil
}

func evidencePath(kind EvidenceKind) string {
	switch kind {
	case EvidencePublisherAttestation:
		return "evidence/publisher-attestation.json"
	case EvidencePlatformPublicationProof:
		return "evidence/platform-publication-proof.json"
	default:
		return ""
	}
}

func sourceTrust(manifest Manifest) string {
	if manifest.Version == LegacyVersion {
		return SourceTrustUnverified
	}
	return manifest.SourceTrust
}

func rawDigest(raw []byte) artifact.Digest {
	sum := sha256.Sum256(raw)
	return artifact.Digest("sha256:" + hex.EncodeToString(sum[:]))
}

func equalRefs(left, right []blob.BlobRef) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalDependencies(left, right []schema.NodePackageDependency) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].PublisherNamespace != right[index].PublisherNamespace ||
			left[index].PackageID != right[index].PackageID || left[index].PackageVersion != right[index].PackageVersion ||
			left[index].ManifestDigest != right[index].ManifestDigest || len(left[index].NodeRefs) != len(right[index].NodeRefs) {
			return false
		}
		for refIndex := range left[index].NodeRefs {
			if left[index].NodeRefs[refIndex] != right[index].NodeRefs[refIndex] {
				return false
			}
		}
	}
	return true
}

func indexEntries(files []*zip.File) (map[string]*zip.File, error) {
	if len(files) < 2 || len(files) > maxEntries {
		return nil, errors.New("workflow bundle entry count is invalid")
	}
	entries := make(map[string]*zip.File, len(files))
	var expanded uint64
	for _, entry := range files {
		name := entry.Name
		if name == "" || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || path.Clean(name) != name || strings.Contains(name, ":") || entry.FileInfo().IsDir() || entry.Mode()&os.ModeSymlink != 0 || entry.Flags&archiveEncryptionFlag != 0 {
			return nil, fmt.Errorf("workflow bundle contains unsafe entry %q", name)
		}
		if _, duplicate := entries[name]; duplicate {
			return nil, fmt.Errorf("workflow bundle contains duplicate entry %q", name)
		}
		if entry.UncompressedSize64 > uint64(maxExpandedBytes)-expanded {
			return nil, errors.New("workflow bundle exceeds expanded byte budget")
		}
		expanded += entry.UncompressedSize64
		entries[name] = entry
	}
	return entries, nil
}

func readEntry(ctx context.Context, entry *zip.File, maximum int64) ([]byte, error) {
	if entry.UncompressedSize64 > uint64(maximum) {
		return nil, fmt.Errorf("workflow bundle entry %q exceeds byte budget", entry.Name)
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	result, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: reader}, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(result)) > maximum || uint64(len(result)) != entry.UncompressedSize64 {
		return nil, fmt.Errorf("workflow bundle entry %q size is invalid", entry.Name)
	}
	return result, nil
}

func verifyEntry(ctx context.Context, entry *zip.File, expected artifact.Digest) error {
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	defer reader.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, &contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return err
	}
	if uint64(written) != entry.UncompressedSize64 || artifact.Digest("sha256:"+hex.EncodeToString(hash.Sum(nil))) != expected {
		return fmt.Errorf("workflow bundle blob %s failed integrity check", expected)
	}
	return nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON document contains trailing data")
	}
	return nil
}

func archiveHeader(name string) *zip.FileHeader {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(privateFileMode)
	header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	return header
}

func blobEntryPath(digest artifact.Digest) string {
	return "blobs/" + strings.TrimPrefix(digest.String(), "sha256:")
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(target []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(target)
}
