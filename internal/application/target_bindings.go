package application

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/yottaapp/yotta/internal/automation/target"
	"github.com/yottaapp/yotta/internal/targetruntime"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

const (
	CodeTargetBindingMissing = "WORKFLOW_TARGET_BINDING_MISSING"
	CodeTargetBindingInvalid = "WORKFLOW_TARGET_BINDING_INVALID"
	CodeTargetKindMismatch   = "WORKFLOW_TARGET_KIND_MISMATCH"
	CodeTargetUndeclared     = "WORKFLOW_TARGET_UNDECLARED"
)

func automationTargetKind(kind string) bool {
	return kind == target.KindDesktopWindow || kind == target.KindAndroidDevice || kind == target.KindBrowserCDP
}
func workflowTargetKind(kind string) bool {
	return kind == "configured-application" || automationTargetKind(kind)
}
func targetKindMatches(expected, actual string) bool {
	return expected == actual || expected == "automation" && automationTargetKind(actual)
}
func targetDiagnostic(code string, role schema.WorkflowTarget, index int) schema.Diagnostic {
	return schema.Diagnostic{Code: code, Severity: schema.SeverityError,
		FieldPath: []string{"targets", strconv.Itoa(index)}, Params: map[string]any{
			"targetId": role.ID, "targetName": role.Name, "parameterId": schema.TargetParameterPrefix + role.ID, "parameterLabel": role.Name,
		}}
}

// checkTargetBindings validates only the requested roles. Save checks explicitly
// supplied bindings; Run checks roles actually referenced by node contracts.
func checkTargetBindings(source schema.WorkflowSource, values map[string]json.RawMessage, snapshot targetruntime.Snapshot, used map[string]bool) (map[string]string, []schema.Diagnostic) {
	bindings := map[string]string{}
	slots := snapshot.Slots()
	var diagnostics []schema.Diagnostic
	for index, role := range source.Targets {
		if used != nil && !used[role.ID] {
			continue
		}
		raw, exists := values[schema.TargetParameterPrefix+role.ID]
		var selected *string
		if exists && (json.Unmarshal(raw, &selected) != nil || selected == nil) {
			diagnostics = append(diagnostics, targetDiagnostic(CodeTargetBindingInvalid, role, index))
			continue
		}
		var slot string
		if selected != nil {
			slot = *selected
		}
		if slot == "" {
			if used != nil {
				diagnostics = append(diagnostics, targetDiagnostic(CodeTargetBindingMissing, role, index))
			}
			continue
		}
		if !slices.Contains(slots, slot) {
			diagnostics = append(diagnostics, targetDiagnostic(CodeTargetBindingMissing, role, index))
			continue
		}
		actual := snapshot.Configuration(slot).Kind
		if !targetKindMatches(role.Kind, actual) {
			d := targetDiagnostic(CodeTargetKindMismatch, role, index)
			d.Params["expectedKind"] = role.Kind
			d.Params["actualKind"] = actual
			diagnostics = append(diagnostics, d)
			continue
		}
		bindings[role.ID] = slot
	}
	return bindings, diagnostics
}

// bindWorkflowTargets projects portable role references onto a frozen device
// snapshot once after active Program projection, before image preparation. Source is untouched.
func (a *Application) bindWorkflowTargets(source schema.WorkflowSource, values map[string]json.RawMessage, snapshot targetruntime.Snapshot, program compiler.ProgramSnapshot) (targetruntime.Snapshot, []schema.Diagnostic, error) {
	if len(source.Targets) == 0 {
		return snapshot, nil, nil
	}
	active := activeSourceNodes(program)
	roles := make([]string, 0, len(source.Targets))
	declared := map[string]schema.WorkflowTarget{}
	for _, role := range source.Targets {
		roles = append(roles, role.ID)
		declared[role.ID] = role
	}
	used := map[string]bool{}
	type reference struct {
		slot, node, graph, key string
		kinds                  []string
	}
	var refs []reference
	var diagnostics []schema.Diagnostic
	for _, graph := range source.Graphs {
		for _, node := range graph.Nodes {
			if !active[graph.ID][node.ID] {
				continue
			}
			entry, ok := a.catalog.Lookup(node.NodeRef.NodeTypeID)
			if !ok || entry.Contract.NodeRef() != node.NodeRef {
				continue
			} // compiler diagnoses missing/exact contract
			for _, spec := range entry.Contract.Machine().ConfiguredTargets {
				managed := false
				for _, kind := range spec.TargetKinds {
					managed = managed || workflowTargetKind(kind)
				}
				if !managed {
					continue
				}
				slot, _ := node.Config[spec.SlotConfigKey].(string)
				if _, explicit := node.Config[spec.SlotConfigKey]; !explicit {
					slot, _ = schema.TargetDefaultSlot(source, spec.TargetSlot)
				}
				role, ok := declared[slot]
				if !ok {
					d := targetDiagnostic(CodeTargetUndeclared, schema.WorkflowTarget{ID: slot}, 0)
					d.NodeID = node.ID
					d.GraphPath = []string{graph.ID}
					d.FieldPath = []string{"config", spec.SlotConfigKey}
					diagnostics = append(diagnostics, d)
					continue
				}
				used[role.ID] = true
				refs = append(refs, reference{slot: slot, node: node.ID, graph: graph.ID, key: spec.SlotConfigKey, kinds: spec.TargetKinds})
			}
		}
	}
	bindings, checked := checkTargetBindings(source, values, snapshot, used)
	for index := range checked {
		for _, ref := range refs {
			if ref.slot == checked[index].Params["targetId"] {
				checked[index].NodeID = ref.node
				checked[index].GraphPath = []string{ref.graph}
				break
			}
		}
	}
	diagnostics = append(diagnostics, checked...)
	for _, ref := range refs {
		slot := bindings[ref.slot]
		if slot == "" {
			continue
		}
		actual := snapshot.Configuration(slot).Kind
		if !slices.Contains(ref.kinds, actual) {
			d := targetDiagnostic(CodeTargetKindMismatch, declared[ref.slot], 0)
			d.NodeID = ref.node
			d.GraphPath = []string{ref.graph}
			d.FieldPath = []string{"config", ref.key}
			d.Params["expectedKind"] = ref.kinds[0]
			d.Params["actualKind"] = actual
			diagnostics = append(diagnostics, d)
		}
	}
	if schema.HasErrors(diagnostics) {
		return snapshot, diagnostics, nil
	}
	bound, err := snapshot.Bind(roles, bindings)
	return bound, diagnostics, err
}

func activeSourceNodes(program compiler.ProgramSnapshot) map[string]map[string]bool {
	active := map[string]map[string]bool{}
	for _, node := range program.Nodes() {
		if active[node.GraphID] == nil {
			active[node.GraphID] = map[string]bool{}
		}
		active[node.GraphID][node.ID] = true
	}
	return active
}
