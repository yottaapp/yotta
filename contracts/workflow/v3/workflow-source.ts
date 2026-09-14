/* Generated from WorkflowSource Go types. Do not edit. */

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

export interface YottaWorkflowSource {
  /**
   * @maxItems 4096
   */
  credentialRequirements: CredentialRequirement[]
  /**
   * @maxItems 4096
   */
  dependencies: NodePackageDependency[]
  derivedFrom?: WorkflowReleaseOrigin
  entryGraph: string
  format: 'yotta.workflow'
  /**
   * @minItems 1
   * @maxItems 256
   */
  graphs: [Graph, ...Graph[]]
  /**
   * @maxItems 4096
   */
  parameterBlocks?: ParameterBlock[]
  /**
   * @maxItems 4096
   */
  resources: WorkflowResource[]
  revision: number
  /**
   * @maxItems 64
   */
  targetDefaults?: TargetDefault[]
  /**
   * @maxItems 64
   */
  targetProfileDefinitions: TargetProfileDefinition[]
  /**
   * @maxItems 4096
   */
  variables: Variable[]
  version: '3'
  workflow: Workflow
}
export interface CredentialRequirement {
  kind: string
  purpose: string
  slot: string
}
export interface NodePackageDependency {
  manifestDigest: string
  /**
   * @minItems 1
   * @maxItems 4096
   */
  nodeRefs: [NodeRef, ...NodeRef[]]
  packageId: string
  packageVersion: string
  publisherNamespace: string
}
export interface NodeRef {
  nodeTypeId: string
  semanticDigest: string
  version: string
}
export interface WorkflowReleaseOrigin {
  attestationDigest: string
  publisherNamespace: string
  releaseDigest: string
  releaseVersion: string
  sourceHash: string
  workflowId: string
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
export interface Position {
  x: number
  y: number
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
export interface BlobRef {
  digest: string
  mediaType: string
  size: number
}
export interface ResourceBinding {
  resourceId: string
  variantId?: string
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
export interface EdgePresentation {
  /**
   * @maxItems 64
   */
  reroutes?: Position[]
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
export interface TypeRef {
  semanticDigest: string
  typeId: string
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
export interface ParameterBlock {
  id: string
  kind: 'label' | 'separator'
  label?: string
  order?: number
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
export interface TargetDefault {
  slot: string
  target: string
}
export interface TargetProfileDefinition {
  adapterKind: string
  description?: string
  /**
   * @maxItems 64
   */
  discoveryHints: TargetDiscoveryHint[]
  id: string
  initialDefaults: any
  name: string
  profileVersion: string
  /**
   * @minItems 1
   * @maxItems 256
   */
  settingsSchemaBundle: [Resource, ...Resource[]]
  settingsSchemaRoot: string
  targetKind: string
}
export interface TargetDiscoveryHint {
  kind: 'application-name' | 'executable-name' | 'window-title' | 'android-package' | 'device-model' | 'browser-host'
  value: string
}
export interface Resource {
  id: string
  schema: any
}
export interface Variable {
  default: any
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
export interface Workflow {
  category?: string
  createdAt?: string
  description?: string
  id: string
  name: string
  /**
   * @maxItems 64
   */
  tags?: string[]
  updatedAt?: string
}
