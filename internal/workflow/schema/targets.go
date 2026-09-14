package schema

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// WorkflowTarget declares a portable role. Its machine binding is never Source data.
type WorkflowTarget struct {
	ID          string `json:"id" jsonschema:"required,maxLength=128,pattern=^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$"`
	Name        string `json:"name" jsonschema:"required,minLength=1,maxLength=256"`
	Description string `json:"description,omitempty" jsonschema:"maxLength=4096"`
	Kind        string `json:"kind" jsonschema:"required,minLength=1,maxLength=128,pattern=^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$"`
	Default     bool   `json:"default,omitempty"`
}

const DefaultWorkflowTargetID = "workflow-default"
const TargetParameterPrefix = "@target/"

func DefaultWorkflowTargets() []WorkflowTarget {
	return []WorkflowTarget{{ID: DefaultWorkflowTargetID, Name: "Default", Kind: "automation", Default: true}}
}
func ValidateWorkflowTargets(targets []WorkflowTarget) error {
	if len(targets) == 0 {
		return nil
	}
	if len(targets) > 64 {
		return fmt.Errorf("workflow target budget exceeded")
	}
	ids := map[string]bool{}
	defaults := 0
	for _, t := range targets {
		if !validTargetDefaultName(t.ID) || !validTargetDefaultName(t.Kind) || strings.TrimSpace(t.Name) == "" || utf8.RuneCountInString(t.Name) > 256 || utf8.RuneCountInString(t.Description) > 4096 {
			return fmt.Errorf("workflow target definition is invalid")
		}
		if ids[t.ID] {
			return fmt.Errorf("duplicate workflow target")
		}
		ids[t.ID] = true
		if t.Default {
			defaults++
		}
	}
	if defaults != 1 {
		return fmt.Errorf("workflow requires one default target")
	}
	return nil
}
