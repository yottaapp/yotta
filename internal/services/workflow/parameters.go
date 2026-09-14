package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/workflowstore"

	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func (s *Service) GetParameters(workflowID string) (appcore.ParameterConfiguration, error) {
	result, err := s.application.GetParameters(workflowID)
	if err != nil {
		return result, sourceError("load-parameters", err)
	}
	return result, nil
}

func (s *Service) SaveParameters(workflowID string, revision int64, values map[string]json.RawMessage) ([]schema.Diagnostic, error) {
	result, err := s.application.SaveParameters(context.Background(), workflowID, revision, values)
	if errors.Is(err, workflowstore.ErrSourceConflict) {
		return nil, apperr.New("workflow.revision.conflict", map[string]any{"baseRevision": revision})
	}
	if err != nil {
		return nil, sourceError("save-parameters", err)
	}
	return result, nil
}

func (s *Service) SaveTargetBindings(workflowID string, revision int64, bindings map[string]string) ([]schema.Diagnostic, error) {
	result, err := s.application.SaveTargetBindings(context.Background(), workflowID, revision, bindings)
	if errors.Is(err, workflowstore.ErrSourceConflict) {
		return nil, apperr.New("workflow.revision.conflict", map[string]any{"baseRevision": revision})
	}
	if err != nil {
		return nil, sourceError("save-target-bindings", err)
	}
	return result, nil
}
