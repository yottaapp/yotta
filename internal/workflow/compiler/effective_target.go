package compiler

import (
	"fmt"

	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

// ResolveNodeCapabilityRequirements applies Workflow target defaults and
// freezes dynamic slots for one exact node contract without resolving them to
// privileged host installations.
func ResolveNodeCapabilityRequirements(
	defaults []schema.TargetDefault,
	node schema.Node,
	contract nodecontract.Contract,
) ([]capability.Requirement, error) {
	if !contract.Valid() || contract.NodeRef() != node.NodeRef {
		return nil, fmt.Errorf("node capability projection requires the exact contract")
	}
	config, err := effectiveNodeConfig(defaults, node, contract.Machine())
	if err != nil {
		return nil, err
	}
	return nodecontract.ResolveCapabilityRequirements(contract.Machine(), config)
}

func effectiveNodeConfig(defaults []schema.TargetDefault, node schema.Node, machine nodecontract.MachineContract) (map[string]any, error) {
	config := make(map[string]any, len(node.Config)+len(machine.RequirementBindings)+len(machine.ConfiguredTargets))
	for key, value := range node.Config {
		config[key] = value
	}
	bindings := make(map[string]nodecontract.RequirementBindingSpec, len(machine.RequirementBindings))
	inheritedByConfigKey := make(map[string]string)
	for _, binding := range machine.RequirementBindings {
		bindings[binding.RequirementID] = binding
	}
	for _, requirement := range machine.CapabilityRequirements {
		binding, ok := bindings[requirement.ID]
		if !ok || binding.TargetSlotConfigKey == "" {
			continue
		}
		if _, explicit := node.Config[binding.TargetSlotConfigKey]; explicit {
			continue
		}
		slot, inherited := targetDefaultSlot(defaults, requirement.TargetSlot)
		if !inherited {
			continue
		}
		if existing, assigned := inheritedByConfigKey[binding.TargetSlotConfigKey]; assigned && existing != slot {
			return nil, fmt.Errorf("target config %q resolves to multiple workflow defaults", binding.TargetSlotConfigKey)
		}
		inheritedByConfigKey[binding.TargetSlotConfigKey] = slot
		config[binding.TargetSlotConfigKey] = slot
	}
	for _, target := range machine.ConfiguredTargets {
		if _, explicit := node.Config[target.SlotConfigKey]; explicit {
			continue
		}
		slot, inherited := targetDefaultSlot(defaults, target.TargetSlot)
		if !inherited {
			continue
		}
		if existing, assigned := inheritedByConfigKey[target.SlotConfigKey]; assigned && existing != slot {
			return nil, fmt.Errorf("target config %q resolves to multiple workflow defaults", target.SlotConfigKey)
		}
		inheritedByConfigKey[target.SlotConfigKey] = slot
		config[target.SlotConfigKey] = slot
	}
	return config, nil
}

func targetDefaultSlot(defaults []schema.TargetDefault, target string) (string, bool) {
	for _, candidate := range defaults {
		if candidate.Target == target {
			return candidate.Slot, true
		}
	}
	if target == "application" {
		return targetDefaultSlot(defaults, "target")
	}
	return "", false
}
