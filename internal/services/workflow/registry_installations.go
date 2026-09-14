package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/durablefs"
	"github.com/yottaapp/yotta/internal/workflowbundle"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

type RegistryInstallation struct {
	WorkflowID     string `json:"workflowId"`
	ReleaseID      string `json:"releaseId"`
	ReleaseVersion string `json:"releaseVersion"`
	SourceHash     string `json:"sourceHash"`
}

func (s *Service) readRegistryInstallations() (map[string]RegistryInstallation, error) {
	result := map[string]RegistryInstallation{}
	if s.registryStatePath == "" {
		return result, nil
	}
	raw, err := os.ReadFile(s.registryStatePath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	// Rebase only this detached read. Installation/update owns persistence;
	// writing here could replace a newer record map from a concurrent operation.
	for id, record := range result {
		current, err := s.application.GetSource(id)
		if err != nil || string(current.Hash()) == record.SourceHash {
			continue
		}
		known, err := workflowstore.KnownSourceMigration(filepath.Join(filepath.Dir(s.registryStatePath), "workflow-source-migrations"), id, artifact.Digest(record.SourceHash), current.Hash())
		if err != nil {
			return nil, err
		}
		if known {
			record.SourceHash = string(current.Hash())
			result[id] = record
		}
	}
	return result, nil
}

func (s *Service) saveRegistryInstallations(records map[string]RegistryInstallation) error {
	if s.registryStatePath == "" {
		return nil
	}
	raw, err := json.Marshal(records)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.registryStatePath), 0700); err != nil {
		return err
	}
	return durablefs.WriteFile(s.registryStatePath, raw, 0600)
}

func (s *Service) RegistryInstallations() ([]RegistryInstallation, error) {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	records, err := s.readRegistryInstallations()
	if err != nil {
		return nil, registryError("installed", err)
	}
	result := []RegistryInstallation{}
	for _, record := range records {
		if _, err := s.application.GetSource(record.WorkflowID); err == nil {
			result = append(result, record)
		}
	}
	return result, nil
}

func (s *Service) CloneSource(ctx context.Context, workflowID string) (SourceView, error) {
	if s.bundles == nil {
		return SourceView{}, unavailable("bundle")
	}
	_, raw, err := s.bundles.ExportBytes(ctx, workflowID)
	if err != nil {
		return SourceView{}, bundleError("clone", err)
	}
	file, err := os.CreateTemp("", ".yotta-clone-*.yotta-workflow")
	if err != nil {
		return SourceView{}, bundleError("clone", err)
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err != nil {
		file.Close()
		return SourceView{}, bundleError("clone", err)
	}
	if err = file.Close(); err != nil {
		return SourceView{}, bundleError("clone", err)
	}
	result, err := s.bundles.Import(ctx, workflowbundle.ImportRequest{Path: file.Name(), Mode: workflowbundle.ImportCopy})
	if err != nil {
		return SourceView{}, bundleError("clone", err)
	}
	if err := s.application.CopyParameters(workflowID, result.Source.WorkflowID()); err != nil {
		cleanupErr := s.application.DeleteSource(ctx, result.Source.WorkflowID(), result.Source.Revision(), result.Source.Hash())
		return SourceView{}, bundleError("clone", errors.Join(err, cleanupErr))
	}
	return sourceView(result.Source, true)
}

func localRegistryChanges() error {
	return projectError("workflow.registry.local_changes", apperr.CategoryDomain, nil, false, errors.New("local workflow differs from installed release; clone before updating"))
}
