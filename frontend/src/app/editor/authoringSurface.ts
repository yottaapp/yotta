import type {
  ConfiguredTargetProjection,
  FieldProjection,
  NodeProjection,
  PortProjection,
} from '../../../../contracts/node/current/authoring-projection'
import type { Node } from './EditorSession'
import { isKeyChordType } from './keyChord'

export type AuthoringGroup = 'required' | 'common' | 'advanced' | 'output'
export type ValueEditorAdapter =
  | 'asset'
  | 'color-range'
  | 'duration'
  | 'key-chord'
  | 'multiline-text'
  | 'point'
  | 'region'
  | 'select'
  | 'toggle'
  | 'number'
  | 'text'
  | 'json'

export type AuthoringSurfaceItem =
  | {
      key: string
      kind: 'config'
      field: FieldProjection
      group: AuthoringGroup
      order: number
      importance: string
      editorAdapter: ValueEditorAdapter
      inlinePriority: number
    }
  | {
      key: string
      kind: 'input'
      port: PortProjection
      group: AuthoringGroup
      order: number
      importance: string
      editorAdapter: ValueEditorAdapter
      inlinePriority: number
    }
  | {
      key: string
      kind: 'output'
      port: PortProjection
      group: AuthoringGroup
      order: number
      importance: string
      editorAdapter: ValueEditorAdapter
      inlinePriority: number
    }

export interface AuthoringSurface {
  groups: Record<AuthoringGroup, AuthoringSurfaceItem[]>
  inlineInputs: Extract<AuthoringSurfaceItem, { kind: 'input' }>[]
}

const groups: AuthoringGroup[] = ['required', 'common', 'advanced', 'output']
const inputClipTypeID = 'https://schemas.yotta.dev/types/automation/input-clip/v1'
const macroTypeID = 'https://schemas.yotta.dev/types/automation/macro/v1'
const compactInlineAdapters = new Set<ValueEditorAdapter>([
  'duration',
  'key-chord',
  'number',
  'select',
  'text',
  'toggle',
])

export function projectAuthoringSurface(
  projection: NodeProjection,
  _node?: Node,
  connectedInputIDs: ReadonlySet<string> = new Set(),
): AuthoringSurface {
  const result: AuthoringSurface = {
    groups: { required: [], common: [], advanced: [], output: [] },
    inlineInputs: [],
  }
  const targetFields = new Set(
    [
      ...(projection.configuredTargets ?? []).map((target) => target.slotConfigKey),
      ...projection.capabilities.map((capability) => capability.targetSlotConfigKey),
    ].filter((field): field is string => Boolean(field)),
  )

  projection.configFields.forEach((field, index) => {
    const item: Extract<AuthoringSurfaceItem, { kind: 'config' }> = {
      key: `config:${field.id}`,
      kind: 'config',
      field,
      group: resolveGroup(field.group, field.importance, field.required, field.hasDefault),
      order: field.order || index + 1,
      importance: field.importance || (field.required ? 'primary' : 'common'),
      editorAdapter: targetFields.has(field.id) ? 'select' : resolveFieldAdapter(field),
      inlinePriority: field.inlinePriority || 0,
    }
    result.groups[item.group].push(item)
  })

  projection.dataInputs.forEach((port, index) => {
    const item: Extract<AuthoringSurfaceItem, { kind: 'input' }> = {
      key: `input:${port.id}`,
      kind: 'input',
      port,
      group: resolveGroup(
        port.group,
        port.importance,
        port.binding === 'required',
        port.hasDefault,
      ),
      order: port.order || index + 1,
      importance: port.importance || (port.binding === 'required' ? 'primary' : 'common'),
      editorAdapter: resolvePortAdapter(port),
      inlinePriority: port.inlinePriority || 0,
    }
    result.groups[item.group].push(item)
    if (
      item.inlinePriority > 0 &&
      compactInlineAdapters.has(item.editorAdapter) &&
      !connectedInputIDs.has(port.id) &&
      port.type.representations.some((representation) => representation.kind === 'inline-json')
    ) {
      result.inlineInputs.push(item)
    }
  })

  projection.dataOutputs.forEach((port, index) => {
    result.groups.output.push({
      key: `output:${port.id}`,
      kind: 'output',
      port,
      group: 'output',
      order: port.order || index + 1,
      importance: port.importance || 'common',
      editorAdapter: resolvePortAdapter(port),
      inlinePriority: 0,
    })
  })

  for (const group of groups) {
    result.groups[group].sort(compareSurfaceItems)
  }
  result.inlineInputs.sort(
    (left, right) => right.inlinePriority - left.inlinePriority || compareSurfaceItems(left, right),
  )
  result.inlineInputs = result.inlineInputs.slice(0, 3)

  return result
}

export function resolvePortAdapter(port: PortProjection): ValueEditorAdapter {
  const explicit = port.editorAdapter || port.type.editorAdapter
  if (isAdapter(explicit)) return explicit
  if (port.editorAdapter === 'template-image') return 'asset'
  if (
    port.type.typeIds.includes(inputClipTypeID) ||
    port.type.typeIds.includes(macroTypeID) ||
    port.type.representations.some((representation) => representation.kind === 'blob-ref')
  ) {
    return 'asset'
  }
  if (isKeyChordType(port.type.expression)) return 'key-chord'
  return controlAdapter(port.type.control)
}

export function effectiveTargetSlot(
  projection: NodeProjection,
  node: Node,
  defaults: readonly { target: string; slot: string }[],
): string {
  const target = automationConfiguredTarget(projection.configuredTargets ?? [])
  if (!target) return defaults.find((candidate) => candidate.target === 'target')?.slot ?? ''
  const fieldID = target.slotConfigKey
  const override = fieldID ? node.config[fieldID] : undefined
  if (typeof override === 'string' && override) return override
  return (
    defaults.find((candidate) => candidate.target === target.targetSlot)?.slot ??
    (target.targetSlot === 'application'
      ? defaults.find((candidate) => candidate.target === 'target')?.slot
      : '') ??
    ''
  )
}

function automationConfiguredTarget(
  targets: ConfiguredTargetProjection[],
): ConfiguredTargetProjection | undefined {
  return targets.find((target) =>
    target.targetKinds.some((kind) =>
      [
        'desktop-window',
        'win32-window',
        'android-device',
        'browser-cdp',
        'configured-application',
      ].includes(kind),
    ),
  )
}

function resolveFieldAdapter(field: FieldProjection): ValueEditorAdapter {
  if (isAdapter(field.editorAdapter)) return field.editorAdapter
  return controlAdapter(field.control)
}

function controlAdapter(control: string): ValueEditorAdapter {
  if (control === 'select' || control === 'state-variable') return 'select'
  if (control === 'toggle') return 'toggle'
  if (control === 'number' || control === 'integer') return 'number'
  if (control === 'text' || control === 'code') return 'text'
  return 'json'
}

function resolveGroup(
  group: string | undefined,
  importance: string | undefined,
  required: boolean,
  hasDefault: boolean,
): AuthoringGroup {
  if (group && groups.includes(group as AuthoringGroup)) return group as AuthoringGroup
  if (importance === 'advanced') return 'advanced'
  if (required && !hasDefault) return 'required'
  return 'common'
}

function isAdapter(value: string | undefined): value is ValueEditorAdapter {
  return Boolean(
    value &&
    [
      'asset',
      'color-range',
      'duration',
      'key-chord',
      'multiline-text',
      'point',
      'region',
      'select',
      'toggle',
      'number',
      'text',
      'json',
    ].includes(value),
  )
}

function compareSurfaceItems(left: AuthoringSurfaceItem, right: AuthoringSurfaceItem): number {
  return left.order - right.order || left.key.localeCompare(right.key)
}
