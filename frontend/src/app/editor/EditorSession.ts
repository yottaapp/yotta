import { declareInsertedTargets } from './insertedWorkflowTargets'
import { i18n } from '@/i18n'
import type {
  Parameter,
  ParameterBlock,
  WorkflowTarget,
  Edge,
  Graph,
  GraphCall,
  Annotation,
  InputBinding,
  Node,
  BlobRef,
  ResourceBinding,
  WorkflowResource,
  NodePackageDependency,
  YottaWorkflowSource,
} from '../../../../contracts/workflow/current/workflow-source'
import type {
  NodeProjection,
  PortProjection,
  TypeExpression,
  TypeProjection,
  YottaNodeAuthoringProjection,
} from '../../../../contracts/node/current/authoring-projection'
import type {
  CompileView,
  RunView,
  SourceView,
  WorkflowTransport,
  DebugBreakpoint,
  DebugSnapshot,
} from '@/app/transport/workflow'
import { runStartOutcome, type RunStartOutcome } from '@/app/run/runReadiness'

export const DEFAULT_ANNOTATION_SIZE = { width: 260, height: 140 } as const
import {
  assignable,
  projectedConnectionCompatibility,
  type ConversionCandidatePlan,
  type ConnectionCompatibility,
} from './connectionCompatibility'
import type { ParsedHandle } from './graphHandles'
import type { GraphBoundaryBinding, GraphBoundaryKey } from './workflowGraphBoundary'
import {
  addGraphInterfaceCandidate,
  graphInterfaceReferences,
  inferGraphInterface,
  moveGraphInterfaceItem,
  projectGraphInterfaceCandidates,
  removeGraphInterfaceItem,
  renameGraphInterfaceItem,
  type GraphInterfaceCandidate,
  type GraphInterfaceDraft,
  type GraphInterfaceElement,
  type GraphInterfaceInferencePreview,
  type GraphInterfaceItemKind,
  type GraphInterfaceReference,
} from './subgraphInterface'
import {
  duplicateGraphCall,
  duplicateGraphDefinition,
  expandGraphCall,
  graphCallSites,
  type ExpandedGraphCall,
  type GraphCallSite,
} from './subgraphLifecycle'
import {
  describeWorkflowSaveError,
  type WorkflowSaveErrorKind,
  type WorkflowSaveErrorTarget,
} from './workflowSaveError'
import { errorMessage, toRPCError } from '@/lib/invoke'
import { toWorkflowPatch } from './editorCommandPersistence'
import {
  applyCompatibleNodeContractUpgrade,
  defaultStateValue,
  nodeKey,
  resolveNodeInstanceProjection,
  stateTypeHasDefault,
} from './editorTypeProjection'
import {
  defaultNodeId,
  graphAt,
  graphEndpointType,
  graphSignalEndpointValid,
  normalizeGraph,
  resolveGraphInputProjection,
  uniqueElementId,
  uniqueGraphId,
  uniqueNodeId,
} from './editorGraphModel'
import { applyCommand } from './editorCommandApplication'
import { syncNodePackageDependencies } from './nodePackageDependencies'

export { assignable } from './connectionCompatibility'

export type EditorPhase = 'empty' | 'loading' | 'ready' | 'saving' | 'running' | 'failed'

export interface LinearWorkflowDraftNode {
  nodeTypeID: string
  config: Record<string, unknown>
  values: Record<string, unknown>
  blobs: Record<string, BlobRef>
  resources?: Record<string, ResourceBinding>
  execInput: string
  execOutput: string
}

export interface StateReferenceLocation {
  graphId: string
  nodeId: string
  mode: 'read' | 'write'
}

export type StateReferenceMode = 'read' | 'write' | 'last-change' | 'increment'

export interface StateTypeChangeIssue {
  graphId: string
  edge: Edge
  disposition: 'conversion' | 'incompatible'
  conversions: ConversionCandidatePlan[]
}

export interface StateTypeChangeImpact {
  references: StateReferenceLocation[]
  issues: StateTypeChangeIssue[]
}

export type EditorCommand =
  | { kind: 'rename-workflow'; name: string }
  | { kind: 'set-target-default'; target: string; slot: string }
  | { kind: 'clear-target-default'; target: string }
  | { kind: 'add-state-variable'; name: string; type: TypeExpression; defaultValue: unknown }
  | {
      kind: 'update-state-variable'
      name: string
      type: TypeExpression
      defaultValue: unknown
      parameter?: Parameter
      clearParameter?: boolean
    }
  | { kind: 'set-workflow-targets'; targets: WorkflowTarget[] }
  | { kind: 'set-parameter-blocks'; blocks: ParameterBlock[] }
  | { kind: 'remove-state-variable'; name: string }
  | { kind: 'add-node'; nodeTypeId: string; position: { x: number; y: number }; nodeId?: string }
  | { kind: 'upgrade-node-contract'; nodeId: string }
  | { kind: 'remove-node'; nodeId: string }
  | { kind: 'move-node'; nodeId: string; position: { x: number; y: number } }
  | {
      kind: 'move-nodes'
      positions: Array<{ nodeId: string; position: { x: number; y: number } }>
    }
  | { kind: 'set-node-label'; nodeId: string; label: string }
  | { kind: 'set-node-disabled'; nodeId: string; disabled: boolean }
  | { kind: 'set-config'; nodeId: string; fieldId: string; value: unknown }
  | { kind: 'clear-config'; nodeId: string; fieldId: string }
  | { kind: 'bind-value'; nodeId: string; portId: string; value: unknown }
  | { kind: 'bind-blob'; nodeId: string; portId: string; blob: BlobRef }
  | { kind: 'bind-resource'; nodeId: string; portId: string; resource: ResourceBinding }
  | { kind: 'add-resource'; resource: WorkflowResource }
  | { kind: 'replace-resource'; resourceId: string; resource: WorkflowResource }
  | {
      kind: 'update-resource-metadata'
      resourceId: string
      name: string
      description: string
      category: string
      tags: string[]
    }
  | { kind: 'remove-resource'; resourceId: string }
  | { kind: 'bind-default'; nodeId: string; portId: string }
  | { kind: 'clear-binding'; nodeId: string; portId: string }
  | { kind: 'connect'; edge: Edge }
  | { kind: 'disconnect'; edge: Edge }
  | { kind: 'add-graph'; graph: Graph }
  | { kind: 'rename-graph'; graphId: string; name: string }
  | { kind: 'remove-graph'; graphId: string }
  | { kind: 'remove-graph-cascade'; graphId: string; calls: GraphCallSite[] }
  | {
      kind: 'update-graph-interface'
      inputs: Graph['inputs']
      outputs: Graph['outputs']
      entries: NonNullable<Graph['entries']>
      exits: NonNullable<Graph['exits']>
    }
  | { kind: 'add-graph-call'; call: GraphCall }
  | { kind: 'update-graph-call'; call: GraphCall }
  | { kind: 'remove-graph-call'; callId: string }
  | { kind: 'fork-graph-call'; graph: Graph; call: GraphCall }
  | ({ kind: 'expand-graph-call' } & ExpandedGraphCall)
  | { kind: 'add-annotation'; annotation: Annotation }
  | { kind: 'update-annotation'; annotation: Annotation }
  | { kind: 'remove-annotation'; annotationId: string }
  | { kind: 'set-edge-reroutes'; edge: Edge; reroutes: Array<{ x: number; y: number }> }
  | {
      kind: 'collapse-selection'
      subgraphId: string
      callId: string
      name: string
      nodeIds: string[]
      position: { x: number; y: number }
    }
  | { kind: 'remove-nodes'; nodeIds: string[] }
  | {
      kind: 'insert-node-selection'
      nodes: Node[]
      calls: GraphCall[]
      annotations: Annotation[]
      edges: Edge[]
    }
  | {
      kind: 'insert-connected-node'
      nodeTypeId: string
      nodeId: string
      position: { x: number; y: number }
      edge: Edge
    }
  | {
      kind: 'promote-output-to-state'
      name: string
      type: TypeExpression
      defaultValue: unknown
      nodeTypeId: string
      nodeId: string
      stateConfigKey: string
      position: { x: number; y: number }
      edge: Edge
    }
  | { kind: 'batch'; commands: EditorCommand[] }

interface PendingCommand {
  graphId: string
  command: EditorCommand
}

export class EditorSession {
  phase: EditorPhase = 'empty'
  source: YottaWorkflowSource | null = null
  authoring: YottaNodeAuthoringProjection | null = null
  baseRevision = -1
  sourceHash = ''
  compiledHash = ''
  lastRunHash = ''
  diagnostics: CompileView['diagnostics'] = []
  activeRun: RunView | null = null
  lastRunOutcome: RunStartOutcome | null = null
  debugSnapshot: DebugSnapshot | null = null
  graphPath: string[] = []
  dirty = false
  saveError = ''
  saveErrorKind: WorkflowSaveErrorKind | '' = ''
  saveErrorTarget: WorkflowSaveErrorTarget | null = null
  openFailure = ''

  private readonly history: YottaWorkflowSource[] = []
  private nodePackages: NodePackageDependency[] = []
  private readonly future: YottaWorkflowSource[] = []
  private readonly pendingCommands: PendingCommand[] = []
  private readonly revertedCommands: PendingCommand[] = []
  private readonly durableNodeKeys = new Set<string>()
  private readonly projections = new Map<string, NodeProjection>()
  private readonly typeProjections = new Map<string, TypeProjection>()
  private readonly pendingDebugSnapshots = new Map<string, DebugSnapshot>()
  private debugStartPending = false
  private saveInFlight: Promise<SourceView> | null = null
  private savedSource: SourceView | null = null

  constructor(
    private readonly transport: WorkflowTransport,
    private readonly idFactory: () => string = defaultNodeId,
  ) {}

  get workflowId(): string {
    return this.source?.workflow.id ?? ''
  }

  get canUndo(): boolean {
    return this.history.length !== 0 && !this.saveInFlight
  }

  get canRedo(): boolean {
    return this.future.length !== 0 && !this.saveInFlight
  }

  get currentGraph(): Graph | null {
    if (!this.source) return null
    const graphId = this.graphPath.at(-1) ?? this.source.entryGraph
    return this.source.graphs.find((graph) => graph.id === graphId) ?? null
  }

  createSubgraph(name = 'Subgraph'): string {
    const source = this.requireSource()
    const graphId = uniqueGraphId(source, this.idFactory)
    this.apply({
      kind: 'add-graph',
      graph: {
        id: graphId,
        name,
        kind: 'subgraph',
        nodes: [],
        calls: [],
        edges: [],
        inputs: [],
        outputs: [],
        entries: [],
        exits: [],
        annotations: [],
      },
    })
    this.enterGraph(graphId)
    return graphId
  }

  renameGraph(graphId: string, name: string): void {
    this.apply({ kind: 'rename-graph', graphId, name })
  }

  setTargetDefault(target: string, slot: string): void {
    if (slot) this.apply({ kind: 'set-target-default', target, slot })
    else this.apply({ kind: 'clear-target-default', target })
  }

  removeGraph(graphId: string): void {
    this.apply({ kind: 'remove-graph', graphId })
    this.graphPath = [this.requireSource().entryGraph]
  }

  removeGraphCascade(graphId: string): void {
    const source = this.requireSource()
    if (graphId === source.entryGraph) throw new Error('entry graph cannot be removed')
    if (!source.graphs.some((graph) => graph.id === graphId))
      throw new Error(`graph ${graphId} does not exist`)
    this.apply({ kind: 'remove-graph-cascade', graphId, calls: graphCallSites(source, graphId) })
    this.graphPath = [this.requireSource().entryGraph]
  }

  duplicateGraphDefinition(graphId: string): string {
    const source = this.requireSource()
    const original = source.graphs.find((graph) => graph.id === graphId)
    const newGraphId = uniqueGraphId(source, this.idFactory)
    const graph = duplicateGraphDefinition(
      source,
      graphId,
      newGraphId,
      `${original?.name?.trim() || graphId} Copy`,
    )
    this.apply({ kind: 'add-graph', graph })
    return newGraphId
  }

  duplicateCurrentGraphCall(callId: string, offset = { x: 32, y: 32 }): string {
    const graph = this.currentGraph
    if (!graph) throw new Error('workflow graph is unavailable')
    const newCallId = uniqueElementId(graph, this.idFactory)
    this.apply({
      kind: 'add-graph-call',
      call: duplicateGraphCall(graph, callId, newCallId, offset),
    })
    return newCallId
  }

  forkCurrentGraphCall(callId: string): string {
    const source = this.requireSource()
    const graph = this.currentGraph
    const call = graph?.calls?.find((candidate) => candidate.id === callId)
    if (!graph || !call) throw new Error(`graph call ${callId} does not exist`)
    const callee = source.graphs.find((candidate) => candidate.id === call.graphId)
    const newGraphId = uniqueGraphId(source, this.idFactory)
    const copy = duplicateGraphDefinition(
      source,
      call.graphId,
      newGraphId,
      `${callee?.name?.trim() || call.graphId} Copy`,
    )
    this.apply({
      kind: 'fork-graph-call',
      graph: copy,
      call: { ...clone(call), graphId: newGraphId },
    })
    return newGraphId
  }

  expandCurrentGraphCall(callId: string): string[] {
    const graph = this.currentGraph
    if (!graph) throw new Error('workflow graph is unavailable')
    const expansion = expandGraphCall(this.requireSource(), graph.id, callId, this.idFactory)
    this.apply({ kind: 'expand-graph-call', ...expansion })
    return [
      ...expansion.nodes.map((node) => node.id),
      ...expansion.calls.map((call) => call.id),
      ...expansion.annotations.map((annotation) => annotation.id),
    ]
  }

  addAnnotation(position: { x: number; y: number }): string {
    const graph = this.currentGraph
    if (!graph) throw new Error('workflow graph is unavailable')
    const id = uniqueElementId(graph, this.idFactory)
    this.apply({
      kind: 'add-annotation',
      annotation: { id, text: '', position, size: { ...DEFAULT_ANNOTATION_SIZE } },
    })
    return id
  }

  collapseSelection(nodeIds: string[], name = 'Subgraph'): string {
    const graph = this.currentGraph
    if (!graph || nodeIds.length === 0) throw new Error('selection is empty')
    const subgraphId = uniqueGraphId(this.requireSource(), this.idFactory)
    const callId = uniqueElementId(graph, this.idFactory)
    const selected = [
      ...graph.nodes.filter((node) => nodeIds.includes(node.id)),
      ...graph.calls!.filter((call) => nodeIds.includes(call.id)),
    ]
    if (selected.length !== nodeIds.length)
      throw new Error('only executable nodes and graph calls can be collapsed')
    const position = {
      x: selected.reduce((sum, node) => sum + node.position.x, 0) / selected.length,
      y: selected.reduce((sum, node) => sum + node.position.y, 0) / selected.length,
    }
    this.apply({ kind: 'collapse-selection', subgraphId, callId, name, nodeIds, position })
    return callId
  }

  nodeProjection(nodeTypeId: string): NodeProjection | undefined {
    return this.projections.get(nodeTypeId)
  }

  isPackagedNode(nodeTypeId: string): boolean {
    return this.nodePackages.some((pkg) =>
      pkg.nodeRefs.some((ref) => ref.nodeTypeId === nodeTypeId),
    )
  }

  nodeInstanceProjection(node: Node): NodeProjection | undefined {
    const base = this.projections.get(node.nodeRef.nodeTypeId)
    if (!base || base.nodeRef.semanticDigest !== node.nodeRef.semanticDigest) return undefined
    return resolveNodeInstanceProjection(
      this.source,
      node,
      base,
      this.projections,
      this.typeProjections,
    )
  }

  stateTypeChangeImpact(name: string, type: TypeExpression): StateTypeChangeImpact {
    const source = this.requireSource()
    const variable = source.variables.find((candidate) => candidate.name === name)
    if (!variable) throw new Error(`state variable ${name} does not exist`)
    const references = collectStateReferences(source, name)
    const candidate = clone(source)
    const candidateVariable = candidate.variables.find((item) => item.name === name)!
    candidateVariable.type = clone(type)
    const issues: StateTypeChangeIssue[] = []
    for (const graph of source.graphs) {
      const candidateGraph = candidate.graphs.find((item) => item.id === graph.id)!
      for (const edge of graph.edges) {
        if (edge.channel !== 'data') continue
        const before = connectionCompatibilityInGraph(
          source,
          graph,
          edge,
          this.projections,
          this.typeProjections,
          this.authoring?.body.nodes ?? [],
        )
        const after = connectionCompatibilityInGraph(
          candidate,
          candidateGraph,
          edge,
          this.projections,
          this.typeProjections,
          this.authoring?.body.nodes ?? [],
        )
        if (!before.valid || after.valid) continue
        issues.push({
          graphId: graph.id,
          edge: clone(edge),
          disposition: after.disposition === 'conversion' ? 'conversion' : 'incompatible',
          conversions: clone(after.conversions ?? []),
        })
      }
    }
    return { references, issues }
  }

  calleeGraph(call: GraphCall): Graph | undefined {
    return this.source?.graphs.find((graph) => graph.id === call.graphId)
  }

  insertGraphCall(graphId: string, position: { x: number; y: number }): string {
    const graph = this.currentGraph
    const callee = this.source?.graphs.find(
      (candidate) => candidate.id === graphId && candidate.kind === 'subgraph',
    )
    if (!graph || !callee) throw new Error(`subgraph ${graphId} does not exist`)
    const id = uniqueElementId(graph, this.idFactory)
    this.apply({
      kind: 'add-graph-call',
      call: {
        id,
        graphId,
        label: callee.name || callee.id,
        position,
        bindings: {},
      },
    })
    return id
  }

  graphInputProjection(graphId: string, portId: string): PortProjection | undefined {
    const source = this.source
    const graph = source?.graphs.find((candidate) => candidate.id === graphId)
    const port = graph?.inputs.find((candidate) => candidate.id === portId)
    if (!source || !graph || !port) return undefined
    const projection = resolveGraphInputProjection(
      source,
      graph,
      { nodeId: port.nodeId, portId: port.portId },
      this.projections,
      new Set(),
    )
    return projection ? { ...clone(projection), id: port.id } : undefined
  }

  currentGraphInterfaceReadiness(): {
    valid: boolean
    reason?: 'not-subgraph' | 'multiple-entry' | 'missing-entry-or-exit'
  } {
    const graph = this.currentGraph
    if (!graph || graph.kind !== 'subgraph') return { valid: false, reason: 'not-subgraph' }
    const candidates = this.currentGraphInterfaceCandidates()
    const entries = candidates.filter((candidate) => candidate.kind === 'entry').length
    const exits = candidates.filter((candidate) => candidate.kind === 'exit').length
    if (entries > 1) return { valid: false, reason: 'multiple-entry' }
    if (!entries || !exits) return { valid: false, reason: 'missing-entry-or-exit' }
    return { valid: true }
  }

  currentGraphInterfaceCandidates(): GraphInterfaceCandidate[] {
    const graph = this.currentGraph
    if (!graph || graph.kind !== 'subgraph') throw new Error('open a subgraph first')
    return projectGraphInterfaceCandidates(graph, this.graphInterfaceElements(graph))
  }

  addCurrentGraphInterfaceCandidate(candidateKey: string): void {
    const graph = this.requireCurrentSubgraph()
    const selected = this.currentGraphInterfaceCandidates().find(
      (candidate) => candidate.key === candidateKey,
    )
    if (!selected) throw new Error('subgraph interface candidate is no longer available')
    this.updateCurrentGraphInterface(addGraphInterfaceCandidate(graph, selected))
  }

  renameCurrentGraphInterfaceItem(kind: GraphInterfaceItemKind, id: string, name: string): void {
    const graph = this.requireCurrentSubgraph()
    this.updateCurrentGraphInterface(renameGraphInterfaceItem(graph, kind, id, name))
  }

  moveCurrentGraphInterfaceItem(kind: GraphInterfaceItemKind, id: string, direction: -1 | 1): void {
    const graph = this.requireCurrentSubgraph()
    this.updateCurrentGraphInterface(moveGraphInterfaceItem(graph, kind, id, direction))
  }

  removeCurrentGraphInterfaceItem(kind: 'entry' | GraphInterfaceItemKind, id = ''): void {
    const graph = this.requireCurrentSubgraph()
    this.updateCurrentGraphInterface(removeGraphInterfaceItem(graph, kind, id))
  }

  currentGraphInterfaceReferences(
    kind: GraphInterfaceItemKind,
    id: string,
  ): GraphInterfaceReference[] {
    const graph = this.requireCurrentSubgraph()
    return graphInterfaceReferences(this.requireSource(), graph.id, kind, id)
  }

  previewCurrentGraphInterfaceInference(): GraphInterfaceInferencePreview {
    const graph = this.requireCurrentSubgraph()
    return inferGraphInterface(graph, this.currentGraphInterfaceCandidates())
  }

  applyCurrentGraphInterfaceInference(preview?: GraphInterfaceInferencePreview): void {
    this.updateCurrentGraphInterface(
      preview?.draft ?? this.previewCurrentGraphInterfaceInference().draft,
    )
  }

  inferCurrentGraphInterface(): void {
    this.applyCurrentGraphInterfaceInference()
  }

  graphBoundaryCompatibility(binding: GraphBoundaryBinding): ConnectionCompatibility {
    const graph = this.currentGraph
    if (!graph || graph.kind !== 'subgraph')
      return { valid: false, issue: 'port', message: 'open a subgraph first' }
    if (binding.kind === 'entry') {
      return graphSignalEndpointValid(
        this.requireSource(),
        graph,
        binding.endpoint,
        false,
        'exec',
        this.projections,
      )
        ? { valid: true, disposition: 'direct' }
        : { valid: false, issue: 'port', message: 'subgraph entry requires an execution input' }
    }
    if (binding.kind === 'exit') {
      const exit = graph.exits?.find((candidate) => candidate.id === binding.boundaryId)
      return exit &&
        graphSignalEndpointValid(
          this.requireSource(),
          graph,
          binding.endpoint,
          true,
          exit.channel,
          this.projections,
        )
        ? { valid: true, disposition: 'direct' }
        : {
            valid: false,
            issue: 'port',
            message: 'subgraph exit requires a matching signal output',
          }
    }
    const port = (binding.kind === 'input' ? graph.inputs : graph.outputs).find(
      (candidate) => candidate.id === binding.boundaryId,
    )
    if (!port) return { valid: false, issue: 'port', message: 'subgraph interface port is missing' }
    const endpointType = graphEndpointType(
      this.requireSource(),
      graph,
      binding.endpoint,
      binding.kind === 'output',
      this.projections,
    )
    const compatible =
      endpointType &&
      (binding.kind === 'input'
        ? assignable(port.type, endpointType, this.typeProjections)
        : assignable(endpointType, port.type, this.typeProjections))
    return compatible
      ? { valid: true, disposition: 'direct' }
      : {
          valid: false,
          issue: 'type',
          message: 'subgraph interface and endpoint types are incompatible',
          disposition: 'incompatible',
        }
  }

  bindGraphBoundary(binding: GraphBoundaryBinding): void {
    const compatibility = this.graphBoundaryCompatibility(binding)
    if (!compatibility.valid) throw new Error(compatibility.message ?? 'invalid subgraph binding')
    const graph = this.currentGraph!
    const inputs = clone(graph.inputs)
    const outputs = clone(graph.outputs)
    const entries: NonNullable<Graph['entries']> = graph.entries ? clone(graph.entries) : []
    const exits = clone(graph.exits ?? [])
    if (binding.kind === 'entry') entries.splice(0, entries.length, clone(binding.endpoint))
    if (binding.kind === 'input') {
      const port = inputs.find((candidate) => candidate.id === binding.boundaryId)!
      Object.assign(port, clone(binding.endpoint))
    }
    if (binding.kind === 'output') {
      const port = outputs.find((candidate) => candidate.id === binding.boundaryId)!
      Object.assign(port, clone(binding.endpoint))
    }
    if (binding.kind === 'exit') {
      const exit = exits.find((candidate) => candidate.id === binding.boundaryId)!
      exit.endpoint = clone(binding.endpoint)
    }
    this.apply({ kind: 'update-graph-interface', inputs, outputs, entries, exits })
  }

  unbindGraphBoundary(key: GraphBoundaryKey): void {
    const graph = this.currentGraph
    if (!graph || graph.kind !== 'subgraph') throw new Error('open a subgraph first')
    this.apply({
      kind: 'update-graph-interface',
      inputs:
        key.kind === 'input'
          ? graph.inputs.filter((port) => port.id !== key.boundaryId)
          : clone(graph.inputs),
      outputs:
        key.kind === 'output'
          ? graph.outputs.filter((port) => port.id !== key.boundaryId)
          : clone(graph.outputs),
      entries: key.kind === 'entry' ? [] : clone(graph.entries ?? []),
      exits:
        key.kind === 'exit'
          ? (graph.exits ?? []).filter((exit) => exit.id !== key.boundaryId)
          : clone(graph.exits ?? []),
    })
  }

  connectionCompatibility(edge: Edge): ConnectionCompatibility {
    const graph = this.currentGraph
    if (!graph) return { valid: false, issue: 'port', message: 'workflow graph is unavailable' }
    return connectionCompatibilityInGraph(
      this.requireSource(),
      graph,
      edge,
      this.projections,
      this.typeProjections,
      this.authoring?.body.nodes ?? [],
    )
  }

  insertConversionBridge(
    edge: Edge,
    conversion: ConversionCandidatePlan,
    position: { x: number; y: number },
  ): string {
    if (edge.channel !== 'data') throw new Error('conversion bridge requires a data edge')
    const plan = this.connectionCompatibility(edge)
    const allowed = plan.conversions?.find(
      (candidate) =>
        candidate.nodeTypeId === conversion.nodeTypeId &&
        candidate.inputPort === conversion.inputPort &&
        candidate.outputPort === conversion.outputPort,
    )
    if (!allowed) throw new Error('conversion is not valid for this connection')
    const graph = this.currentGraph
    const projection = this.projections.get(allowed.nodeTypeId)
    if (!graph || !projection) throw new Error('conversion projection is unavailable')
    const nodeId = uniqueNodeId(graph, this.idFactory)
    this.apply({
      kind: 'insert-node-selection',
      nodes: [
        {
          id: nodeId,
          nodeRef: clone(projection.nodeRef),
          position: clone(position),
          config: {},
          bindings: {},
        },
      ],
      calls: [],
      annotations: [],
      edges: [
        {
          channel: 'data',
          from: clone(edge.from),
          to: { nodeId, portId: allowed.inputPort },
        },
        {
          channel: 'data',
          from: { nodeId, portId: allowed.outputPort },
          to: clone(edge.to),
        },
      ],
    })
    return nodeId
  }

  promoteOutputToState(
    nodeId: string,
    portId: string,
    name: string,
    position: { x: number; y: number },
  ): string {
    const graph = this.currentGraph
    const sourceNode = graph?.nodes.find((node) => node.id === nodeId)
    const sourceProjection = sourceNode ? this.nodeInstanceProjection(sourceNode) : undefined
    const output = sourceProjection?.dataOutputs.find((port) => port.id === portId)
    if (
      !graph ||
      !output ||
      output.carrier !== 'durable' ||
      output.type.expression.kind !== 'ref'
    ) {
      throw new Error('output cannot be promoted to durable state')
    }
    const type = this.typeProjections.get(output.type.expression.ref.typeId)
    if (!type || !type.traits.includes('durable') || !stateTypeHasDefault(type)) {
      throw new Error('output type cannot be initialized as durable state')
    }
    const stateWrite = [...this.projections.values()].find(
      (projection) =>
        projection.nodeRef.nodeTypeId.endsWith('/state/write') &&
        projection.stateAccesses.some((access) => access.mode === 'write'),
    )
    if (!stateWrite) throw new Error('state write node is unavailable')
    const inputPort = stateWrite.stateAccesses.find((access) => access.mode === 'write')
    const valuePort = stateWrite.dataInputs.find((port) => port.type.expression.kind === 'variable')
    if (!inputPort || !valuePort) throw new Error('state write contract is incomplete')
    const stateNodeId = uniqueNodeId(graph, this.idFactory)
    this.apply({
      kind: 'promote-output-to-state',
      name,
      type: clone(output.type.expression),
      defaultValue: defaultStateValue(type),
      nodeTypeId: stateWrite.nodeRef.nodeTypeId,
      nodeId: stateNodeId,
      stateConfigKey: inputPort.slotConfigKey,
      position: clone(position),
      edge: {
        channel: 'data',
        from: { nodeId, portId },
        to: { nodeId: stateNodeId, portId: valuePort.id },
      },
    })
    return stateNodeId
  }

  insertConnectedNode(
    anchorNodeId: string,
    anchor: ParsedHandle,
    nodeTypeId: string,
    candidate: ParsedHandle,
    position: { x: number; y: number },
  ): string {
    if (anchor.direction === candidate.direction) {
      throw new Error('connected node ports are incompatible')
    }
    const graph = this.currentGraph
    if (!graph) throw new Error('workflow graph is unavailable')
    const nodeId = uniqueNodeId(graph, this.idFactory)
    const output = anchor.direction === 'output' ? anchor : candidate
    const edge: Edge =
      anchor.direction === 'output'
        ? {
            channel: output.channel,
            from: { nodeId: anchorNodeId, portId: anchor.portId },
            to: { nodeId, portId: candidate.portId },
          }
        : {
            channel: output.channel,
            from: { nodeId, portId: candidate.portId },
            to: { nodeId: anchorNodeId, portId: anchor.portId },
          }
    this.apply({ kind: 'insert-connected-node', nodeTypeId, nodeId, position, edge })
    return nodeId
  }

  insertNodeIntoSignalEdge(
    edge: Edge,
    nodeTypeId: string,
    position: { x: number; y: number },
  ): string {
    return this.insertNodesIntoSignalEdges([edge], nodeTypeId, [position])[0]!
  }

  insertNodesIntoSignalEdges(
    edges: Edge[],
    nodeTypeId: string,
    positions: Array<{ x: number; y: number }>,
  ): string[] {
    if (!edges.length || edges.length !== positions.length) {
      throw new Error('selected signal edges and insertion positions must align')
    }
    if (edges.some((edge) => edge.channel === 'data')) {
      throw new Error('data edges require an explicit typed insertion')
    }
    const graph = this.currentGraph
    const projection = this.projections.get(nodeTypeId)
    if (!graph || !projection) throw new Error('workflow node projection is unavailable')
    const shadow = clone(graph)
    const nodeIds: string[] = []
    const commands: EditorCommand[] = []
    for (const [index, edge] of edges.entries()) {
      const input = projection.signals.find(
        (signal) => signal.direction === 'input' && signal.channel === edge.channel,
      )
      const output = projection.signals.find(
        (signal) => signal.direction === 'output' && signal.channel === edge.channel,
      )
      if (!input || !output) {
        throw new Error('node cannot be inserted into every selected signal edge')
      }
      const nodeId = uniqueNodeId(shadow, this.idFactory)
      nodeIds.push(nodeId)
      shadow.nodes.push({
        id: nodeId,
        nodeRef: clone(projection.nodeRef),
        position: clone(positions[index]!),
        config: {},
        bindings: {},
      })
      commands.push(
        { kind: 'disconnect', edge: clone(edge) },
        { kind: 'add-node', nodeTypeId, nodeId, position: clone(positions[index]!) },
        {
          kind: 'connect',
          edge: {
            channel: edge.channel,
            from: clone(edge.from),
            to: { nodeId, portId: input.id },
          },
        },
        {
          kind: 'connect',
          edge: {
            channel: edge.channel,
            from: { nodeId, portId: output.id },
            to: clone(edge.to),
          },
        },
      )
    }
    this.apply({ kind: 'batch', commands })
    return nodeIds
  }

  moveNodes(positions: Array<{ nodeId: string; position: { x: number; y: number } }>): void {
    if (positions.length) this.apply({ kind: 'move-nodes', positions })
  }

  removeNodes(nodeIds: string[]): void {
    const unique = [...new Set(nodeIds)]
    if (unique.length) this.apply({ kind: 'remove-nodes', nodeIds: unique })
  }

  selectionSnapshot(nodeIds: string[]): {
    nodes: Node[]
    calls: GraphCall[]
    annotations: Annotation[]
    edges: Edge[]
  } {
    const graph = this.currentGraph
    if (!graph) return { nodes: [], calls: [], annotations: [], edges: [] }
    const selected = new Set(nodeIds)
    return {
      nodes: clone(graph.nodes.filter((node) => selected.has(node.id))),
      calls: clone(graph.calls!.filter((call) => selected.has(call.id))),
      annotations: clone(graph.annotations!.filter((annotation) => selected.has(annotation.id))),
      edges: clone(
        graph.edges.filter(
          (edge) => selected.has(edge.from.nodeId) && selected.has(edge.to.nodeId),
        ),
      ),
    }
  }

  insertNodeSelection(
    selection: {
      nodes: Node[]
      calls?: GraphCall[]
      annotations?: Annotation[]
      edges: Edge[]
    },
    offset: { x: number; y: number },
  ): string[] {
    const graph = this.currentGraph
    if (
      !graph ||
      selection.nodes.length +
        (selection.calls?.length ?? 0) +
        (selection.annotations?.length ?? 0) ===
        0
    )
      return []
    const shadow = clone(graph)
    const ids = new Map<string, string>()
    const nodes = selection.nodes.map((node) => {
      const id = uniqueNodeId(shadow, this.idFactory)
      ids.set(node.id, id)
      const inserted = {
        ...clone(node),
        id,
        position: { x: node.position.x + offset.x, y: node.position.y + offset.y },
      }
      shadow.nodes.push(inserted)
      return inserted
    })
    const calls = (selection.calls ?? []).map((call) => {
      const id = uniqueElementId(shadow, this.idFactory)
      ids.set(call.id, id)
      const inserted = {
        ...clone(call),
        id,
        position: { x: call.position.x + offset.x, y: call.position.y + offset.y },
      }
      shadow.calls!.push(inserted)
      return inserted
    })
    const annotations = (selection.annotations ?? []).map((annotation) => {
      const id = uniqueElementId(shadow, this.idFactory)
      const inserted = {
        ...clone(annotation),
        id,
        position: {
          x: annotation.position.x + offset.x,
          y: annotation.position.y + offset.y,
        },
      }
      shadow.annotations!.push(inserted)
      return inserted
    })
    const edges = selection.edges.flatMap((edge) => {
      const from = ids.get(edge.from.nodeId)
      const to = ids.get(edge.to.nodeId)
      return from && to
        ? [
            {
              ...clone(edge),
              from: { ...edge.from, nodeId: from },
              to: { ...edge.to, nodeId: to },
            },
          ]
        : []
    })
    const targets = declareInsertedTargets(
      nodes,
      this.source?.targets ?? [],
      (node) => this.projections.get(node.nodeRef.nodeTypeId),
      () => `target-${crypto.randomUUID()}`,
      (number) => i18n.global.t('workflow.settings_panel.target_name', { number }),
    )
    this.applyBatch([
      ...(targets ? [{ kind: 'set-workflow-targets' as const, targets }] : []),
      { kind: 'insert-node-selection', nodes, calls, annotations, edges },
    ])
    return [
      ...nodes.map((node) => node.id),
      ...calls.map((call) => call.id),
      ...annotations.map((annotation) => annotation.id),
    ]
  }

  insertStateReference(
    variable: string,
    mode: StateReferenceMode,
    position: { x: number; y: number },
  ): string {
    const accessMode = mode === 'read' || mode === 'last-change' ? 'read' : 'write'
    const nodeTypeId = [...this.projections.values()].find(
      (projection) =>
        projection.nodeRef.nodeTypeId.endsWith(`/state/${mode}`) &&
        projection.stateAccesses.some((access) => access.mode === accessMode),
    )?.nodeRef.nodeTypeId
    if (!nodeTypeId) throw new Error(`state ${mode} node is unavailable`)
    const projection = this.projections.get(nodeTypeId)!
    const [nodeId] = this.insertNodeSelection(
      {
        nodes: [
          {
            id: 'state-reference',
            nodeRef: clone(projection.nodeRef),
            position: { x: 0, y: 0 },
            config: { variable },
            bindings: {},
          },
        ],
        edges: [],
      },
      position,
    )
    return nodeId
  }

  insertLinearDraft(
    draftNodes: LinearWorkflowDraftNode[],
    origin: { x: number; y: number },
    resources: WorkflowResource[] = [],
  ): string[] {
    const graph = this.currentGraph
    if (!graph || draftNodes.length === 0) return []
    const shadow = clone(graph)
    const defaultTargetSlot = this.source?.targetDefaults?.find(
      (item) => item.target === 'target',
    )?.slot
    const nodes = draftNodes.map((draft, index): Node => {
      const projection = this.projections.get(draft.nodeTypeID)
      if (!projection) throw new Error(`draft node type ${draft.nodeTypeID} is unavailable`)
      const id = uniqueNodeId(shadow, this.idFactory)
      const bindings: Node['bindings'] = {}
      for (const [portId, value] of Object.entries(draft.values)) {
        bindings[portId] = { kind: 'value', value: clone(value) }
      }
      for (const [portId, blob] of Object.entries(draft.blobs)) {
        bindings[portId] = { kind: 'blob', blob: clone(blob) }
      }
      for (const [portId, resource] of Object.entries(draft.resources ?? {})) {
        bindings[portId] = { kind: 'resource', resource: clone(resource) }
      }
      const config = clone(draft.config)
      if (defaultTargetSlot && config.slot === defaultTargetSlot) delete config.slot
      const node: Node = {
        id,
        nodeRef: clone(projection.nodeRef),
        position: { x: origin.x + index * 280, y: origin.y },
        config,
        bindings,
      }
      shadow.nodes.push(node)
      return node
    })
    const edges = nodes.slice(1).map((node, index): Edge => ({
      channel: 'exec',
      from: { nodeId: nodes[index].id, portId: draftNodes[index].execOutput },
      to: { nodeId: node.id, portId: draftNodes[index + 1].execInput },
    }))
    const targets = declareInsertedTargets(
      nodes,
      this.source?.targets ?? [],
      (node) => this.projections.get(node.nodeRef.nodeTypeId),
      () => `target-${crypto.randomUUID()}`,
      (number) => i18n.global.t('workflow.settings_panel.target_name', { number }),
    )
    this.applyBatch([
      ...(targets ? [{ kind: 'set-workflow-targets' as const, targets }] : []),
      ...resources.map((resource): EditorCommand => ({ kind: 'add-resource', resource })),
      { kind: 'insert-node-selection', nodes, calls: [], annotations: [], edges },
    ])
    return nodes.map((node) => node.id)
  }

  duplicateNodes(nodeIds: string[], offset = { x: 32, y: 32 }): string[] {
    return this.insertNodeSelection(this.selectionSnapshot(nodeIds), offset)
  }

  async load(workflowId: string): Promise<void> {
    this.phase = 'loading'
    this.openFailure = ''
    try {
      const [view, authoringJson, packages] = await Promise.all([
        this.transport.getSource(workflowId),
        this.transport.getAuthoringProjection(),
        this.transport.getNodePackageDependencies?.() ?? Promise.resolve([]),
      ])
      this.nodePackages = packages
      this.loadAuthoring(authoringJson)
      this.acceptSource(view)
      this.upgradeCompatibleNodeContracts()
      this.phase = 'ready'
    } catch (error) {
      this.failOpen(error)
      throw error
    }
  }

  async refreshIfClean(): Promise<boolean> {
    if (!this.source || this.dirty || this.phase === 'saving') return false
    const workflowId = this.workflowId
    const baseRevision = this.baseRevision
    const sourceHash = this.sourceHash
    const graphPath = [...this.graphPath]
    const view = await this.transport.getSource(workflowId)
    if (
      !this.source ||
      this.dirty ||
      this.workflowId !== workflowId ||
      this.baseRevision !== baseRevision ||
      (view.revision === baseRevision && view.sourceHash === sourceHash)
    ) {
      return false
    }
    this.acceptSource(view)
    this.openGraphPath(graphPath)
    return true
  }

  apply(command: EditorCommand): void {
    const source = this.requireSource()
    const next = clone(source)
    const graph = graphAt(next, this.graphPath)
    const resolved = resolveEditorCommand(command, graph, this.idFactory)
    applyCommand(next, graph, resolved, this.projections, this.typeProjections)
    syncNodePackageDependencies(next, this.nodePackages)
    next.revision = this.baseRevision + 1
    this.history.push(clone(source))
    if (this.history.length > 100) this.history.shift()
    this.future.length = 0
    this.pendingCommands.push({ graphId: graph.id, command: clone(resolved) })
    this.revertedCommands.length = 0
    this.source = next
    this.dirty = true
    this.dismissSaveError()
    this.resetCompileFacts()
  }

  applyBatch(commands: EditorCommand[]): void {
    if (!commands.length) return
    this.apply({ kind: 'batch', commands: clone(commands) })
  }

  undo(): void {
    if (!this.source || !this.canUndo) return
    this.future.push(clone(this.source))
    this.source = this.history.pop() ?? this.source
    const reverted = this.pendingCommands.pop()
    if (reverted) this.revertedCommands.push(reverted)
    this.source.revision = this.baseRevision + 1
    this.dirty = true
    this.resetCompileFacts()
  }

  redo(): void {
    if (!this.source || !this.canRedo) return
    this.history.push(clone(this.source))
    this.source = this.future.pop() ?? this.source
    const restored = this.revertedCommands.pop()
    if (restored) this.pendingCommands.push(restored)
    this.source.revision = this.baseRevision + 1
    this.dirty = true
    this.resetCompileFacts()
  }

  enterGraph(graphId: string): void {
    const source = this.requireSource()
    if (!source.graphs.some((graph) => graph.id === graphId)) {
      throw new Error(`graph ${graphId} does not exist`)
    }
    this.graphPath.push(graphId)
  }

  openGraphPath(graphPath: readonly string[]): void {
    const source = this.requireSource()
    // Runtime provenance interleaves call IDs so two calls to one subgraph
    // remain distinguishable. The editor navigation model only contains graphs.
    const graphIds = new Set(source.graphs.map((graph) => graph.id))
    const next = graphPath.filter((id) => graphIds.has(id))
    if (!next.length) next.push(source.entryGraph)
    this.graphPath = next
  }

  leaveGraph(): void {
    this.graphPath.pop()
  }

  async check(): Promise<CompileView> {
    await this.refreshAuthoringProjection()
    return this.checkCurrentDraft()
  }

  private async checkCurrentDraft(): Promise<CompileView> {
    const result = await this.transport.checkDraft(this.serialize())
    this.sourceHash = result.sourceHash ?? ''
    this.compiledHash = result.programHash ?? ''
    this.diagnostics = [...result.diagnostics]
    return result
  }

  save(): Promise<SourceView> {
    if (this.saveInFlight) return this.saveInFlight
    if (!this.dirty) {
      const source = this.requireSource()
      return Promise.resolve({
        workflowId: source.workflow.id,
        name: source.workflow.name,
        revision: this.baseRevision,
        sourceHash: this.sourceHash,
        sourceJson: this.serialize(),
      } as SourceView)
    }
    const operation = this.persistSave()
    this.saveInFlight = operation
    const clearInFlight = () => {
      if (this.saveInFlight === operation) this.saveInFlight = null
    }
    void operation.then(clearInFlight, clearInFlight)
    return operation
  }

  dismissSaveError(): void {
    this.saveError = ''
    this.saveErrorKind = ''
    this.saveErrorTarget = null
  }

  // Discard must not depend on a new backend read succeeding. Exit waits for
  // outstanding saves first, so this is the last acknowledged durable snapshot.
  discardDraft(): void {
    if (this.saveInFlight) throw new Error('Cannot discard during a save')
    if (!this.savedSource) throw new Error('No saved Workflow Source to restore')
    this.acceptSource(this.savedSource)
    this.phase = 'ready'
  }

  private async persistSave(): Promise<SourceView> {
    let saved: SourceView
    do {
      saved = await this.persistSaveBatch()
    } while (this.dirty)
    return saved
  }

  private async persistSaveBatch(): Promise<SourceView> {
    this.phase = 'saving'
    this.dismissSaveError()
    const pendingCount = this.pendingCommands.length
    const commands = toWorkflowPatch(this.pendingCommands)
    try {
      const checked = await this.checkCurrentDraft()
      const blocking = checked.diagnostics.find((diagnostic) => diagnostic.severity === 'error')
      if (blocking) {
        const diagnostic = blocking as typeof blocking & {
          graphPath?: string[]
          nodeId?: string
          fieldPath?: string[]
        }
        throw toRPCError(
          {
            cause: {
              id: 'workflow.draft.invalid',
              category: 'validation',
              params: {
                code: diagnostic.code,
                graphPath: diagnostic.graphPath ?? [],
                nodeId: diagnostic.nodeId ?? '',
                fieldPath: diagnostic.fieldPath ?? [],
              },
            },
          },
          'workflow.checkDraft',
        )
      }
      if (commands.length === 0) {
        const persisted = await this.transport.getSource(this.workflowId)
        this.acceptSavedSource(persisted, pendingCount)
        this.phase = 'ready'
        return persisted
      }
      let patched
      try {
        patched = await this.transport.applyPatch(this.workflowId, this.baseRevision, commands)
      } catch (error) {
        const failure = describeWorkflowSaveError(error, commands)
        if (failure.kind !== 'revision') throw error
        const latest = await this.transport.getSource(this.workflowId)
        if (latest.revision === this.baseRevision) throw error
        patched = await this.transport.applyPatch(this.workflowId, latest.revision, commands)
      }
      this.acceptSavedSource(patched.source, pendingCount)
      this.phase = 'ready'
      return patched.source
    } catch (error) {
      const failure = describeWorkflowSaveError(error, commands)
      this.saveError = failure.message
      this.saveErrorKind = failure.kind
      this.saveErrorTarget = failure.target ?? null
      this.phase = 'ready'
      throw error
    }
  }

  async run(): Promise<RunView | null> {
    return this.start(false, [])
  }

  async startDebug(breakpoints: DebugBreakpoint[]): Promise<RunView | null> {
    return this.start(true, breakpoints)
  }

  async controlDebug(action: 'continue' | 'pause' | 'step'): Promise<DebugSnapshot> {
    if (!this.activeRun || !this.debugSnapshot) throw new Error('debug Run is unavailable')
    const runId = this.activeRun.runId
    const snapshot = await this.transport.controlDebugRun(runId, action)
    this.acceptDebugSnapshot(runId, snapshot)
    return this.debugSnapshot ?? snapshot
  }

  async setDebugBreakpoints(breakpoints: DebugBreakpoint[]): Promise<DebugSnapshot> {
    if (!this.activeRun || !this.debugSnapshot) throw new Error('debug Run is unavailable')
    const runId = this.activeRun.runId
    const snapshot = await this.transport.setDebugBreakpoints(runId, breakpoints)
    this.acceptDebugSnapshot(runId, snapshot)
    return this.debugSnapshot ?? snapshot
  }

  acceptDebugSnapshot(runId: string, snapshot: DebugSnapshot): boolean {
    if (runId !== this.activeRun?.runId || !this.debugSnapshot) {
      if (this.debugStartPending) {
        const pending = this.pendingDebugSnapshots.get(runId)
        if (!pending || snapshot.generation >= pending.generation) {
          this.pendingDebugSnapshots.set(runId, snapshot)
        }
      }
      return false
    }
    if (snapshot.generation < this.debugSnapshot.generation) return false
    this.debugSnapshot = snapshot
    return true
  }

  async refreshRun(): Promise<RunView | null> {
    if (!this.activeRun) return null
    this.activeRun = await this.transport.getRunTimeline(this.activeRun.runId)
    if (terminalStatus(this.activeRun.status)) this.phase = 'ready'
    return this.activeRun
  }

  async loadTimelinePage(page: number): Promise<RunView | null> {
    if (!this.activeRun) return null
    this.activeRun = await this.transport.getRunTimelinePage(this.activeRun.runId, page, 200)
    return this.activeRun
  }

  async cancelRun(): Promise<RunView | null> {
    if (!this.activeRun) return null
    this.activeRun = await this.transport.cancelRun(this.activeRun.runId)
    this.phase = 'ready'
    return this.activeRun
  }

  clearRunTrace(): boolean {
    if (!this.activeRun || !terminalStatus(this.activeRun.status)) return false
    this.activeRun = null
    this.debugSnapshot = null
    this.pendingDebugSnapshots.clear()
    return true
  }

  serialize(): string {
    return JSON.stringify(this.requireSource())
  }

  private async start(debug: boolean, breakpoints: DebugBreakpoint[]): Promise<RunView | null> {
    this.lastRunOutcome = null
    try {
      await this.refreshAuthoringProjection()
      if (this.dirty) await this.save()
      const checked = await this.checkCurrentDraft()
      if (checked.diagnostics.some((diagnostic) => diagnostic.severity === 'error')) return null
      if (!checked.programHash) throw new Error('workflow check produced no Program hash')
      this.phase = 'running'
      if (debug) {
        this.debugStartPending = true
        this.pendingDebugSnapshots.clear()
      }
      const started = debug
        ? await this.transport.startDebugRun(this.workflowId, breakpoints)
        : await this.transport.startRun(this.workflowId)
      this.lastRunOutcome = runStartOutcome(started)
      this.diagnostics = [...started.diagnostics]
      this.sourceHash = started.sourceHash ?? this.sourceHash
      this.compiledHash = started.programHash ?? this.compiledHash
      this.lastRunHash = started.programHash ?? ''
      this.activeRun = started.run ?? null
      this.debugSnapshot = started.debug ?? null
      if (debug && this.activeRun) {
        const pending = this.pendingDebugSnapshots.get(this.activeRun.runId)
        if (
          pending &&
          (!this.debugSnapshot || pending.generation >= this.debugSnapshot.generation)
        ) {
          this.debugSnapshot = pending
        }
      }
      this.debugStartPending = false
      this.pendingDebugSnapshots.clear()
      if (!this.activeRun) this.phase = 'ready'
      return this.activeRun
    } catch (error) {
      this.debugStartPending = false
      this.pendingDebugSnapshots.clear()
      this.phase = 'ready'
      throw error
    }
  }

  private loadAuthoring(raw: string): void {
    const parsed: unknown = JSON.parse(raw)
    if (!isAuthoringProjection(parsed)) throw new Error('unsupported node authoring projection')
    this.authoring = parsed
    this.projections.clear()
    this.typeProjections.clear()
    for (const projection of parsed.body.nodes) {
      this.projections.set(projection.nodeRef.nodeTypeId, projection)
    }
    for (const projection of parsed.body.types) {
      this.typeProjections.set(projection.typeRef.typeId, projection)
    }
  }

  private acceptSavedSource(view: SourceView, pendingCount: number): void {
    const remaining = this.pendingCommands.slice(pendingCount)
    const graphPath = [...this.graphPath]
    this.acceptSource(view)
    for (const pending of remaining) {
      this.graphPath = [pending.graphId]
      this.apply(pending.command)
    }
    const graphs = new Set(this.requireSource().graphs.map((graph) => graph.id))
    this.graphPath = graphPath.filter((id) => graphs.has(id))
    if (!this.graphPath.length) this.graphPath = [this.requireSource().entryGraph]
  }

  private acceptSource(view: SourceView): void {
    if (!view.sourceJson) throw new Error('Workflow Source response omitted sourceJson')
    const parsed: unknown = JSON.parse(view.sourceJson)
    if (!isWorkflowSource(parsed) || parsed.workflow.id !== view.workflowId) {
      throw new Error('Workflow Source response has invalid identity')
    }
    for (const graph of parsed.graphs) normalizeGraph(graph)
    this.savedSource = { ...view }
    this.source = parsed
    this.baseRevision = view.revision
    this.sourceHash = view.sourceHash
    this.graphPath = [parsed.entryGraph]
    this.history.length = 0
    this.future.length = 0
    this.pendingCommands.length = 0
    this.revertedCommands.length = 0
    this.durableNodeKeys.clear()
    for (const graph of parsed.graphs) {
      for (const node of graph.nodes) this.durableNodeKeys.add(nodeKey(graph.id, node.id))
    }
    this.dirty = false
    this.dismissSaveError()
    this.openFailure = ''
    this.resetCompileFacts()
  }

  private upgradeCompatibleNodeContracts(): void {
    const source = this.requireSource()
    let upgraded = false
    const commands: PendingCommand[] = []
    for (const graph of source.graphs) {
      for (const node of graph.nodes) {
        const projection = this.projections.get(node.nodeRef.nodeTypeId)
        if (
          !projection ||
          projection.nodeRef.semanticDigest === node.nodeRef.semanticDigest ||
          !applyCompatibleNodeContractUpgrade(graph, node, projection)
        ) {
          continue
        }
        if (this.durableNodeKeys.has(nodeKey(graph.id, node.id))) {
          commands.push({
            graphId: graph.id,
            command: { kind: 'upgrade-node-contract', nodeId: node.id },
          })
        }
        upgraded = true
      }
    }
    if (!upgraded) return
    this.pendingCommands.unshift(...commands)
    source.revision = this.baseRevision + 1
    this.dirty = true
    this.resetCompileFacts()
  }

  private async refreshAuthoringProjection(): Promise<void> {
    this.loadAuthoring(await this.transport.getAuthoringProjection())
    this.upgradeCompatibleNodeContracts()
  }

  private requireCurrentSubgraph(): Graph {
    const graph = this.currentGraph
    if (!graph || graph.kind !== 'subgraph') throw new Error('open a subgraph first')
    return graph
  }

  private updateCurrentGraphInterface(draft: GraphInterfaceDraft): void {
    this.apply({ kind: 'update-graph-interface', ...draft })
  }

  private graphInterfaceElements(graph: Graph): GraphInterfaceElement[] {
    const elements: GraphInterfaceElement[] = []
    for (const node of graph.nodes) {
      const projection = this.nodeInstanceProjection(node)
      if (!projection) continue
      elements.push({
        id: node.id,
        label: node.label?.trim() || node.id,
        dataInputs: projection.dataInputs.map((port) => ({
          id: port.id,
          name: port.id,
          type: clone(port.type.expression),
        })),
        dataOutputs: projection.dataOutputs.map((port) => ({
          id: port.id,
          name: port.id,
          type: clone(port.type.expression),
        })),
        signals: projection.signals.map((signal) => ({ ...signal })),
        bindings: node.bindings,
      })
    }
    for (const call of graph.calls ?? []) {
      const callee = this.calleeGraph(call)
      if (!callee) continue
      elements.push({
        id: call.id,
        label: call.label?.trim() || callee.name?.trim() || call.id,
        dataInputs: callee.inputs.map((port) => ({
          id: port.id,
          name: port.name?.trim() || port.id,
          type: clone(port.type),
        })),
        dataOutputs: callee.outputs.map((port) => ({
          id: port.id,
          name: port.name?.trim() || port.id,
          type: clone(port.type),
        })),
        signals: [
          { id: 'in', name: 'in', channel: 'exec', direction: 'input' },
          ...(callee.exits ?? []).map((exit) => ({
            id: exit.id,
            name: exit.name?.trim() || exit.id,
            channel: exit.channel,
            direction: 'output' as const,
          })),
        ],
        bindings: call.bindings,
      })
    }
    return elements
  }

  private resetCompileFacts(): void {
    this.compiledHash = ''
    this.diagnostics = []
  }

  private requireSource(): YottaWorkflowSource {
    if (!this.source) throw new Error('EditorSession has no Workflow Source')
    return this.source
  }

  private failOpen(error: unknown): void {
    this.phase = 'failed'
    this.openFailure = errorText(error)
  }
}

function collectStateReferences(
  source: YottaWorkflowSource,
  name: string,
): StateReferenceLocation[] {
  const references: StateReferenceLocation[] = []
  for (const graph of source.graphs) {
    for (const node of graph.nodes) {
      if (!node.nodeRef.nodeTypeId.includes('/nodes/state/') || node.config.variable !== name)
        continue
      references.push({
        graphId: graph.id,
        nodeId: node.id,
        mode:
          node.nodeRef.nodeTypeId.endsWith('/write') ||
          node.nodeRef.nodeTypeId.endsWith('/increment')
            ? 'write'
            : 'read',
      })
    }
  }
  return references
}

function connectionCompatibilityInGraph(
  source: YottaWorkflowSource,
  graph: Graph,
  edge: Edge,
  projections: ReadonlyMap<string, NodeProjection>,
  types: ReadonlyMap<string, TypeProjection>,
  nodes: readonly NodeProjection[],
): ConnectionCompatibility {
  const fromNode = graph.nodes.find((node) => node.id === edge.from.nodeId)
  const toNode = graph.nodes.find((node) => node.id === edge.to.nodeId)
  const fromCall = graph.calls!.find((call) => call.id === edge.from.nodeId)
  const toCall = graph.calls!.find((call) => call.id === edge.to.nodeId)
  if ((!fromNode && !fromCall) || (!toNode && !toCall))
    return { valid: false, issue: 'port', message: 'connection node is missing' }
  if (fromCall || toCall) {
    if (edge.channel === 'data') {
      const fromType = fromCall
        ? source.graphs
            .find((candidate) => candidate.id === fromCall.graphId)
            ?.outputs.find((port) => port.id === edge.from.portId)?.type
        : projections
            .get(fromNode!.nodeRef.nodeTypeId)
            ?.dataOutputs.find((port) => port.id === edge.from.portId)?.type.expression
      const toType = toCall
        ? source.graphs
            .find((candidate) => candidate.id === toCall.graphId)
            ?.inputs.find((port) => port.id === edge.to.portId)?.type
        : projections
            .get(toNode!.nodeRef.nodeTypeId)
            ?.dataInputs.find((port) => port.id === edge.to.portId)?.type.expression
      return fromType && toType && assignable(fromType, toType, types)
        ? { valid: true, disposition: 'direct' }
        : {
            valid: false,
            issue: 'type',
            message: 'connection types are incompatible',
            disposition: 'incompatible',
          }
    }
    const fromValid = fromCall
      ? source.graphs
          .find((candidate) => candidate.id === fromCall.graphId)
          ?.exits?.some((exit) => exit.id === edge.from.portId && exit.channel === edge.channel)
      : projections
          .get(fromNode!.nodeRef.nodeTypeId)
          ?.signals.some(
            (signal) =>
              signal.id === edge.from.portId &&
              signal.direction === 'output' &&
              signal.channel === edge.channel,
          )
    const toValid = toCall
      ? edge.channel === 'exec' && edge.to.portId === 'in'
      : projections
          .get(toNode!.nodeRef.nodeTypeId)
          ?.signals.some((signal) => signal.id === edge.to.portId && signal.direction === 'input')
    return fromValid && toValid
      ? { valid: true }
      : { valid: false, issue: 'port', message: 'connection ports are incompatible' }
  }
  const fromBase = projections.get(fromNode!.nodeRef.nodeTypeId)
  const toBase = projections.get(toNode!.nodeRef.nodeTypeId)
  if (!fromBase || !toBase)
    return { valid: false, issue: 'port', message: 'connection projection is missing' }
  const from = resolveNodeInstanceProjection(source, fromNode!, fromBase, projections, types)
  const to = resolveNodeInstanceProjection(source, toNode!, toBase, projections, types)
  return projectedConnectionCompatibility(
    from,
    { channel: edge.channel, direction: 'output', portId: edge.from.portId },
    to,
    { channel: edge.channel, direction: 'input', portId: edge.to.portId },
    types,
    nodes,
  )
}

function resolveEditorCommand(
  command: EditorCommand,
  graph: Graph,
  idFactory: () => string,
): EditorCommand {
  if (command.kind !== 'add-node' || command.nodeId) return command
  return { ...command, nodeId: uniqueNodeId(graph, idFactory) }
}

function terminalStatus(status: string): boolean {
  return ['SUCCEEDED', 'FAILED', 'CANCELLED', 'INTERRUPTED'].includes(status.toUpperCase())
}

function isWorkflowSource(value: unknown): value is YottaWorkflowSource {
  if (typeof value !== 'object' || value === null) return false
  const source = value as Record<string, unknown>
  return (
    source.format === 'yotta.workflow' &&
    source.version === '5' &&
    typeof source.revision === 'number' &&
    typeof source.entryGraph === 'string' &&
    Array.isArray(source.graphs) &&
    Array.isArray(source.resources) &&
    Array.isArray(source.targetProfileDefinitions) &&
    Array.isArray(source.credentialRequirements) &&
    Array.isArray(source.dependencies) &&
    Array.isArray(source.variables) &&
    typeof source.workflow === 'object' &&
    source.workflow !== null
  )
}

function isAuthoringProjection(value: unknown): value is YottaNodeAuthoringProjection {
  if (typeof value !== 'object' || value === null) return false
  const document = value as Record<string, unknown>
  if (
    document.format !== 'yotta.node-authoring-projection' ||
    document.version !== '1' ||
    typeof document.projectionDigest !== 'string' ||
    typeof document.body !== 'object' ||
    document.body === null
  ) {
    return false
  }
  const body = document.body as Record<string, unknown>
  return Array.isArray(body.nodes) && Array.isArray(body.types)
}

function clone<T>(value: T): T {
  return structuredClone(value)
}

function errorText(error: unknown): string {
  return errorMessage(error)
}

export type {
  Edge,
  Graph,
  InputBinding,
  Node,
  NodeProjection,
  WorkflowResource,
  YottaWorkflowSource,
}
