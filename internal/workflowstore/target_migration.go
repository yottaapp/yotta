package workflowstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func legacyTargetID(slot string) string {
	sum := sha256.Sum256([]byte(slot))
	return "workflow-target-" + hex.EncodeToString(sum[:8])
}

// This is the released v4 configured-target inventory, not a namespace guess.
// Unknown/plugin config keys named slot may mean something entirely different.
func legacyTargetKind(id string) string {
	switch id {
	case "https://schemas.yotta.dev/nodes/application/launch", "https://schemas.yotta.dev/nodes/application/terminate":
		return "configured-application"
	case "https://schemas.yotta.dev/nodes/automation/activate-window",
		"https://schemas.yotta.dev/nodes/automation/capture-window",
		"https://schemas.yotta.dev/nodes/automation/click-pointer",
		"https://schemas.yotta.dev/nodes/automation/click-template",
		"https://schemas.yotta.dev/nodes/automation/close-window",
		"https://schemas.yotta.dev/nodes/automation/control-dual-color-bar",
		"https://schemas.yotta.dev/nodes/automation/drag-pointer",
		"https://schemas.yotta.dev/nodes/automation/get-pointer-position",
		"https://schemas.yotta.dev/nodes/automation/get-window-state",
		"https://schemas.yotta.dev/nodes/automation/hold-keys",
		"https://schemas.yotta.dev/nodes/automation/hold-pointer-button",
		"https://schemas.yotta.dev/nodes/automation/maximize-window",
		"https://schemas.yotta.dev/nodes/automation/minimize-window",
		"https://schemas.yotta.dev/nodes/automation/move-character-to",
		"https://schemas.yotta.dev/nodes/automation/move-pointer",
		"https://schemas.yotta.dev/nodes/automation/move-pointer-relative",
		"https://schemas.yotta.dev/nodes/automation/move-resize-window",
		"https://schemas.yotta.dev/nodes/automation/play-input-clip",
		"https://schemas.yotta.dev/nodes/automation/play-macro",
		"https://schemas.yotta.dev/nodes/automation/press-keys",
		"https://schemas.yotta.dev/nodes/automation/release-held-input",
		"https://schemas.yotta.dev/nodes/automation/restore-window",
		"https://schemas.yotta.dev/nodes/automation/scroll-pointer",
		"https://schemas.yotta.dev/nodes/automation/stop-target-app",
		"https://schemas.yotta.dev/nodes/automation/turn-find-template",
		"https://schemas.yotta.dev/nodes/automation/turn-view",
		"https://schemas.yotta.dev/nodes/automation/type-text",
		"https://schemas.yotta.dev/nodes/automation/wait-change",
		"https://schemas.yotta.dev/nodes/automation/wait-stable",
		"https://schemas.yotta.dev/nodes/automation/wait-template",
		"https://schemas.yotta.dev/nodes/automation/wait-template-gone",
		"https://schemas.yotta.dev/nodes/automation/wait-window",
		"https://schemas.yotta.dev/nodes/automation/wait-window-gone",
		"https://schemas.yotta.dev/nodes/navigation/follow-path",
		"https://schemas.yotta.dev/nodes/navigation/follow-saved-path":
		return "automation"
	default:
		return ""
	}
}

// transformLegacyTargets changes references only. Local bindings are returned
// separately and are never included in the portable source or target labels.
func transformLegacyTargets(source *schema.WorkflowSource) (map[string]string, error) {
	bindings := map[string]string{}
	if len(source.Targets) > 0 {
		return bindings, nil
	}
	bySlot := map[string]int{}
	add := func(slot, kind string) int {
		if i, ok := bySlot[slot]; ok {
			return i
		}
		i := len(source.Targets)
		id := legacyTargetID(slot)
		bySlot[slot] = i
		source.Targets = append(source.Targets, schema.WorkflowTarget{ID: id, Name: fmt.Sprintf("Target %d", i+1), Kind: kind})
		bindings[id] = slot
		return i
	}
	for i := range source.TargetDefaults {
		d := &source.TargetDefaults[i]
		kind := "automation"
		if d.Target == "application" {
			kind = "configured-application"
		} else if d.Target != "target" {
			continue
		}
		if d.Slot == "" {
			continue
		}
		index := add(d.Slot, kind)
		d.Slot = source.Targets[index].ID
		if d.Target == "target" {
			source.Targets[index].Default = true
		}
	}
	applicationDefault := ""
	for _, d := range source.TargetDefaults {
		if d.Target == "application" {
			applicationDefault = d.Slot
		}
	}
	for gi := range source.Graphs {
		for ni := range source.Graphs[gi].Nodes {
			node := &source.Graphs[gi].Nodes[ni]
			kind := legacyTargetKind(node.NodeRef.NodeTypeID)
			if kind == "" {
				continue
			}
			slot, _ := node.Config["slot"].(string)
			if _, explicit := node.Config["slot"]; !explicit && kind == "configured-application" && applicationDefault != "" {
				node.Config["slot"] = applicationDefault
				continue
			}
			if slot == "" {
				continue
			}
			index := add(slot, kind)
			node.Config["slot"] = source.Targets[index].ID
		}
	}
	// Preserve former application inheritance explicitly before unifying the default.
	filtered := source.TargetDefaults[:0]
	for _, d := range source.TargetDefaults {
		if d.Target != "application" {
			filtered = append(filtered, d)
		}
	}
	source.TargetDefaults = filtered
	hasDefault := false
	for _, target := range source.Targets {
		hasDefault = hasDefault || target.Default
	}
	if !hasDefault {
		source.Targets = append(schema.DefaultWorkflowTargets(), source.Targets...)
		if err := schema.SetTargetDefault(source, "target", schema.DefaultWorkflowTargetID); err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

func legacyTargetBindings(raw []byte) (map[string]string, error) {
	var source schema.WorkflowSource
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	return transformLegacyTargets(&source)
}
