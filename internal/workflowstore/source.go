// Package workflowstore owns the durable Workflow Source command boundary and
// the derived Program cache consumed by the application.
package workflowstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

const SourceLayoutVersion = "catalog-3"

var (
	ErrSourceNotFound = errors.New("workflow source not found")
	ErrSourceConflict = errors.New("workflow source revision conflict")
	ErrSourceChanged  = errors.New("workflow source changed outside store")
)

type InvalidSourceError struct{ Diagnostics []schema.Diagnostic }

func (e *InvalidSourceError) Error() string { return "workflow source is invalid" }

// Parameters must be the durable local override store when opening legacy
// sources with machine targets. Bundle migration never uses this option.
type SourceStoreOptions struct {
	MigrationHistoryRoot string
	Parameters           *ParameterStore
	MaxSources           int
	Now                  func() time.Time
}

type SourceRecovery struct {
	ID           artifact.Digest
	OriginalName string
	Reason       string
	raw          []byte
}

func (r SourceRecovery) Artifact() []byte { return append([]byte(nil), r.raw...) }

type SourceSnapshot struct {
	workflowID string
	revision   int64
	hash       artifact.Digest
	raw        []byte
}

func (s SourceSnapshot) Valid() bool {
	return s.workflowID != "" && s.revision >= 0 && s.hash.Valid() && len(s.raw) != 0
}
func (s SourceSnapshot) WorkflowID() string    { return s.workflowID }
func (s SourceSnapshot) Revision() int64       { return s.revision }
func (s SourceSnapshot) Hash() artifact.Digest { return s.hash }
func (s SourceSnapshot) Artifact() []byte      { return append([]byte(nil), s.raw...) }

type SourceStore struct {
	mu         sync.RWMutex
	repository *catalog.WorkflowRepository
	max        int
	now        func() time.Time
	sources    map[string]SourceSnapshot
	recoveries map[artifact.Digest]SourceRecovery
}

func OpenSourceStore(repository *catalog.WorkflowRepository, options SourceStoreOptions) (*SourceStore, error) {
	plan, err := currentSourceMigrationPlan()
	if err != nil {
		return nil, fmt.Errorf("open Workflow Source migration registry: %w", err)
	}
	return openSourceStore(repository, options, plan)
}

func openSourceStore(
	repository *catalog.WorkflowRepository,
	options SourceStoreOptions,
	plan sourceMigrationPlan,
) (*SourceStore, error) {
	if repository == nil || options.MaxSources <= 0 {
		return nil, errors.New("workflow source store requires a Catalog repository and positive source limit")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	ctx := context.Background()
	records, err := repository.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("open Workflow Source Repository: %w", err)
	}
	if len(records) > options.MaxSources {
		return nil, errors.New("workflow source repository exceeds source limit")
	}
	type preparedSource struct {
		original  catalog.WorkflowSourceRecord
		candidate sourceCandidate
		migrated  bool
		bindings  map[string]string
	}
	prepared := make([]preparedSource, 0, len(records))
	for _, record := range records {
		contract, contractErr := sourceContractOf(record.Artifact)
		if contractErr != nil || contract.Format != record.Format || contract.Version != record.Version {
			return nil, fmt.Errorf("%w: Catalog record %q is inconsistent", ErrSourceChanged, record.WorkflowID)
		}
		migratedRaw, migrated, migrationErr := plan.Migrate(record.Artifact)
		if migrationErr != nil {
			return nil, fmt.Errorf("migrate Workflow Source %q: %w", record.WorkflowID, migrationErr)
		}
		candidate, inspectErr := inspectSource(migratedRaw, !migrated)
		if inspectErr != nil || candidate.snapshot.workflowID != record.WorkflowID ||
			candidate.snapshot.revision != record.Revision || candidate.name != record.Name {
			return nil, fmt.Errorf("%w: Catalog record %q is inconsistent after migration", ErrSourceChanged, record.WorkflowID)
		}
		if !migrated && (candidate.snapshot.hash != record.Hash ||
			candidate.format != record.Format || candidate.version != record.Version) {
			return nil, fmt.Errorf("%w: Catalog record %q is inconsistent", ErrSourceChanged, record.WorkflowID)
		}
		var bindings map[string]string
		if migrated {
			bindings, err = legacyTargetBindings(record.Artifact)
			if err != nil {
				return nil, fmt.Errorf("read legacy target bindings %q: %w", record.WorkflowID, err)
			}
			if len(bindings) > 0 && options.Parameters == nil {
				return nil, errors.New("workflow target migration requires the local parameter store")
			}
		}
		prepared = append(prepared, preparedSource{original: record, candidate: candidate, migrated: migrated, bindings: bindings})
	}
	// Every source has passed migration and current-schema validation before
	// publication begins. Each Catalog replacement is an exact hash CAS; a
	// crash between records is safely resumed on the next open.
	for _, source := range prepared {
		if !source.migrated {
			continue
		}
		// Seed before publishing: interruption leaves the old source retryable.
		// Existing values (including explicit empty bindings) always win on retry.
		if len(source.bindings) > 0 {
			values, err := options.Parameters.Load(source.original.WorkflowID)
			if err != nil {
				return nil, fmt.Errorf("load target migration bindings %q: %w", source.original.WorkflowID, err)
			}
			for id, slot := range source.bindings {
				key := schema.TargetParameterPrefix + id
				if _, exists := values[key]; !exists {
					values[key], _ = json.Marshal(slot)
				}
			}
			if err := options.Parameters.Save(source.original.WorkflowID, values); err != nil {
				return nil, fmt.Errorf("save target migration bindings %q: %w", source.original.WorkflowID, err)
			}
		}
		if err := recordSourceMigration(options.MigrationHistoryRoot, source.original.WorkflowID, source.original.Hash, source.candidate.snapshot.hash); err != nil {
			return nil, fmt.Errorf("record Workflow Source migration: %w", err)
		}
		record := source.candidate.record(options.Now().UTC())
		if err := repository.PublishMigration(ctx, source.original.Hash, record, source.candidate.references()); err != nil {
			return nil, fmt.Errorf("publish Workflow Source migration %q: %w", source.original.WorkflowID, sourceRepositoryError(err))
		}
	}
	sources := make(map[string]SourceSnapshot, len(prepared))
	for _, source := range prepared {
		sources[source.candidate.snapshot.workflowID] = source.candidate.snapshot
	}
	quarantine, err := repository.ListQuarantine(ctx)
	if err != nil {
		return nil, fmt.Errorf("open Workflow Source quarantine: %w", err)
	}
	recoveries := make(map[artifact.Digest]SourceRecovery, len(quarantine))
	for _, record := range quarantine {
		recoveries[record.ID] = SourceRecovery{
			ID: record.ID, OriginalName: record.OriginalName,
			Reason: record.Reason, raw: append([]byte(nil), record.Artifact...),
		}
	}
	return &SourceStore{
		repository: repository, max: options.MaxSources, now: options.Now,
		sources: sources, recoveries: recoveries,
	}, nil
}

func (s *SourceStore) ListRecoveries() []SourceRecovery {
	s.mu.RLock()
	result := make([]SourceRecovery, 0, len(s.recoveries))
	for _, recovery := range s.recoveries {
		recovery.raw = append([]byte(nil), recovery.raw...)
		result = append(result, recovery)
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s *SourceStore) RepairRecovery(ctx context.Context, recoveryID artifact.Digest, raw []byte) (SourceSnapshot, error) {
	if ctx == nil || !recoveryID.Valid() {
		return SourceSnapshot{}, errors.New("workflow source recovery requires context and recovery identity")
	}
	if err := ctx.Err(); err != nil {
		return SourceSnapshot{}, err
	}
	candidate, err := inspectSource(raw, false)
	if err != nil {
		return SourceSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.recoveries[recoveryID]; !exists {
		return SourceSnapshot{}, errors.New("workflow source recovery not found")
	}
	if _, exists := s.sources[candidate.snapshot.workflowID]; exists || len(s.sources) >= s.max {
		return SourceSnapshot{}, ErrSourceConflict
	}
	record := candidate.record(s.now().UTC())
	if err := s.repository.Repair(ctx, recoveryID, record, candidate.references()); err != nil {
		return SourceSnapshot{}, sourceRepositoryError(err)
	}
	s.sources[candidate.snapshot.workflowID] = candidate.snapshot
	delete(s.recoveries, recoveryID)
	return cloneSource(candidate.snapshot), nil
}

func (s *SourceStore) DeleteRecovery(ctx context.Context, recoveryID artifact.Digest) error {
	if ctx == nil || !recoveryID.Valid() {
		return errors.New("workflow source recovery delete requires context and recovery identity")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.recoveries[recoveryID]; !exists {
		return errors.New("workflow source recovery not found")
	}
	deleted, err := s.repository.DeleteQuarantine(ctx, recoveryID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrSourceChanged
	}
	delete(s.recoveries, recoveryID)
	return nil
}

func (s *SourceStore) Save(ctx context.Context, raw []byte, baseRevision int64) (SourceSnapshot, error) {
	if ctx == nil {
		return SourceSnapshot{}, errors.New("workflow source save context is required")
	}
	if err := ctx.Err(); err != nil {
		return SourceSnapshot{}, err
	}
	candidate, err := inspectSource(raw, false)
	if err != nil {
		return SourceSnapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.sources[candidate.snapshot.workflowID]
	if !exists {
		if baseRevision != -1 || candidate.snapshot.revision != 0 || len(s.sources) >= s.max {
			return SourceSnapshot{}, ErrSourceConflict
		}
	} else {
		if baseRevision != current.revision || candidate.snapshot.revision != current.revision+1 {
			return SourceSnapshot{}, ErrSourceConflict
		}
		if err := s.verifyLocked(current); err != nil {
			return SourceSnapshot{}, err
		}
	}
	now := s.now().UTC()
	if err := s.repository.Commit(ctx, baseRevision, candidate.record(now), candidate.references()); err != nil {
		return SourceSnapshot{}, sourceRepositoryError(err)
	}
	s.sources[candidate.snapshot.workflowID] = candidate.snapshot
	return cloneSource(candidate.snapshot), nil
}

func (s *SourceStore) Load(workflowID string) (SourceSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, ok := s.sources[workflowID]
	if !ok {
		return SourceSnapshot{}, ErrSourceNotFound
	}
	if err := s.verifyLocked(current); err != nil {
		return SourceSnapshot{}, err
	}
	return cloneSource(current), nil
}

func (s *SourceStore) List() []SourceSnapshot {
	s.mu.RLock()
	ids := make([]string, 0, len(s.sources))
	for id := range s.sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]SourceSnapshot, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneSource(s.sources[id]))
	}
	s.mu.RUnlock()
	return result
}

func (s *SourceStore) Delete(ctx context.Context, workflowID string, revision int64, hash artifact.Digest) error {
	if ctx == nil || strings.TrimSpace(workflowID) == "" || revision < 0 || !hash.Valid() {
		return errors.New("workflow source delete requires context and exact source identity")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.sources[workflowID]
	if !exists {
		return ErrSourceNotFound
	}
	if current.revision != revision || current.hash != hash {
		return ErrSourceConflict
	}
	if err := s.verifyLocked(current); err != nil {
		return err
	}
	deleted, err := s.repository.Delete(ctx, workflowID, revision, hash)
	if err != nil {
		return sourceRepositoryError(err)
	}
	if !deleted {
		return ErrSourceChanged
	}
	delete(s.sources, workflowID)
	return nil
}

func (s *SourceStore) verifyLocked(current SourceSnapshot) error {
	record, found, err := s.repository.Get(context.Background(), current.workflowID)
	if err != nil {
		return err
	}
	if !found {
		return ErrSourceChanged
	}
	durable, err := inspectSource(record.Artifact, true)
	if err != nil || durable.snapshot.hash != current.hash ||
		durable.snapshot.revision != current.revision ||
		!bytes.Equal(durable.snapshot.raw, current.raw) ||
		record.Hash != current.hash || record.Revision != current.revision {
		return changedSource(err)
	}
	return nil
}

type sourceCandidate struct {
	snapshot SourceSnapshot
	name     string
	format   string
	version  string
	refs     []blob.BlobRef
}

func inspectSource(raw []byte, requireCanonical bool) (sourceCandidate, error) {
	document, canonical, digest, diagnostics, err := schema.CanonicalSource(raw)
	if err != nil {
		return sourceCandidate{}, err
	}
	if len(diagnostics) != 0 {
		return sourceCandidate{}, &InvalidSourceError{Diagnostics: append([]schema.Diagnostic(nil), diagnostics...)}
	}
	if requireCanonical && !bytes.Equal(raw, canonical) {
		return sourceCandidate{}, errors.New("workflow source artifact is not canonical")
	}
	refs, err := schema.BlobReferences(document)
	if err != nil {
		return sourceCandidate{}, err
	}
	return sourceCandidate{
		snapshot: SourceSnapshot{
			workflowID: document.Workflow.ID, revision: document.Revision,
			hash: digest, raw: append([]byte(nil), canonical...),
		},
		name: document.Workflow.Name, format: document.Format, version: document.Version,
		refs: append([]blob.BlobRef(nil), refs...),
	}, nil
}

func (c sourceCandidate) record(now time.Time) catalog.WorkflowSourceRecord {
	return catalog.WorkflowSourceRecord{
		WorkflowID: c.snapshot.workflowID, Name: c.name, Revision: c.snapshot.revision,
		Hash: c.snapshot.hash, Format: c.format, Version: c.version,
		Artifact: append([]byte(nil), c.snapshot.raw...), UpdatedAt: now,
	}
}

func (c sourceCandidate) references() []catalog.WorkflowReference {
	result := make([]catalog.WorkflowReference, 0, len(c.refs))
	for index, ref := range c.refs {
		result = append(result, catalog.WorkflowReference{
			Role: fmt.Sprintf("blob/%06d", index), Blob: ref,
		})
	}
	return result
}

func cloneSource(source SourceSnapshot) SourceSnapshot {
	source.raw = append([]byte(nil), source.raw...)
	return source
}

func changedSource(cause error) error {
	if cause == nil {
		return ErrSourceChanged
	}
	return fmt.Errorf("%w: %v", ErrSourceChanged, cause)
}

func sourceRepositoryError(err error) error {
	if errors.Is(err, catalog.ErrWorkflowSourceConflict) {
		return ErrSourceConflict
	}
	return err
}
