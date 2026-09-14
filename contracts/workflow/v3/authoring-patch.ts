/* Generated from current Workflow Authoring Patch Go types. Do not edit. */

export type Command =
  | {
      kind: 'rename-workflow'
      renameWorkflow: RenameWorkflowCommand
    }
  | {
      kind: 'update-workflow-metadata'
      updateWorkflowMetadata: UpdateWorkflowMetadataCommand
    }
  | {
      kind: 'set-target-default'
      setTargetDefault: SetTargetDefaultCommand
    }
  | {
      clearTargetDefault: ClearTargetDefaultCommand
      kind: 'clear-target-default'
    }
  | {
      addStateVariable: AddStateVariableCommand
      kind: 'add-state-variable'
    }
  | {
      kind: 'update-state-variable'
      updateStateVariable: UpdateStateVariableCommand
    }
  | {
      kind: 'set-parameter-blocks'
      setParameterBlocks: SetParameterBlocksCommand
    }
  | {
      kind: 'remove-state-variable'
      removeStateVariable: RemoveStateVariableCommand
    }
  | {
      addNode: AddNodeCommand
      kind: 'add-node'
    }
  | {
      kind: 'upgrade-node-contract'
      upgradeNodeContract: NodeCommand
    }
  | {
      kind: 'remove-node'
      removeNode: NodeCommand
    }
  | {
      kind: 'move-node'
      moveNode: MoveNodeCommand
    }
  | {
      kind: 'set-node-label'
      setNodeLabel: SetNodeLabelCommand
    }
  | {
      kind: 'set-node-disabled'
      setNodeDisabled: SetNodeDisabledCommand
    }
  | {
      kind: 'set-config'
      setConfig: SetConfigCommand
    }
  | {
      clearConfig: FieldCommand
      kind: 'clear-config'
    }
  | {
      bindValue: BindValueCommand
      kind: 'bind-value'
    }
  | {
      bindDefault: PortCommand
      kind: 'bind-default'
    }
  | {
      bindBlob: BindBlobCommand
      kind: 'bind-blob'
    }
  | {
      bindResource: BindResourceCommand
      kind: 'bind-resource'
    }
  | {
      addResource: AddResourceCommand
      kind: 'add-resource'
    }
  | {
      kind: 'replace-resource'
      replaceResource: ReplaceResourceCommand
    }
  | {
      kind: 'update-resource-metadata'
      updateResourceMetadata: UpdateResourceMetadataCommand
    }
  | {
      kind: 'remove-resource'
      removeResource: RemoveResourceCommand
    }
  | {
      clearBinding: PortCommand
      kind: 'clear-binding'
    }
  | {
      connect: EdgeCommand
      kind: 'connect'
    }
  | {
      disconnect: EdgeCommand
      kind: 'disconnect'
    }
  | {
      addGraph: AddGraphCommand
      kind: 'add-graph'
    }
  | {
      kind: 'rename-graph'
      renameGraph: RenameGraphCommand
    }
  | {
      kind: 'remove-graph'
      removeGraph: GraphCommand
    }
  | {
      kind: 'update-graph-interface'
      updateGraphInterface: GraphInterfaceCommand
    }
  | {
      addGraphCall: GraphCallCommand
      kind: 'add-graph-call'
    }
  | {
      kind: 'update-graph-call'
      updateGraphCall: GraphCallCommand
    }
  | {
      kind: 'remove-graph-call'
      removeGraphCall: CallCommand
    }
  | {
      addAnnotation: AnnotationCommand
      kind: 'add-annotation'
    }
  | {
      kind: 'update-annotation'
      updateAnnotation: AnnotationCommand
    }
  | {
      kind: 'remove-annotation'
      removeAnnotation: AnnotationIDCommand
    }
  | {
      kind: 'set-edge-reroutes'
      setEdgeReroutes: SetEdgeReroutesCommand
    }
  | {
      collapseSelection: CollapseSelectionCommand
      kind: 'collapse-selection'
    }
export type JSONValue =
  | null
  | boolean
  | number
  | string
  | JSONValue[]
  | {
      [k: string]: JSONValue
    }
export type TypeExpression =
  | {
      kind: 'ref'
      ref: TypeRef
    }
  | {
      element: TypeExpression
      kind: 'list'
    }
  | {
      kind: 'union'
      /**
       * @minItems 2
       * @maxItems 64
       */
      members: [TypeExpression, TypeExpression, ...TypeExpression[]]
    }
  | {
      /**
       * @maxItems 32
       */
      constraints?: string[]
      kind: 'variable'
      variable: string
    }
export type PatchNodeReference = string | string

export interface YottaWorkflowAuthoringPatch {
  baseRevision: number
  /**
   * @minItems 1
   * @maxItems 256
   */
  commands: [Command, ...Command[]]
  workflowId: string
}
export interface RenameWorkflowCommand {
  name: string
}
export interface UpdateWorkflowMetadataCommand {
  category: string
  description: string
  name: string
  tags: string[]
}
export interface SetTargetDefaultCommand {
  slot: string
  target: string
}
export interface ClearTargetDefaultCommand {
  target: string
}
export interface AddStateVariableCommand {
  default: JSONValue
  name: string
  parameter?: Parameter
  type: TypeExpression
}
export interface Parameter {
  control: 'auto' | 'select' | 'multiselect'
  description?: string
  group?: string
  id: string
  label: string
  maximum?: number
  minimum?: number
  /**
   * @maxItems 256
   */
  options?: ParameterOption[]
  order?: number
  required?: boolean
  visibleWhen?: ParameterCondition
}
export interface ParameterOption {
  label: string
  value: any
}
export interface ParameterCondition {
  equals: any
  parameterId: string
}
export interface TypeRef {
  semanticDigest: string
  typeId: string
}
export interface UpdateStateVariableCommand {
  clearParameter?: boolean
  default: any
  name: string
  parameter?: Parameter
  type: TypeExpression
}
export interface SetParameterBlocksCommand {
  /**
   * @maxItems 4096
   */
  blocks: ParameterBlock[]
}
export interface ParameterBlock {
  id: string
  kind: 'label' | 'separator'
  label?: string
  order?: number
}
export interface RemoveStateVariableCommand {
  name: string
}
export interface AddNodeCommand {
  graphId: string
  handle?: string
  nodeTypeId: string
  position: Position
}
export interface Position {
  x: number
  y: number
}
export interface NodeCommand {
  graphId: string
  nodeId: string
}
export interface MoveNodeCommand {
  graphId: string
  nodeId: string
  position: Position
}
export interface SetNodeLabelCommand {
  graphId: string
  label: string
  nodeId: string
}
export interface SetNodeDisabledCommand {
  disabled: boolean
  graphId: string
  nodeId: string
}
export interface SetConfigCommand {
  fieldId: string
  graphId: string
  nodeId: string
  value: JSONValue
}
export interface FieldCommand {
  fieldId: string
  graphId: string
  nodeId: string
}
export interface BindValueCommand {
  graphId: string
  nodeId: string
  portId: string
  value: JSONValue
}
export interface PortCommand {
  graphId: string
  nodeId: string
  portId: string
}
export interface BindBlobCommand {
  blob: BlobRef
  graphId: string
  nodeId: string
  portId: string
}
export interface BlobRef {
  digest: string
  mediaType: string
  size: number
}
export interface BindResourceCommand {
  graphId: string
  nodeId: string
  portId: string
  resource: ResourceBinding
}
export interface ResourceBinding {
  resourceId: string
  variantId?: string
}
export interface AddResourceCommand {
  resource: WorkflowResource
}
export interface WorkflowResource {
  category?: string
  description?: string
  id: string
  image?: ImageResource
  inputClip?: InputClipResource
  kind: 'image' | 'macro' | 'input-clip'
  macro?: MacroResource
  name: string
  /**
   * @maxItems 64
   */
  tags?: string[]
}
export interface ImageResource {
  /**
   * @minItems 1
   * @maxItems 256
   */
  variants: [ImageResourceVariant, ...ImageResourceVariant[]]
}
export interface ImageResourceVariant {
  /**
   * @minItems 4
   * @maxItems 4
   */
  bbox: [number, number, number, number]
  blob: BlobRef
  id: string
  /**
   * @maxItems 256
   */
  regions?: [number, number, number, number][]
  /**
   * @minItems 2
   * @maxItems 2
   */
  resolution: [number, number]
}
export interface InputClipResource {
  /**
   * @minItems 2
   * @maxItems 2
   */
  baseResolution: [number, number]
  blob: BlobRef
  durationUs: number
  eventCount: number
  mouseCounts360: number
  mouseMode: 'relative' | 'absolute' | 'mixed'
  recordingMode: 'simple' | 'precise'
  stopHotkeyVk: number
}
export interface MacroResource {
  actionCount: number
  /**
   * @minItems 2
   * @maxItems 2
   */
  baseResolution: [number, number]
  blob: BlobRef
  durationUs: number
}
export interface ReplaceResourceCommand {
  resource: WorkflowResource
  resourceId: string
}
export interface UpdateResourceMetadataCommand {
  category: string
  description: string
  name: string
  resourceId: string
  tags: string[]
}
export interface RemoveResourceCommand {
  resourceId: string
}
export interface EdgeCommand {
  edge: PatchEdge
  graphId: string
}
export interface PatchEdge {
  channel: 'data' | 'exec' | 'error'
  from: PatchEndpoint
  presentation?: EdgePresentation
  to: PatchEndpoint
}
export interface PatchEndpoint {
  nodeId: PatchNodeReference
  portId: string
}
export interface EdgePresentation {
  /**
   * @maxItems 64
   */
  reroutes?: Position[]
}
export interface AddGraphCommand {
  graph: Graph
}
export interface Graph {
  /**
   * @maxItems 4096
   */
  annotations?: Annotation[]
  /**
   * @maxItems 4096
   */
  calls?: GraphCall[]
  /**
   * @maxItems 16384
   */
  edges: Edge[]
  /**
   * @maxItems 1
   */
  entries?: [] | [Endpoint]
  /**
   * @maxItems 64
   */
  exits?: GraphExit[]
  id: string
  /**
   * @maxItems 4096
   */
  inputs: GraphPort[]
  kind: 'main' | 'subgraph'
  name?: string
  /**
   * @maxItems 4096
   */
  nodes: Node[]
  /**
   * @maxItems 4096
   */
  outputs: GraphPort[]
}
export interface Annotation {
  color?: string
  id: string
  position: Position
  size: Size
  text: string
}
export interface Size {
  height: number
  width: number
}
export interface GraphCall {
  bindings: {
    [k: string]: InputBinding
  }
  graphId: string
  id: string
  label?: string
  position: Position
}
export interface InputBinding {
  blob?: BlobRef
  kind: 'value' | 'default' | 'blob' | 'resource'
  resource?: ResourceBinding
  value?: any
}
export interface Edge {
  channel: 'data' | 'exec' | 'error'
  from: Endpoint
  presentation?: EdgePresentation
  to: Endpoint
}
export interface Endpoint {
  nodeId: string
  portId: string
}
export interface GraphExit {
  channel: 'exec' | 'error'
  endpoint: Endpoint
  id: string
  name?: string
}
export interface GraphPort {
  id: string
  name?: string
  nodeId: string
  portId: string
  type: TypeExpression
}
export interface Node {
  bindings: {
    [k: string]: InputBinding
  }
  config: {
    [k: string]: any
  }
  disabled?: boolean
  id: string
  label?: string
  nodeRef: NodeRef
  position: Position
}
export interface NodeRef {
  nodeTypeId: string
  semanticDigest: string
  version: string
}
export interface RenameGraphCommand {
  graphId: string
  name: string
}
export interface GraphCommand {
  graphId: string
}
export interface GraphInterfaceCommand {
  entries: Endpoint[]
  exits: GraphExit[]
  graphId: string
  inputs: GraphPort[]
  outputs: GraphPort[]
}
export interface GraphCallCommand {
  call: GraphCall
  graphId: string
}
export interface CallCommand {
  callId: string
  graphId: string
}
export interface AnnotationCommand {
  annotation: Annotation
  graphId: string
}
export interface AnnotationIDCommand {
  annotationId: string
  graphId: string
}
export interface SetEdgeReroutesCommand {
  edge: PatchEdge
  graphId: string
  reroutes: Position[]
}
export interface CollapseSelectionCommand {
  callId: string
  graphId: string
  name: string
  nodeIds: string[]
  position: Position
  subgraphId: string
}
