import type { Node } from '../../../../contracts/workflow/current/workflow-source'
import type { WorkflowJSONValue, WorkflowPatchCommand } from '@/app/transport/workflow'
import type { EditorCommand } from './EditorSession'

interface PendingEditorCommand {
  graphId: string
  command: EditorCommand
}

export function toWorkflowPatch(pending: readonly PendingEditorCommand[]): WorkflowPatchCommand[] {
  const expanded = pending.flatMap(({ graphId, command }) => {
    if (command.kind === 'remove-graph-cascade') {
      return [
        ...command.calls.map((call) => ({
          graphId: call.parentGraphId,
          command: {
            kind: 'remove-graph-call' as const,
            callId: call.callId,
          } satisfies EditorCommand,
        })),
        {
          graphId,
          command: {
            kind: 'remove-graph' as const,
            graphId: command.graphId,
          } satisfies EditorCommand,
        },
      ]
    }
    return expandEditorCommand(command).map((expandedCommand) => ({
      graphId,
      command: expandedCommand,
    }))
  })
  const added = new Set(
    expanded.flatMap(({ graphId, command }) =>
      command.kind === 'add-node' && command.nodeId ? [nodeKey(graphId, command.nodeId)] : [],
    ),
  )
  const removed = new Set(
    expanded.flatMap(({ graphId, command }) =>
      command.kind === 'remove-node' ? [nodeKey(graphId, command.nodeId)] : [],
    ),
  )
  const canceled = new Set([...added].filter((key) => removed.has(key)))
  const compacted = expanded.filter(({ graphId, command }) => {
    if (command.kind === 'remove-node') return !canceled.has(nodeKey(graphId, command.nodeId))
    return ![...removed].some(
      (key) =>
        key.startsWith(`${graphId}\u0000`) &&
        commandReferencesNode(command, key.slice(graphId.length + 1)),
    )
  })
  const generated = new Set(
    compacted.flatMap(({ command }) =>
      command.kind === 'add-node' && command.nodeId ? [command.nodeId] : [],
    ),
  )
  const nodeRef = (nodeId: string): string => (generated.has(nodeId) ? `$${nodeId}` : nodeId)
  return compacted.map(({ graphId, command }): WorkflowPatchCommand => {
    switch (command.kind) {
      case 'rename-workflow':
        return { kind: command.kind, renameWorkflow: { name: command.name } }
      case 'set-target-default':
        return {
          kind: command.kind,
          setTargetDefault: { target: command.target, slot: command.slot },
        }
      case 'clear-target-default':
        return { kind: command.kind, clearTargetDefault: { target: command.target } }
      case 'add-state-variable':
        return {
          kind: command.kind,
          addStateVariable: {
            name: command.name,
            type: clone(command.type),
            default: jsonValue(command.defaultValue),
          },
        }
      case 'set-workflow-targets': {
        const [first, ...rest] = clone(command.targets)
        if (!first) throw new Error('workflow targets require one default')
        return { kind: command.kind, setWorkflowTargets: { targets: [first, ...rest] } }
      }
      case 'set-parameter-blocks':
        return { kind: command.kind, setParameterBlocks: { blocks: clone(command.blocks) } }
      case 'update-state-variable':
        return {
          kind: command.kind,
          updateStateVariable: {
            ...(command.parameter ? { parameter: clone(command.parameter) } : {}),
            ...(command.clearParameter ? { clearParameter: true } : {}),
            name: command.name,
            type: clone(command.type),
            default: clone(command.defaultValue) as WorkflowJSONValue,
          },
        }
      case 'remove-state-variable':
        return { kind: command.kind, removeStateVariable: { name: command.name } }
      case 'add-node':
        if (!command.nodeId) throw new Error('pending add-node command omitted node ID')
        return {
          kind: command.kind,
          addNode: {
            graphId,
            nodeTypeId: command.nodeTypeId,
            handle: command.nodeId,
            position: clone(command.position),
          },
        }
      case 'upgrade-node-contract':
        return {
          kind: command.kind,
          upgradeNodeContract: { graphId, nodeId: nodeRef(command.nodeId) },
        }
      case 'remove-node':
        return { kind: command.kind, removeNode: { graphId, nodeId: nodeRef(command.nodeId) } }
      case 'move-node':
        return {
          kind: command.kind,
          moveNode: { graphId, nodeId: nodeRef(command.nodeId), position: clone(command.position) },
        }
      case 'move-nodes':
        throw new Error('move-nodes must be expanded before persistence')
      case 'set-node-label':
        return {
          kind: command.kind,
          setNodeLabel: { graphId, nodeId: nodeRef(command.nodeId), label: command.label },
        }
      case 'set-node-disabled':
        return {
          kind: command.kind,
          setNodeDisabled: {
            graphId,
            nodeId: nodeRef(command.nodeId),
            disabled: command.disabled,
          },
        }
      case 'set-config':
        return {
          kind: command.kind,
          setConfig: {
            graphId,
            nodeId: nodeRef(command.nodeId),
            fieldId: command.fieldId,
            value: jsonValue(command.value),
          },
        }
      case 'clear-config':
        return {
          kind: command.kind,
          clearConfig: { graphId, nodeId: nodeRef(command.nodeId), fieldId: command.fieldId },
        }
      case 'bind-value':
        return {
          kind: command.kind,
          bindValue: {
            graphId,
            nodeId: nodeRef(command.nodeId),
            portId: command.portId,
            value: jsonValue(command.value),
          },
        }
      case 'bind-blob':
        return {
          kind: command.kind,
          bindBlob: {
            graphId,
            nodeId: nodeRef(command.nodeId),
            portId: command.portId,
            blob: clone(command.blob),
          },
        }
      case 'bind-resource':
        return {
          kind: command.kind,
          bindResource: {
            graphId,
            nodeId: nodeRef(command.nodeId),
            portId: command.portId,
            resource: clone(command.resource),
          },
        }
      case 'add-resource':
        return {
          kind: command.kind,
          addResource: { resource: clone(command.resource) },
        }
      case 'replace-resource':
        return {
          kind: command.kind,
          replaceResource: {
            resourceId: command.resourceId,
            resource: clone(command.resource),
          },
        }
      case 'update-resource-metadata':
        return {
          kind: command.kind,
          updateResourceMetadata: {
            resourceId: command.resourceId,
            name: command.name,
            description: command.description,
            category: command.category,
            tags: [...command.tags],
          },
        }
      case 'remove-resource':
        return {
          kind: command.kind,
          removeResource: { resourceId: command.resourceId },
        }
      case 'bind-default':
        return {
          kind: command.kind,
          bindDefault: {
            graphId,
            nodeId: nodeRef(command.nodeId),
            portId: command.portId,
          },
        }
      case 'clear-binding':
        return {
          kind: command.kind,
          clearBinding: {
            graphId,
            nodeId: nodeRef(command.nodeId),
            portId: command.portId,
          },
        }
      case 'connect':
        return {
          kind: command.kind,
          connect: {
            graphId,
            edge: {
              channel: command.edge.channel,
              from: { nodeId: nodeRef(command.edge.from.nodeId), portId: command.edge.from.portId },
              to: { nodeId: nodeRef(command.edge.to.nodeId), portId: command.edge.to.portId },
            },
          },
        }
      case 'disconnect':
        return {
          kind: command.kind,
          disconnect: {
            graphId,
            edge: {
              channel: command.edge.channel,
              from: { nodeId: nodeRef(command.edge.from.nodeId), portId: command.edge.from.portId },
              to: { nodeId: nodeRef(command.edge.to.nodeId), portId: command.edge.to.portId },
            },
          },
        }
      case 'add-graph':
        return { kind: command.kind, addGraph: { graph: clone(command.graph) } }
      case 'rename-graph':
        return {
          kind: command.kind,
          renameGraph: { graphId: command.graphId, name: command.name },
        }
      case 'remove-graph':
        return { kind: command.kind, removeGraph: { graphId: command.graphId } }
      case 'remove-graph-cascade':
        throw new Error('remove-graph-cascade must be expanded before persistence')
      case 'update-graph-interface':
        return {
          kind: command.kind,
          updateGraphInterface: {
            graphId,
            inputs: command.inputs.map((port) => ({
              ...clone(port),
              nodeId: nodeRef(port.nodeId),
            })),
            outputs: command.outputs.map((port) => ({
              ...clone(port),
              nodeId: nodeRef(port.nodeId),
            })),
            entries: command.entries.map((entry) => ({
              ...clone(entry),
              nodeId: nodeRef(entry.nodeId),
            })),
            exits: command.exits.map((exit) => ({
              ...clone(exit),
              endpoint: { ...clone(exit.endpoint), nodeId: nodeRef(exit.endpoint.nodeId) },
            })),
          },
        }
      case 'add-graph-call':
        return { kind: command.kind, addGraphCall: { graphId, call: clone(command.call) } }
      case 'update-graph-call':
        return { kind: command.kind, updateGraphCall: { graphId, call: clone(command.call) } }
      case 'remove-graph-call':
        return { kind: command.kind, removeGraphCall: { graphId, callId: command.callId } }
      case 'fork-graph-call':
        throw new Error('fork-graph-call must be expanded before persistence')
      case 'expand-graph-call':
        throw new Error('expand-graph-call must be expanded before persistence')
      case 'add-annotation':
        return {
          kind: command.kind,
          addAnnotation: { graphId, annotation: clone(command.annotation) },
        }
      case 'update-annotation':
        return {
          kind: command.kind,
          updateAnnotation: { graphId, annotation: clone(command.annotation) },
        }
      case 'remove-annotation':
        return {
          kind: command.kind,
          removeAnnotation: { graphId, annotationId: command.annotationId },
        }
      case 'set-edge-reroutes':
        return {
          kind: command.kind,
          setEdgeReroutes: {
            graphId,
            edge: clone(command.edge),
            reroutes: clone(command.reroutes),
          },
        }
      case 'collapse-selection':
        return {
          kind: command.kind,
          collapseSelection: {
            graphId,
            subgraphId: command.subgraphId,
            callId: command.callId,
            name: command.name,
            nodeIds: [...command.nodeIds],
            position: clone(command.position),
          },
        }
      case 'insert-connected-node':
      case 'promote-output-to-state':
        throw new Error(`${command.kind} must be expanded before persistence`)
      case 'remove-nodes':
        throw new Error('remove-nodes must be expanded before persistence')
      case 'insert-node-selection':
        throw new Error('insert-node-selection must be expanded before persistence')
      case 'batch':
        throw new Error('batch must be expanded before persistence')
    }
  })
}

function nodeKey(graphId: string, nodeId: string): string {
  return `${graphId}\u0000${nodeId}`
}

function commandReferencesNode(command: EditorCommand, nodeId: string): boolean {
  switch (command.kind) {
    case 'add-node':
    case 'upgrade-node-contract':
    case 'remove-node':
    case 'move-node':
    case 'set-node-label':
    case 'set-node-disabled':
    case 'set-config':
    case 'clear-config':
    case 'bind-value':
    case 'bind-blob':
    case 'bind-resource':
    case 'bind-default':
    case 'clear-binding':
      return command.nodeId === nodeId
    case 'connect':
    case 'disconnect':
    case 'set-edge-reroutes':
      return command.edge.from.nodeId === nodeId || command.edge.to.nodeId === nodeId
    case 'update-graph-interface':
      return (
        command.inputs.some((port) => port.nodeId === nodeId) ||
        command.outputs.some((port) => port.nodeId === nodeId) ||
        command.entries.some((entry) => entry.nodeId === nodeId) ||
        command.exits.some((exit) => exit.endpoint.nodeId === nodeId)
      )
    default:
      return false
  }
}

export function expandEditorCommand(command: EditorCommand): EditorCommand[] {
  switch (command.kind) {
    case 'batch':
      return command.commands.flatMap(expandEditorCommand)
    case 'insert-connected-node':
      return [
        {
          kind: 'add-node',
          nodeTypeId: command.nodeTypeId,
          nodeId: command.nodeId,
          position: command.position,
        },
        { kind: 'connect', edge: command.edge },
      ]
    case 'promote-output-to-state':
      return [
        {
          kind: 'add-state-variable',
          name: command.name,
          type: clone(command.type),
          defaultValue: clone(command.defaultValue),
        },
        {
          kind: 'add-node',
          nodeTypeId: command.nodeTypeId,
          nodeId: command.nodeId,
          position: clone(command.position),
        },
        {
          kind: 'set-config',
          nodeId: command.nodeId,
          fieldId: command.stateConfigKey,
          value: command.name,
        },
        { kind: 'connect', edge: clone(command.edge) },
      ]
    case 'move-nodes':
      return command.positions.map(({ nodeId, position }) => ({
        kind: 'move-node',
        nodeId,
        position,
      }))
    case 'remove-nodes':
      return command.nodeIds.map((nodeId) => ({ kind: 'remove-node', nodeId }))
    case 'insert-node-selection':
      return [
        ...command.nodes.flatMap(nodeCommands),
        ...command.calls.map((call): EditorCommand => ({ kind: 'add-graph-call', call })),
        ...command.annotations.map((annotation): EditorCommand => ({
          kind: 'add-annotation',
          annotation,
        })),
        ...command.edges.map((edge): EditorCommand => ({ kind: 'connect', edge })),
      ]
    case 'fork-graph-call':
      return [
        { kind: 'add-graph', graph: command.graph },
        { kind: 'update-graph-call', call: command.call },
      ]
    case 'expand-graph-call':
      return [
        { kind: 'remove-graph-call', callId: command.callId },
        ...command.nodes.flatMap(nodeCommands),
        ...command.calls.map((call): EditorCommand => ({ kind: 'add-graph-call', call })),
        ...command.annotations.map((annotation): EditorCommand => ({
          kind: 'add-annotation',
          annotation,
        })),
        ...command.edges.map((edge): EditorCommand => ({ kind: 'connect', edge })),
      ]
    default:
      return [command]
  }
}

function nodeCommands(node: Node): EditorCommand[] {
  const commands: EditorCommand[] = [
    {
      kind: 'add-node',
      nodeTypeId: node.nodeRef.nodeTypeId,
      nodeId: node.id,
      position: node.position,
    },
  ]
  if (node.label) commands.push({ kind: 'set-node-label', nodeId: node.id, label: node.label })
  if (node.disabled) {
    commands.push({ kind: 'set-node-disabled', nodeId: node.id, disabled: true })
  }
  for (const [fieldId, value] of Object.entries(node.config)) {
    commands.push({ kind: 'set-config', nodeId: node.id, fieldId, value })
  }
  for (const [portId, binding] of Object.entries(node.bindings)) {
    if (binding.kind === 'value') {
      commands.push({ kind: 'bind-value', nodeId: node.id, portId, value: binding.value })
    } else if (binding.kind === 'blob' && binding.blob) {
      commands.push({ kind: 'bind-blob', nodeId: node.id, portId, blob: binding.blob })
    } else if (binding.kind === 'resource' && binding.resource) {
      commands.push({
        kind: 'bind-resource',
        nodeId: node.id,
        portId,
        resource: binding.resource,
      })
    } else if (binding.kind === 'default') {
      commands.push({ kind: 'bind-default', nodeId: node.id, portId })
    }
  }
  return commands
}

function clone<T>(value: T): T {
  return structuredClone(value)
}

function jsonValue(value: unknown): WorkflowJSONValue {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new Error('authoring value must be a finite JSON number')
    return value
  }
  if (Array.isArray(value)) return value.map(jsonValue)
  if (typeof value === 'object') {
    const result: Record<string, WorkflowJSONValue> = {}
    for (const [key, member] of Object.entries(value)) result[key] = jsonValue(member)
    return result
  }
  throw new Error('authoring value must be JSON data')
}
