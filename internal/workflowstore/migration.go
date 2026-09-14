package workflowstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

var errSourceMigrationUnavailable = errors.New("workflow source migration is unavailable")

type sourceContract struct {
	Format  string
	Version string
}

func (c sourceContract) valid() bool {
	return c.Format != "" && c.Version != ""
}

type sourceMigration func([]byte) ([]byte, error)

type sourceMigrationStep struct {
	From  sourceContract
	To    sourceContract
	Apply sourceMigration
}

type sourceMigrationPlan struct {
	current sourceContract
	steps   map[sourceContract]sourceMigrationStep
}

// currentSourceMigrationPlan is the single registration point for released
// Workflow Source migrations. It intentionally contains no development-format
// repair: a schema change after release must change format/version and add an
// explicit deterministic step here.
func currentSourceMigrationPlan() (sourceMigrationPlan, error) {
	current := sourceContract{Format: schema.Format, Version: schema.Version}
	return newSourceMigrationPlan(current, []sourceMigrationStep{{
		From: sourceContract{Format: schema.Format, Version: "1"}, To: current,
		Apply: migrateSourceParameters,
	}, {From: sourceContract{Format: schema.Format, Version: "2"}, To: current, Apply: migrateSourceParameterBlocks}, {From: sourceContract{Format: schema.Format, Version: "3"}, To: current, Apply: migrateSourceBlockDescriptions}, {From: sourceContract{Format: schema.Format, Version: "4"}, To: current, Apply: migrateSourceWorkflowTargets}})
}

func migrateSourceParameters(raw []byte) ([]byte, error) {
	canonical, err := artifact.Canonicalize(raw)
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &document); err != nil {
		return nil, err
	}
	var variables []map[string]json.RawMessage
	if err := json.Unmarshal(document["variables"], &variables); err != nil {
		return nil, err
	}
	for _, variable := range variables {
		if _, exists := variable["parameter"]; exists {
			return nil, errors.New("v1 source cannot declare parameters")
		}
	}
	if _, exists := document["parameterBlocks"]; exists {
		return nil, errors.New("legacy source cannot declare parameter blocks")
	}
	return upgradeParameterDocument(document)
}

func migrateSourceParameterBlocks(raw []byte) ([]byte, error) {
	canonical, err := artifact.Canonicalize(raw)
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &document); err != nil {
		return nil, err
	}
	if _, exists := document["parameterBlocks"]; exists {
		return nil, errors.New("v2 source cannot declare parameter blocks")
	}
	return upgradeParameterDocument(document)
}
func migrateSourceBlockDescriptions(raw []byte) ([]byte, error) {
	canonical, err := artifact.Canonicalize(raw)
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &document); err != nil {
		return nil, err
	}
	var blocks []map[string]json.RawMessage
	if rawBlocks, exists := document["parameterBlocks"]; exists {
		if err := json.Unmarshal(rawBlocks, &blocks); err != nil {
			return nil, err
		}
		for _, block := range blocks {
			if _, exists := block["description"]; exists {
				return nil, errors.New("v3 source cannot declare block descriptions")
			}
		}
	}
	return upgradeParameterDocument(document)
}
func migrateSourceWorkflowTargets(raw []byte) ([]byte, error) {
	canonical, err := artifact.Canonicalize(raw)
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err = json.Unmarshal(canonical, &document); err != nil {
		return nil, err
	}
	return upgradeParameterDocument(document)
}
func upgradeParameterDocument(document map[string]json.RawMessage) ([]byte, error) {
	document["version"], _ = json.Marshal(schema.Version)
	updated, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	if _, exists := document["targets"]; exists {
		return nil, errors.New("legacy source cannot declare workflow targets")
	}
	// Validate before typed decoding: otherwise unknown graph/node fields and
	// invalid values can disappear during the migration round trip.
	targetSource, diagnostics := schema.ParseSource(updated)
	if schema.HasErrors(diagnostics) {
		return nil, &InvalidSourceError{Diagnostics: diagnostics}
	}
	if _, err := transformLegacyTargets(&targetSource); err != nil {
		return nil, err
	}
	document["targets"], _ = json.Marshal(targetSource.Targets)
	document["targetDefaults"], _ = json.Marshal(targetSource.TargetDefaults)
	// Rewrite only the references; retain exact JSON numbers and other config.
	var graphs []map[string]json.RawMessage
	if err := json.Unmarshal(document["graphs"], &graphs); err != nil {
		return nil, err
	}
	for gi := range graphs {
		var nodes []map[string]json.RawMessage
		if err := json.Unmarshal(graphs[gi]["nodes"], &nodes); err != nil {
			return nil, err
		}
		for ni := range nodes {
			node := targetSource.Graphs[gi].Nodes[ni]
			if legacyTargetKind(node.NodeRef.NodeTypeID) == "" {
				continue
			}
			slot, ok := node.Config["slot"].(string)
			if !ok || slot == "" {
				continue
			}
			var config map[string]json.RawMessage
			if err := json.Unmarshal(nodes[ni]["config"], &config); err != nil {
				return nil, err
			}
			config["slot"], _ = json.Marshal(slot)
			nodes[ni]["config"], _ = json.Marshal(config)
		}
		graphs[gi]["nodes"], _ = json.Marshal(nodes)
	}
	document["graphs"], _ = json.Marshal(graphs)
	updated, err = json.Marshal(document)
	if err != nil {
		return nil, err
	}
	_, diagnostics = schema.ParseSource(updated)
	if schema.HasErrors(diagnostics) {
		return nil, &InvalidSourceError{Diagnostics: diagnostics}
	}
	return updated, nil
}

// MigrateSourceArtifact is the shared read seam for durable stores and
// portable bundles. It only applies fully registered format/version chains;
// callers validate and publish the returned current artifact at their own
// authority boundary.
func MigrateSourceArtifact(raw []byte) ([]byte, bool, error) {
	plan, err := currentSourceMigrationPlan()
	if err != nil {
		return nil, false, err
	}
	return plan.Migrate(raw)
}

func newSourceMigrationPlan(current sourceContract, steps []sourceMigrationStep) (sourceMigrationPlan, error) {
	if !current.valid() {
		return sourceMigrationPlan{}, errors.New("workflow source migration plan requires a current contract")
	}
	bySource := make(map[sourceContract]sourceMigrationStep, len(steps))
	for _, step := range steps {
		if !step.From.valid() || !step.To.valid() || step.From == step.To || step.From == current || step.Apply == nil {
			return sourceMigrationPlan{}, errors.New("workflow source migration step is invalid")
		}
		if _, duplicate := bySource[step.From]; duplicate {
			return sourceMigrationPlan{}, fmt.Errorf("duplicate workflow source migration from %s/%s", step.From.Format, step.From.Version)
		}
		bySource[step.From] = step
	}
	for origin := range bySource {
		seen := make(map[sourceContract]struct{}, len(bySource))
		cursor := origin
		for cursor != current {
			if _, cycle := seen[cursor]; cycle {
				return sourceMigrationPlan{}, fmt.Errorf("workflow source migration cycle from %s/%s", origin.Format, origin.Version)
			}
			seen[cursor] = struct{}{}
			step, ok := bySource[cursor]
			if !ok {
				return sourceMigrationPlan{}, fmt.Errorf(
					"workflow source migration from %s/%s does not reach current %s/%s",
					origin.Format, origin.Version, current.Format, current.Version,
				)
			}
			cursor = step.To
		}
	}
	return sourceMigrationPlan{current: current, steps: bySource}, nil
}

// Migrate applies only a fully registered format/version chain. Current
// artifacts are returned byte-for-byte so strict current-schema validation
// remains the only authority for their shape.
func (p sourceMigrationPlan) Migrate(raw []byte) ([]byte, bool, error) {
	contract, err := sourceContractOf(raw)
	if err != nil {
		return append([]byte(nil), raw...), false, nil
	}
	if contract == p.current {
		return append([]byte(nil), raw...), false, nil
	}
	current := append([]byte(nil), raw...)
	for applied := 0; contract != p.current; applied++ {
		if applied >= len(p.steps) {
			return nil, false, fmt.Errorf("%w: %s/%s", errSourceMigrationUnavailable, contract.Format, contract.Version)
		}
		step, ok := p.steps[contract]
		if !ok {
			return nil, false, fmt.Errorf("%w: %s/%s", errSourceMigrationUnavailable, contract.Format, contract.Version)
		}
		next, err := step.Apply(append([]byte(nil), current...))
		if err != nil {
			return nil, false, fmt.Errorf("migrate Workflow Source %s/%s to %s/%s: %w",
				step.From.Format, step.From.Version, step.To.Format, step.To.Version, err)
		}
		nextContract, err := sourceContractOf(next)
		if err != nil {
			return nil, false, fmt.Errorf("migration from %s/%s produced an invalid contract header: %w",
				step.From.Format, step.From.Version, err)
		}
		if nextContract != step.To {
			return nil, false, fmt.Errorf(
				"migration from %s/%s declared %s/%s but produced %s/%s",
				step.From.Format, step.From.Version, step.To.Format, step.To.Version,
				nextContract.Format, nextContract.Version,
			)
		}
		current = next
		contract = nextContract
	}
	return current, true, nil
}

func sourceContractOf(raw []byte) (sourceContract, error) {
	var header struct {
		Format  string `json:"format"`
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&header); err != nil {
		return sourceContract{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return sourceContract{}, errors.New("workflow source contains trailing JSON")
	}
	contract := sourceContract{Format: header.Format, Version: header.Version}
	if !contract.valid() {
		return sourceContract{}, errors.New("workflow source contract header is missing")
	}
	return contract, nil
}
