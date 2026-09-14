import { workflowTargetIssue } from '@/app/editor/workflowTargets'
import { i18n } from '@/i18n'
import type { WorkflowStartRunView } from '@/app/transport/workflow'

export interface RunReadinessLike {
  state: string
  code?: string
  slot?: string
  nodeId?: string
  fromNodeId?: string
  fromPortId?: string
  toNodeId?: string
  toPortId?: string
  requirementId?: string
}

export type RunStartOutcome =
  | { state: 'started'; runId: string }
  | {
      state:
        | 'workflow-invalid'
        | 'target-required'
        | 'credential-required'
        | 'environment-unavailable'
        | 'not-started'
        | 'failed'
      code?: string
      slot?: string
      nodeId?: string
      fromNodeId?: string
      fromPortId?: string
      toNodeId?: string
      toPortId?: string
      requirementId?: string
      parameterLabel?: string
    }

export function runStartOutcome(started: WorkflowStartRunView): RunStartOutcome {
  if (started.run) return { state: 'started', runId: started.run.runId }
  const parameterIssue = started.diagnostics.find(
    (item) => item.severity === 'error' && typeof item.params?.parameterLabel === 'string',
  )
  if (parameterIssue)
    return {
      state: 'workflow-invalid',
      code: parameterIssue.code,
      parameterLabel: String(parameterIssue.params?.parameterLabel),
    }
  const readiness = started.readiness
  if (readiness && readiness.state !== 'started' && readiness.state !== 'failed') {
    return readinessOutcome(readiness)
  }
  if (readiness?.state === 'failed') {
    return readinessOutcome(readiness)
  }
  const diagnostic = started.diagnostics.find((item) => item.severity === 'error')
  if (diagnostic) {
    return {
      state: 'workflow-invalid',
      code: diagnostic.code,
      nodeId: diagnostic.nodeId,
      ...edgeLocation(diagnostic.params),
    }
  }
  return { state: 'not-started' }
}

export function readinessOutcome(
  readiness?: RunReadinessLike | null,
): Exclude<RunStartOutcome, { state: 'started' }> {
  if (!readiness) return { state: 'not-started' }
  return {
    state: normalizeReadinessState(readiness.state),
    code: readiness.code,
    slot: readiness.slot,
    nodeId: readiness.nodeId,
    requirementId: readiness.requirementId,
    ...edgeLocation(readiness),
  }
}

export function runReadinessMessage(
  outcome: Exclude<RunStartOutcome, { state: 'started' }>,
): string {
  const t = i18n.global.t
  const te = i18n.global.te
  if (outcome.parameterLabel)
    return (
      workflowTargetIssue(outcome.code ?? '', outcome.parameterLabel, t) ??
      t('workflow.parameters.invalid', { name: outcome.parameterLabel })
    )
  if (
    outcome.requirementId === 'model' &&
    (outcome.code === 'admission.target_unavailable' ||
      outcome.code === 'admission.target_ambiguous')
  ) {
    return t('workflow.run_readiness.ai_model_unavailable', { slot: outcome.slot ?? '' })
  }
  if (
    outcome.requirementId === 'model' &&
    (outcome.code === 'admission.credential_unavailable' ||
      outcome.code === 'admission.credential_ambiguous')
  ) {
    return t('workflow.run_readiness.ai_credential_unavailable', { slot: outcome.slot ?? '' })
  }
  const edge = edgeLocation(outcome)
  if (
    outcome.code === 'UNKNOWN_PORT' &&
    edge.fromNodeId &&
    edge.fromPortId &&
    edge.toNodeId &&
    edge.toPortId
  ) {
    return t('workflow.run_readiness.unknown_port', edge)
  }
  if (outcome.code && te(`error.${outcome.code}`)) {
    return t(`error.${outcome.code}`, {
      slot: outcome.slot ?? '',
      nodeId: outcome.nodeId ?? '',
      ...edge,
    })
  }
  return t(`workflow.run_readiness.${outcome.state}`)
}

function edgeLocation(value: unknown): {
  fromNodeId?: string
  fromPortId?: string
  toNodeId?: string
  toPortId?: string
} {
  if (!value || typeof value !== 'object') return {}
  const record = value as Record<string, unknown>
  const result: {
    fromNodeId?: string
    fromPortId?: string
    toNodeId?: string
    toPortId?: string
  } = {}
  for (const key of ['fromNodeId', 'fromPortId', 'toNodeId', 'toPortId'] as const) {
    const item = record[key]
    if (typeof item === 'string' && item) result[key] = item
  }
  return result
}

function normalizeReadinessState(
  state: string,
): Exclude<RunStartOutcome, { state: 'started' }>['state'] {
  switch (state) {
    case 'workflow-invalid':
    case 'target-required':
    case 'credential-required':
    case 'environment-unavailable':
    case 'failed':
      return state
    default:
      return 'not-started'
  }
}
