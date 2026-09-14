import type { InjectionKey, Ref } from 'vue'
import type { WorkflowTarget } from '../../../../contracts/workflow/current/workflow-source'
export const targetValueKey = (id: string) => `@target/${id}`
export function isWorkflowTargetKind(kinds: readonly string[]): boolean {
  return kinds.some((kind) =>
    ['desktop-window', 'android-device', 'browser-cdp', 'configured-application'].includes(kind),
  )
}
export function targetAcceptsKinds(target: WorkflowTarget, kinds: readonly string[]): boolean {
  return (
    kinds.includes(target.kind) ||
    (target.kind === 'automation' &&
      kinds.some((kind) => ['desktop-window', 'android-device', 'browser-cdp'].includes(kind)))
  )
}
export interface WorkflowTargetContext {
  targets: Readonly<Ref<WorkflowTarget[]>>
  resolve: (id: string) => string
  openSettings: () => void
}
export const WORKFLOW_TARGETS: InjectionKey<WorkflowTargetContext> = Symbol('workflow-targets')

export function workflowTargetIssue(
  code: string,
  name: string,
  t: (key: string, params: Record<string, unknown>) => string,
): string | undefined {
  const keys: Record<string, string> = {
    WORKFLOW_TARGET_BINDING_MISSING: 'missing_binding',
    WORKFLOW_TARGET_BINDING_INVALID: 'invalid_binding',
    WORKFLOW_TARGET_KIND_MISMATCH: 'wrong_kind',
    WORKFLOW_TARGET_UNDECLARED: 'undeclared',
  }
  const key = keys[code]
  return key ? t(`workflow.settings_panel.${key}`, { name }) : undefined
}
