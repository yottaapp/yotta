package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

type ParameterConfiguration struct {
	Targets   []schema.WorkflowTarget    `json:"targets"`
	Blocks    []schema.ParameterBlock    `json:"blocks"`
	Variables []schema.Variable          `json:"variables"`
	Values    map[string]json.RawMessage `json:"values"`
	Revision  int64                      `json:"revision"`
}

func (a *Application) GetParameters(workflowID string) (ParameterConfiguration, error) {
	a.commandMu.RLock()
	defer a.commandMu.RUnlock()
	if err := a.requireRunning(); err != nil {
		return ParameterConfiguration{}, err
	}
	snapshot, err := a.sources.Load(workflowID)
	if err != nil {
		return ParameterConfiguration{}, err
	}
	source, diagnostics := schema.ParseSource(snapshot.Artifact())
	if schema.HasErrors(diagnostics) {
		return ParameterConfiguration{}, errors.New("invalid workflow source")
	}
	values, err := a.parameters.Load(workflowID)
	if err != nil {
		return ParameterConfiguration{}, err
	}
	variables := []schema.Variable{}
	for _, variable := range source.Variables {
		if variable.Parameter != nil {
			variables = append(variables, variable)
		}
	}
	return ParameterConfiguration{Targets: source.Targets, Blocks: source.ParameterBlocks, Variables: variables, Values: values, Revision: source.Revision}, nil
}

// SaveParameters checks the exact revision displayed by the form. It does not
// publish a source revision, compile a new source, or affect a queued Program.
func (a *Application) SaveParameters(ctx context.Context, workflowID string, revision int64, values map[string]json.RawMessage) ([]schema.Diagnostic, error) {
	if ctx == nil {
		return nil, errors.New("parameter save context is required")
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.requireRunning(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := a.sources.Load(workflowID)
	if err != nil {
		return nil, err
	}
	if snapshot.Revision() != revision {
		return nil, workflowstore.ErrSourceConflict
	}
	source, diagnostics := schema.ParseSource(snapshot.Artifact())
	if schema.HasErrors(diagnostics) {
		return diagnostics, nil
	}
	known := map[string]json.RawMessage{}
	for _, variable := range source.Variables {
		if variable.Parameter == nil {
			continue
		}
		if value, exists := values[variable.Parameter.ID]; exists {
			known[variable.Parameter.ID] = append(json.RawMessage(nil), value...)
		}
	}
	for _, role := range source.Targets {
		key := schema.TargetParameterPrefix + role.ID
		if value, exists := values[key]; exists {
			known[key] = append(json.RawMessage(nil), value...)
		}
	}
	leased, err := a.leaseRunTargets()
	if err != nil {
		return nil, err
	}
	defer leased.release()
	_, targetDiagnostics := checkTargetBindings(source, known, leased.targets, nil)
	if schema.HasErrors(targetDiagnostics) {
		return targetDiagnostics, nil
	}
	// Validate values against pinned data types without depending on whether
	// the author's graph is already complete or its resources are available.
	diagnostics = compiler.ValidateParameterValues(source.Variables, known, a.catalog)
	if schema.HasErrors(diagnostics) {
		return diagnostics, nil
	}
	return diagnostics, a.parameters.Save(workflowID, known)
}

// SaveTargetBindings replaces only local target bindings, preserving parameter
// drafts already saved by the homepage. Revision and record update share the
// same command lock as SaveParameters and Source editing.
func (a *Application) SaveTargetBindings(ctx context.Context, workflowID string, revision int64, bindings map[string]string) ([]schema.Diagnostic, error) {
	if ctx == nil {
		return nil, errors.New("parameter save context is required")
	}
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.requireRunning(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := a.sources.Load(workflowID)
	if err != nil {
		return nil, err
	}
	if snapshot.Revision() != revision {
		return nil, workflowstore.ErrSourceConflict
	}
	source, diagnostics := schema.ParseSource(snapshot.Artifact())
	if schema.HasErrors(diagnostics) {
		return diagnostics, nil
	}
	values, err := a.parameters.Load(workflowID)
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	for key := range values {
		if strings.HasPrefix(key, schema.TargetParameterPrefix) {
			delete(values, key)
		}
	}
	declared := map[string]bool{}
	for _, role := range source.Targets {
		declared[role.ID] = true
	}
	for id, slot := range bindings {
		if !declared[id] {
			return []schema.Diagnostic{targetDiagnostic(CodeTargetUndeclared, schema.WorkflowTarget{ID: id}, 0)}, nil
		}
		if slot != "" {
			raw, _ := json.Marshal(slot)
			values[schema.TargetParameterPrefix+id] = raw
		}
	}
	leased, err := a.leaseRunTargets()
	if err != nil {
		return nil, err
	}
	defer leased.release()
	_, diagnostics = checkTargetBindings(source, values, leased.targets, nil)
	if schema.HasErrors(diagnostics) {
		return diagnostics, nil
	}
	return diagnostics, a.parameters.Save(workflowID, values)
}

// CopyParameters is only for cloning an existing local workflow. Import and
// installation do not call it: machine bindings are never package defaults.
func (a *Application) CopyParameters(sourceID, targetID string) error {
	a.commandMu.Lock()
	defer a.commandMu.Unlock()
	if err := a.requireRunning(); err != nil {
		return err
	}
	if sourceID == targetID {
		return errors.New("parameter copy requires distinct workflows")
	}
	from, err := a.sources.Load(sourceID)
	if err != nil {
		return err
	}
	to, err := a.sources.Load(targetID)
	if err != nil {
		return err
	}
	source, ds := schema.ParseSource(from.Artifact())
	if schema.HasErrors(ds) {
		return errors.New("invalid source workflow for parameter copy")
	}
	destination, ds := schema.ParseSource(to.Artifact())
	if schema.HasErrors(ds) {
		return errors.New("invalid destination workflow for parameter copy")
	}
	keys := func(s schema.WorkflowSource) map[string]bool {
		result := map[string]bool{}
		for _, variable := range s.Variables {
			if variable.Parameter != nil {
				result[variable.Parameter.ID] = true
			}
		}
		for _, role := range s.Targets {
			result[schema.TargetParameterPrefix+role.ID] = true
		}
		return result
	}
	fromKeys, toKeys := keys(source), keys(destination)
	values, err := a.parameters.Load(sourceID)
	if err != nil {
		return err
	}
	copied := map[string]json.RawMessage{}
	for key, value := range values {
		if fromKeys[key] && toKeys[key] {
			copied[key] = append(json.RawMessage(nil), value...)
		}
	}
	return a.parameters.Save(targetID, copied)
}
