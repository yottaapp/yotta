import type {
  Graph,
  YottaWorkflowSource,
} from '../../../../contracts/workflow/current/workflow-source'
import type {
  NodeProjection,
  TypeProjection,
} from '../../../../contracts/node/current/authoring-projection'
import type { EditorCommand } from './EditorSession'
import { graphCallSites } from './subgraphLifecycle'
import { expandEditorCommand } from './editorCommandPersistence'
import {
  applyCompatibleNodeContractUpgrade,
  pruneConfigDependentTopology,
  requireDataInput,
  requireNode,
  requireProjection,
  validateEdge,
} from './editorTypeProjection'
import {
  collapseGraphSelection,
  graphElementExists,
  normalizeGraph,
  sameEdge,
} from './editorGraphModel'
import {
  normalizeTextSet,
  normalizeWorkflowResource,
  requireWorkflowResourceBinding,
  validBlob,
  workflowResourceReferenceCount,
} from './editorResourceModel'

export function applyCommand(
  source: YottaWorkflowSource,
  graph: Graph,
  command: EditorCommand,
  projections: Map<string, NodeProjection>,
  types: Map<string, TypeProjection>,
): void {
  const expanded = expandEditorCommand(command)
  if (expanded.length !== 1 || expanded[0] !== command) {
    for (const primitive of expanded) applyCommand(source, graph, primitive, projections, types)
    return
  }
  switch (command.kind) {
    case 'rename-workflow': {
      const name = command.name.trim()
      if (!name) throw new Error('workflow name is required')
      source.workflow.name = name
      return
    }
    case 'set-target-default': {
      const target = command.target.trim()
      const slot = command.slot.trim()
      if (!/^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$/.test(target))
        throw new Error('target default name is invalid')
      if (!/^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$/.test(slot))
        throw new Error('target default slot is invalid')
      if (source.targets?.length && ['target', 'application'].includes(target)) {
        if (!source.targets.some((role) => role.id === slot))
          throw new Error('unknown workflow target')
        applyCommand(
          source,
          graph,
          {
            kind: 'set-workflow-targets',
            targets: source.targets.map((role) => ({ ...role, default: role.id === slot })),
          },
          projections,
          types,
        )
        return
      }
      const defaults = (source.targetDefaults ??= [])
      const existing = defaults.find((candidate) => candidate.target === target)
      if (existing) existing.slot = slot
      else defaults.push({ target, slot })
      defaults.sort((left, right) => left.target.localeCompare(right.target))
      return
    }
    case 'clear-target-default':
      if (source.targets?.length && ['target', 'application'].includes(command.target))
        throw new Error('workflow default target cannot be cleared')
      source.targetDefaults = source.targetDefaults?.filter(
        (candidate) => candidate.target !== command.target,
      )
      if (!source.targetDefaults?.length) delete source.targetDefaults
      return
    case 'add-state-variable': {
      const name = command.name.trim()
      if (!/^[A-Za-z0-9_][A-Za-z0-9._-]*$/.test(name) || name.length > 128)
        throw new Error('state variable name is invalid')
      if (source.variables.some((variable) => variable.name === name))
        throw new Error(`duplicate state variable ${name}`)
      if (source.variables.length >= 4096) throw new Error('state variable budget exceeded')
      source.variables.push({
        name,
        type: clone(command.type),
        default: clone(command.defaultValue),
      })
      return
    }
    case 'set-workflow-targets': {
      const targets = clone(command.targets)
      if (
        !targets.length ||
        targets.filter((target) => target.default).length !== 1 ||
        new Set(targets.map((target) => target.id)).size !== targets.length
      )
        throw new Error('workflow targets require unique IDs and one default')
      const removed = (source.targets ?? []).filter(
        (target) => !targets.some((next) => next.id === target.id),
      )
      if (
        removed.some((target) =>
          source.graphs.some((graph) => graph.nodes.some((node) => node.config.slot === target.id)),
        )
      )
        throw new Error('workflow target is still referenced')
      source.targets = targets
      source.targetDefaults =
        source.targetDefaults?.filter((target) => target.target !== 'application') ?? []
      const defaults = source.targetDefaults
      const current = defaults.find((target) => target.target === 'target')
      const slot = targets.find((target) => target.default)!.id
      if (current) current.slot = slot
      else defaults.push({ target: 'target', slot })
      return
    }
    case 'set-parameter-blocks':
      source.parameterBlocks = clone(command.blocks)
      return
    case 'update-state-variable': {
      const variable = source.variables.find((candidate) => candidate.name === command.name)
      if (!variable) throw new Error(`state variable ${command.name} does not exist`)
      variable.type = clone(command.type)
      variable.default = clone(command.defaultValue)
      if (command.clearParameter) delete variable.parameter
      else if (command.parameter) variable.parameter = clone(command.parameter)
      return
    }
    case 'remove-state-variable': {
      if (!source.variables.some((variable) => variable.name === command.name))
        throw new Error(`state variable ${command.name} does not exist`)
      const referenced = source.graphs.some((candidate) =>
        candidate.nodes.some(
          (node) =>
            node.nodeRef.nodeTypeId.includes('/nodes/state/') &&
            node.config.variable === command.name,
        ),
      )
      if (referenced) throw new Error(`state variable ${command.name} is still referenced`)
      source.variables = source.variables.filter((variable) => variable.name !== command.name)
      return
    }
    case 'add-node': {
      const projection = projections.get(command.nodeTypeId)
      if (!projection) throw new Error(`unknown Node Contract ${command.nodeTypeId}`)
      if (!command.nodeId) throw new Error('resolved add-node command omitted node ID')
      const id = command.nodeId
      if (graph.nodes.some((node) => node.id === id)) throw new Error(`duplicate node ${id}`)
      const config = Object.fromEntries(
        projection.configFields
          .filter((field) => field.hasDefault)
          .map((field) => [field.id, clone(field.default)]),
      )
      const bindings = Object.fromEntries(
        projection.dataInputs
          .filter((port) => port.hasDefault)
          .map((port) => [port.id, { kind: 'default' as const }]),
      )
      graph.nodes.push({
        id,
        nodeRef: clone(projection.nodeRef),
        position: clone(command.position),
        config,
        bindings,
      })
      return
    }
    case 'upgrade-node-contract': {
      const node = requireNode(graph, command.nodeId)
      const projection = projections.get(node.nodeRef.nodeTypeId)
      if (!projection || !applyCompatibleNodeContractUpgrade(graph, node, projection)) {
        throw new Error(`node ${node.id} cannot be upgraded without losing authoring data`)
      }
      return
    }
    case 'remove-node':
      requireNode(graph, command.nodeId)
      graph.nodes = graph.nodes.filter((node) => node.id !== command.nodeId)
      graph.edges = graph.edges.filter(
        (edge) => edge.from.nodeId !== command.nodeId && edge.to.nodeId !== command.nodeId,
      )
      return
    case 'move-node':
      requireNode(graph, command.nodeId).position = clone(command.position)
      return
    case 'move-nodes':
      throw new Error('move-nodes expansion failed')
    case 'set-node-label': {
      const node = requireNode(graph, command.nodeId)
      const label = command.label.trim()
      if (label) node.label = label
      else delete node.label
      return
    }
    case 'set-node-disabled':
      requireNode(graph, command.nodeId).disabled = command.disabled || undefined
      return
    case 'set-config': {
      const node = requireNode(graph, command.nodeId)
      node.config[command.fieldId] = clone(command.value)
      pruneConfigDependentTopology(graph, node, projections)
      return
    }
    case 'clear-config': {
      const node = requireNode(graph, command.nodeId)
      const projection = requireProjection(node, projections)
      const field = projection.configFields.find((candidate) => candidate.id === command.fieldId)
      const inherited = [
        ...(projection.configuredTargets ?? []).map((target) => ({
          key: target.slotConfigKey,
          target: target.targetSlot,
        })),
        ...projection.capabilities.map((target) => ({
          key: target.targetSlotConfigKey,
          target: target.targetSlot,
        })),
      ].some(
        (target) =>
          target.key === command.fieldId &&
          source.targetDefaults?.some(
            (value) =>
              (value.target === target.target ||
                (target.target === 'application' && value.target === 'target')) &&
              value.slot,
          ),
      )
      if (field?.hasDefault && !inherited) node.config[command.fieldId] = clone(field.default)
      else delete node.config[command.fieldId]
      pruneConfigDependentTopology(graph, node, projections)
      return
    }
    case 'bind-value': {
      const node = requireNode(graph, command.nodeId)
      requireDataInput(node, command.portId, projections)
      node.bindings[command.portId] = { kind: 'value', value: clone(command.value) }
      graph.edges = graph.edges.filter(
        (edge) =>
          !(
            edge.channel === 'data' &&
            edge.to.nodeId === command.nodeId &&
            edge.to.portId === command.portId
          ),
      )
      return
    }
    case 'bind-blob': {
      const node = requireNode(graph, command.nodeId)
      const port = requireDataInput(node, command.portId, projections)
      if (!port.type.representations.some((representation) => representation.kind === 'blob-ref'))
        throw new Error(`port ${command.portId} does not accept BlobRef`)
      if (!validBlob(command.blob)) throw new Error('BlobRef is invalid')
      node.bindings[command.portId] = { kind: 'blob', blob: clone(command.blob) }
      graph.edges = graph.edges.filter(
        (edge) =>
          !(
            edge.channel === 'data' &&
            edge.to.nodeId === command.nodeId &&
            edge.to.portId === command.portId
          ),
      )
      return
    }
    case 'bind-resource': {
      const node = requireNode(graph, command.nodeId)
      requireDataInput(node, command.portId, projections)
      requireWorkflowResourceBinding(source, command.resource)
      node.bindings[command.portId] = {
        kind: 'resource',
        resource: clone(command.resource),
      }
      graph.edges = graph.edges.filter(
        (edge) =>
          !(
            edge.channel === 'data' &&
            edge.to.nodeId === command.nodeId &&
            edge.to.portId === command.portId
          ),
      )
      return
    }
    case 'add-resource': {
      if (source.resources.some((resource) => resource.id === command.resource.id))
        throw new Error(`workflow resource ${command.resource.id} already exists`)
      source.resources.push(normalizeWorkflowResource(command.resource))
      source.resources.sort((left, right) => left.id.localeCompare(right.id))
      return
    }
    case 'replace-resource': {
      const index = source.resources.findIndex((candidate) => candidate.id === command.resourceId)
      if (index < 0) throw new Error(`workflow resource ${command.resourceId} does not exist`)
      if (command.resource.id !== command.resourceId)
        throw new Error('workflow resource replacement must preserve identity')
      if (command.resource.kind !== source.resources[index]!.kind)
        throw new Error('workflow resource replacement must preserve kind')
      source.resources[index] = normalizeWorkflowResource(command.resource)
      return
    }
    case 'update-resource-metadata': {
      const resource = source.resources.find((candidate) => candidate.id === command.resourceId)
      if (!resource) throw new Error(`workflow resource ${command.resourceId} does not exist`)
      const name = command.name.trim()
      if (!name) throw new Error('workflow resource name is required')
      resource.name = name
      resource.description = command.description.trim() || undefined
      resource.category = command.category.trim() || undefined
      const tags = normalizeTextSet(command.tags)
      resource.tags = tags.length ? tags : undefined
      return
    }
    case 'remove-resource': {
      if (!source.resources.some((resource) => resource.id === command.resourceId))
        throw new Error(`workflow resource ${command.resourceId} does not exist`)
      if (workflowResourceReferenceCount(source, command.resourceId) !== 0)
        throw new Error('workflow resource is still in use')
      source.resources = source.resources.filter((resource) => resource.id !== command.resourceId)
      return
    }
    case 'bind-default': {
      const node = requireNode(graph, command.nodeId)
      const port = requireDataInput(node, command.portId, projections)
      if (!port.hasDefault) throw new Error(`port ${command.portId} has no declared default`)
      node.bindings[command.portId] = { kind: 'default' }
      return
    }
    case 'clear-binding':
      delete requireNode(graph, command.nodeId).bindings[command.portId]
      return
    case 'connect':
      validateEdge(source, graph, command.edge, projections, types)
      if (command.edge.channel === 'data') {
        const target = graph.nodes.find((node) => node.id === command.edge.to.nodeId)
        const targetCall = graph.calls!.find((call) => call.id === command.edge.to.nodeId)
        if (target) delete target.bindings[command.edge.to.portId]
        else if (targetCall) delete targetCall.bindings[command.edge.to.portId]
        graph.edges = graph.edges.filter(
          (edge) =>
            !(
              edge.channel === 'data' &&
              edge.to.nodeId === command.edge.to.nodeId &&
              edge.to.portId === command.edge.to.portId
            ),
        )
      }
      if (!graph.edges.some((edge) => sameEdge(edge, command.edge))) {
        graph.edges.push(clone(command.edge))
      }
      return
    case 'add-graph':
      if (source.graphs.some((candidate) => candidate.id === command.graph.id))
        throw new Error(`duplicate graph ${command.graph.id}`)
      source.graphs.push(normalizeGraph(clone(command.graph)))
      return
    case 'rename-graph': {
      const target = source.graphs.find((candidate) => candidate.id === command.graphId)
      if (!target) throw new Error(`graph ${command.graphId} does not exist`)
      target.name = command.name.trim() || undefined
      return
    }
    case 'remove-graph':
      if (command.graphId === source.entryGraph) throw new Error('entry graph cannot be removed')
      if (!source.graphs.some((candidate) => candidate.id === command.graphId))
        throw new Error(`graph ${command.graphId} does not exist`)
      if (
        source.graphs.some((candidate) =>
          candidate.calls?.some((call) => call.graphId === command.graphId),
        )
      )
        throw new Error(`graph ${command.graphId} is still referenced`)
      source.graphs.splice(
        source.graphs.findIndex((candidate) => candidate.id === command.graphId),
        1,
      )
      return
    case 'remove-graph-cascade': {
      if (command.graphId === source.entryGraph) throw new Error('entry graph cannot be removed')
      if (!source.graphs.some((candidate) => candidate.id === command.graphId))
        throw new Error(`graph ${command.graphId} does not exist`)
      const actual = graphCallSites(source, command.graphId)
      const expected = new Set(command.calls.map((call) => `${call.parentGraphId}\0${call.callId}`))
      if (
        actual.length !== expected.size ||
        actual.some((call) => !expected.has(`${call.parentGraphId}\0${call.callId}`))
      )
        throw new Error('subgraph call sites changed before cascade deletion')
      for (const call of actual) {
        const parent = source.graphs.find((candidate) => candidate.id === call.parentGraphId)!
        parent.calls = (parent.calls ?? []).filter((candidate) => candidate.id !== call.callId)
        parent.edges = parent.edges.filter(
          (edge) => edge.from.nodeId !== call.callId && edge.to.nodeId !== call.callId,
        )
      }
      source.graphs.splice(
        source.graphs.findIndex((candidate) => candidate.id === command.graphId),
        1,
      )
      return
    }
    case 'update-graph-interface':
      if (graph.kind !== 'subgraph') throw new Error('only subgraphs have a callable interface')
      {
        const inputs = new Set(command.inputs.map((port) => port.id))
        const outputs = new Set(command.outputs.map((port) => port.id))
        const exits = new Set(command.exits.map((exit) => exit.id))
        for (const caller of source.graphs) {
          for (const call of caller.calls ?? []) {
            if (call.graphId !== graph.id) continue
            if (Object.keys(call.bindings).some((portId) => !inputs.has(portId)))
              throw new Error('removed graph input is still bound by a call')
            if (
              caller.edges.some(
                (edge) =>
                  (edge.to.nodeId === call.id &&
                    edge.channel === 'data' &&
                    !inputs.has(edge.to.portId)) ||
                  (edge.from.nodeId === call.id &&
                    edge.channel === 'data' &&
                    !outputs.has(edge.from.portId)) ||
                  (edge.from.nodeId === call.id &&
                    edge.channel !== 'data' &&
                    !exits.has(edge.from.portId)),
              )
            )
              throw new Error('removed graph port is still connected by a call')
          }
        }
      }
      graph.inputs = clone(command.inputs)
      graph.outputs = clone(command.outputs)
      graph.entries = clone(command.entries)
      graph.exits = clone(command.exits)
      return
    case 'add-graph-call':
      if (graphElementExists(graph, command.call.id))
        throw new Error(`duplicate graph element ${command.call.id}`)
      graph.calls!.push(clone(command.call))
      return
    case 'update-graph-call': {
      const index = graph.calls!.findIndex((call) => call.id === command.call.id)
      if (index < 0) throw new Error(`call ${command.call.id} does not exist`)
      graph.calls![index] = clone(command.call)
      return
    }
    case 'remove-graph-call':
      if (!graph.calls!.some((call) => call.id === command.callId))
        throw new Error(`call ${command.callId} does not exist`)
      graph.calls = graph.calls!.filter((call) => call.id !== command.callId)
      graph.edges = graph.edges.filter(
        (edge) => edge.from.nodeId !== command.callId && edge.to.nodeId !== command.callId,
      )
      return
    case 'fork-graph-call':
    case 'expand-graph-call':
      throw new Error(`${command.kind} must be expanded before application`)
    case 'add-annotation':
      if (graph.annotations!.some((annotation) => annotation.id === command.annotation.id))
        throw new Error(`annotation ${command.annotation.id} already exists`)
      graph.annotations!.push(clone(command.annotation))
      return
    case 'update-annotation': {
      const index = graph.annotations!.findIndex(
        (annotation) => annotation.id === command.annotation.id,
      )
      if (index < 0) throw new Error(`annotation ${command.annotation.id} does not exist`)
      graph.annotations![index] = clone(command.annotation)
      return
    }
    case 'remove-annotation':
      graph.annotations = graph.annotations!.filter(
        (annotation) => annotation.id !== command.annotationId,
      )
      return
    case 'set-edge-reroutes': {
      const edge = graph.edges.find((candidate) => sameEdge(candidate, command.edge))
      if (!edge) throw new Error('edge does not exist')
      edge.presentation = command.reroutes.length
        ? { reroutes: clone(command.reroutes) }
        : undefined
      return
    }
    case 'collapse-selection':
      collapseGraphSelection(source, graph, command, projections)
      return
    case 'insert-connected-node':
    case 'promote-output-to-state':
    case 'remove-nodes':
    case 'insert-node-selection':
    case 'batch':
      throw new Error(`${command.kind} expansion failed`)
    case 'disconnect':
      graph.edges = graph.edges.filter((edge) => !sameEdge(edge, command.edge))
  }
}

function clone<T>(value: T): T {
  return structuredClone(value)
}
