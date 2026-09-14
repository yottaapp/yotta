import type { Node, WorkflowTarget } from '../../../../contracts/workflow/current/workflow-source'
import type { NodeProjection } from '../../../../contracts/node/current/authoring-projection'
import { isWorkflowTargetKind, targetAcceptsKinds } from './workflowTargets'

// Mutates only detached insertion nodes; the caller commits declarations and nodes together.
export function declareInsertedTargets(
  nodes: Node[],
  current: WorkflowTarget[],
  projectionFor: (node: Node) => NodeProjection | undefined,
  newID: () => string,
  label: (number: number) => string,
): WorkflowTarget[] | undefined {
  const declarations = current.map((target) => ({ ...target }))
  const groups = new Map<string, Array<{ node: Node; key: string; kinds: string[] }>>()
  for (const node of nodes) {
    const projection = projectionFor(node)
    if (
      !projection ||
      projection.nodeRef.semanticDigest !== node.nodeRef.semanticDigest ||
      projection.nodeRef.version !== node.nodeRef.version
    )
      continue
    for (const target of projection.configuredTargets ?? []) {
      if (!isWorkflowTargetKind(target.targetKinds)) continue
      const slot = node.config[target.slotConfigKey]
      if (typeof slot !== 'string' || !slot) continue
      if (current.some((role) => role.id === slot && targetAcceptsKinds(role, target.targetKinds)))
        continue
      const group = groups.get(slot) ?? []
      group.push({ node, key: target.slotConfigKey, kinds: target.targetKinds })
      groups.set(slot, group)
    }
  }
  for (const group of groups.values()) {
    const intersection = group[0]!.kinds.filter((kind) =>
      group.every((entry) => entry.kinds.includes(kind)),
    )
    if (!intersection.length) throw new Error('incompatible target uses in inserted selection')
    const kind = intersection.length === 1 ? intersection[0]! : 'automation'
    let id = newID()
    while (declarations.some((target) => target.id === id)) id = newID()
    declarations.push({
      id,
      name: label(declarations.length + 1),
      kind,
      default: declarations.length === 0,
    })
    for (const { node, key } of group) node.config[key] = id
  }
  return groups.size ? declarations : undefined
}
