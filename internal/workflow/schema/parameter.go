package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Parameter describes the public input bound to a variable. ID is independent
// of its display name and remains stable when the author edits presentation.
// Values selected on this machine never belong to WorkflowSource.
type Parameter struct {
	ID          string              `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Label       string              `json:"label" jsonschema:"required,minLength=1,maxLength=256"`
	Description string              `json:"description,omitempty" jsonschema:"maxLength=4096"`
	Group       string              `json:"group,omitempty" jsonschema:"maxLength=256"`
	Order       int                 `json:"order,omitempty"`
	Control     string              `json:"control" jsonschema:"required,enum=auto,enum=select,enum=multiselect"`
	Options     []ParameterOption   `json:"options,omitempty" jsonschema:"maxItems=256"`
	Minimum     *float64            `json:"minimum,omitempty"`
	Maximum     *float64            `json:"maximum,omitempty"`
	Required    bool                `json:"required,omitempty"`
	VisibleWhen *ParameterCondition `json:"visibleWhen,omitempty"`
}

type ParameterOption struct {
	Label string          `json:"label" jsonschema:"required,minLength=1,maxLength=256"`
	Value json.RawMessage `json:"value" jsonschema:"required"`
}

type ParameterCondition struct {
	ParameterID string          `json:"parameterId" jsonschema:"required,maxLength=128"`
	Equals      json.RawMessage `json:"equals" jsonschema:"required"`
}

// ValidateValue checks presentation constraints. The compiler independently
// seals the value against the variable's pinned data type before admission.
func (p Parameter) ValidateValue(raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("invalid parameter JSON")
	}
	if p.Required && (value == nil || value == "") {
		return fmt.Errorf("parameter is required")
	}
	if number, ok := value.(float64); ok {
		if p.Minimum != nil && number < *p.Minimum {
			return fmt.Errorf("below minimum")
		}
		if p.Maximum != nil && number > *p.Maximum {
			return fmt.Errorf("above maximum")
		}
	}
	if p.Control == "select" && !p.contains(raw) {
		return fmt.Errorf("option no longer exists")
	}
	if p.Control == "multiselect" {
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("multiple selection requires a list")
		}
		if p.Required && len(items) == 0 {
			return fmt.Errorf("parameter is required")
		}
		seen := map[string]bool{}
		for _, item := range items {
			encoded, _ := json.Marshal(item)
			if !p.contains(encoded) || seen[string(encoded)] {
				return fmt.Errorf("invalid or repeated option")
			}
			seen[string(encoded)] = true
		}
	}
	return nil
}

func (p Parameter) contains(raw json.RawMessage) bool {
	for _, option := range p.Options {
		if equalParameterJSON(raw, option.Value) {
			return true
		}
	}
	return false
}

func equalParameterJSON(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	return bytes.Equal(x, y)
}

func validateParameters(variables []Variable) []Diagnostic {
	var out []Diagnostic
	ids := map[string]bool{}
	dependencies := map[string]string{}
	for _, variable := range variables {
		if variable.Parameter != nil {
			ids[variable.Parameter.ID] = true
			if variable.Parameter.VisibleWhen != nil {
				dependencies[variable.Parameter.ID] = variable.Parameter.VisibleWhen.ParameterID
			}
		}
	}
	seen := map[string]bool{}
	for index, variable := range variables {
		p := variable.Parameter
		if p == nil {
			continue
		}
		reason := ""
		switch {
		case seen[p.ID]:
			reason = "duplicate parameter ID"
		case strings.TrimSpace(p.Label) == "":
			reason = "parameter label is empty"
		case p.Minimum != nil && (math.IsNaN(*p.Minimum) || math.IsInf(*p.Minimum, 0)):
			reason = "invalid minimum"
		case p.Maximum != nil && (math.IsNaN(*p.Maximum) || math.IsInf(*p.Maximum, 0)):
			reason = "invalid maximum"
		case p.Minimum != nil && p.Maximum != nil && *p.Minimum > *p.Maximum:
			reason = "minimum exceeds maximum"
		case p.Control != "auto" && len(p.Options) == 0:
			reason = "selection requires options"
		case p.VisibleWhen != nil && (!ids[p.VisibleWhen.ParameterID] || p.VisibleWhen.ParameterID == p.ID):
			reason = "invalid visibility dependency"
		}
		seen[p.ID] = true
		chain := map[string]bool{}
		for cursor := p.ID; cursor != ""; cursor = dependencies[cursor] {
			if chain[cursor] {
				reason = "cyclic visibility dependency"
				break
			}
			chain[cursor] = true
		}
		for optionIndex, option := range p.Options {
			if strings.TrimSpace(option.Label) == "" || !json.Valid(option.Value) {
				reason = "invalid option"
			}
			for _, previous := range p.Options[:optionIndex] {
				if equalParameterJSON(previous.Value, option.Value) {
					reason = "duplicate option value"
				}
			}
		}
		if reason == "" {
			if err := p.ValidateValue(variable.Default); err != nil {
				reason = err.Error()
			}
		}
		if reason != "" {
			out = append(out, diagnostic(CodeInvalidWorkflowJSON, []string{"variables", fmt.Sprint(index), "parameter"}, map[string]any{"reason": reason}))
		}
	}
	return out
}

// ParameterBlock is presentation only; it never creates runtime state.
type ParameterBlock struct {
	Description string `json:"description,omitempty" jsonschema:"maxLength=4096"`
	ID          string `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Kind        string `json:"kind" jsonschema:"required,enum=label,enum=separator"`
	Label       string `json:"label,omitempty" jsonschema:"maxLength=4096"`
	Order       int    `json:"order,omitempty"`
}

func validateParameterBlocks(source WorkflowSource) []Diagnostic {
	ids := map[string]bool{}
	for _, v := range source.Variables {
		if v.Parameter != nil {
			ids[v.Parameter.ID] = true
		}
	}
	for _, block := range source.ParameterBlocks {
		if ids[block.ID] {
			return []Diagnostic{diagnostic(CodeInvalidField, []string{"parameterBlocks"}, map[string]any{"reason": "duplicate presentation ID"})}
		}
		ids[block.ID] = true
	}
	return nil
}
