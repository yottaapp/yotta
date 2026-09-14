import { i18n } from '@/i18n'
import { normalizeError } from '@/lib/invoke'
import type { Command as WorkflowPatchCommand } from '../../../../contracts/workflow/current/authoring-patch'

export type WorkflowSaveErrorKind = 'revision' | 'validation' | 'unknown'

export interface WorkflowSaveErrorTarget {
  graphId: string
  nodeId: string
  fieldId?: string
  portId?: string
}

export interface WorkflowSaveError {
  kind: WorkflowSaveErrorKind
  message: string
  target?: WorkflowSaveErrorTarget
}

const validationCodes = new Set([
  'INVALID_TARGET',
  'REFERENCE_IN_USE',
  'INVALID_FIELD',
  'MISSING_REQUIRED_FIELD',
  'UNKNOWN_FIELD',
  'INVALID_EDGE',
  'UNKNOWN_NODE',
  'UNKNOWN_HANDLE',
  'INVALID_COMMAND',
  'INVALID_CONFIG_VALUE',
  'UNKNOWN_CONFIG_FIELD',
  'REQUIRED_CONFIG_FIELD',
  'INVALID_BINDING_VALUE',
  'UNKNOWN_DATA_INPUT',
  'INVALID_BLOB_REF',
  'INVALID_RESOURCE_BINDING',
  'INVALID_POSITION',
  'INVALID_LABEL',
])

export function describeWorkflowSaveError(
  error: unknown,
  commands: readonly WorkflowPatchCommand[] = [],
): WorkflowSaveError {
  const normalized = normalizeError(error)
  const params = normalized.params
  const code = firstErrorCode(normalized, params)
  const commandIndex = typeof params?.commandIndex === 'number' ? params.commandIndex : undefined
  const target =
    targetFromProblemParams(params) ??
    (commandIndex !== undefined ? targetFromCommand(commands[commandIndex]) : undefined)
  if (normalized.id === 'workflow.revision.conflict') {
    return { kind: 'revision', message: i18n.global.t('workflow.editor.revision_conflict') }
  }

  if (code === 'INVALID_TARGET' || code === 'REFERENCE_IN_USE') {
    return {
      kind: 'validation',
      message: i18n.global.t(`workflow.editor.save_error.${code}`),
      target,
    }
  }

  if (validationCodes.has(code) || normalized.category === 'validation') {
    return {
      kind: 'validation',
      message: target
        ? targetMessage(target)
        : i18n.global.te(`workflow.editor.save_error.${code}`)
          ? i18n.global.t(`workflow.editor.save_error.${code}`)
          : i18n.global.t('workflow.editor.save_error.validation'),
      target,
    }
  }

  if (code) {
    return {
      kind: 'unknown',
      message: i18n.global.t('workflow.editor.save_error.internal', { code }),
    }
  }

  return {
    kind: 'unknown',
    message: i18n.global.t('workflow.editor.save_error.unknown'),
  }
}

function targetFromProblemParams(
  params: Record<string, unknown> | undefined,
): WorkflowSaveErrorTarget | undefined {
  if (!params || typeof params.nodeId !== 'string' || !params.nodeId) return undefined
  const graphPath = Array.isArray(params.graphPath)
    ? params.graphPath.filter((item): item is string => typeof item === 'string')
    : []
  const fieldPath = Array.isArray(params.fieldPath)
    ? params.fieldPath.filter((item): item is string => typeof item === 'string')
    : []
  const graphId = graphPath.at(-1)
  if (!graphId) return undefined
  return {
    graphId,
    nodeId: params.nodeId,
    fieldId: fieldPath.at(-1),
  }
}

function firstErrorCode(
  normalized: ReturnType<typeof normalizeError>,
  params: Record<string, unknown> | undefined,
): string {
  const diagnosticCode = normalized.errors?.[0]?.code
  if (diagnosticCode) return diagnosticCode
  if (typeof params?.code === 'string' && params.code) return params.code
  return normalized.id ?? ''
}

function targetMessage(target: WorkflowSaveErrorTarget): string {
  if (target.fieldId) {
    return i18n.global.t('workflow.editor.save_error.node_field', { field: target.fieldId })
  }
  if (target.portId) {
    return i18n.global.t('workflow.editor.save_error.node_input', { input: target.portId })
  }
  return i18n.global.t('workflow.editor.save_error.node')
}

function targetFromCommand(
  command: WorkflowPatchCommand | undefined,
): WorkflowSaveErrorTarget | undefined {
  if (!command) return undefined
  const payload = Object.values(command).find(
    (value): value is Record<string, unknown> =>
      typeof value === 'object' && value !== null && !Array.isArray(value),
  )
  if (!payload) return undefined

  const graphId = typeof payload.graphId === 'string' ? payload.graphId : ''
  let nodeId = typeof payload.nodeId === 'string' ? payload.nodeId : ''
  let portId = typeof payload.portId === 'string' ? payload.portId : undefined

  if (!nodeId && typeof payload.handle === 'string') nodeId = payload.handle
  const edge = asRecord(payload.edge)
  const endpoint = asRecord(edge?.to) ?? asRecord(edge?.from)
  if (!nodeId && typeof endpoint?.nodeId === 'string') nodeId = endpoint.nodeId
  if (!portId && typeof endpoint?.portId === 'string') portId = endpoint.portId

  nodeId = nodeId.replace(/^\$/, '')
  if (!graphId || !nodeId) return undefined
  return {
    graphId,
    nodeId,
    fieldId: typeof payload.fieldId === 'string' ? payload.fieldId : undefined,
    portId,
  }
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined
}
