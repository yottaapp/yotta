import { ref } from 'vue'
import type { CompileView, DebugBreakpoint, DebugSnapshot, RunView } from '@/app/transport/workflow'
import { runReadinessMessage, type RunStartOutcome } from '@/app/run/runReadiness'

export type EditorRuntimeWorkbenchTab = 'diagnostics' | 'logs' | 'timeline' | 'debug'

export type EditorRunCommand =
  | { kind: 'check-workflow' }
  | { kind: 'save'; inputsCommitted?: boolean }
  | { kind: 'start' }
  | { kind: 'start-debug'; breakpoints: DebugBreakpoint[] }
  | { kind: 'control-debug'; action: 'continue' | 'pause' | 'step' }
  | { kind: 'cancel' }
  | { kind: 'refresh' }
  | { kind: 'load-timeline-page'; page: number }
  | { kind: 'export-timeline' }

export interface EditorRunCommandResult {
  ok: boolean
}

export interface EditorRunSession {
  diagnostics: unknown[]
  saveError?: string
  lastRunOutcome?: RunStartOutcome | null
  debugSnapshot?: DebugSnapshot | null
  check(): Promise<CompileView>
  save(): Promise<unknown>
  run(): Promise<RunView | null>
  startDebug(breakpoints: DebugBreakpoint[]): Promise<RunView | null>
  controlDebug(action: 'continue' | 'pause' | 'step'): Promise<DebugSnapshot | null>
  cancelRun(): Promise<RunView | null>
  refreshRun(): Promise<RunView | null>
  loadTimelinePage(page: number): Promise<RunView | null>
}

export interface EditorRunControllerDependencies {
  commitInputs?: () => Promise<boolean>
  persistLocalConfiguration?: () => Promise<boolean>
  session: EditorRunSession
  translate: (key: string, params?: Record<string, unknown>) => string
  showError: (title: string, error: unknown) => void
  showSuccess: (title: string) => void
  openWorkbench: (tab: EditorRuntimeWorkbenchTab) => void
  focusDebugNode: (graphPath: string[], nodeId: string) => Promise<void>
  activeRun: () => Pick<RunView, 'runId'> | null
  chooseTimelineDestination: (filename: string) => Promise<string>
  exportTimeline: (runId: string, destination: string) => Promise<{ entries: number }>
}

export function createEditorRunController(dependencies: EditorRunControllerDependencies) {
  const saveSucceeded = ref(false)
  const debugControlBusy = ref(false)
  const timelineExporting = ref(false)
  let saveFlashTimer: ReturnType<typeof setTimeout> | undefined

  async function execute(command: EditorRunCommand): Promise<EditorRunCommandResult> {
    if (
      !(command.kind === 'save' && command.inputsCommitted) &&
      ['check-workflow', 'save', 'start', 'start-debug'].includes(command.kind) &&
      dependencies.commitInputs &&
      !(await dependencies.commitInputs())
    )
      return { ok: false }
    if (
      ['save', 'start', 'start-debug'].includes(command.kind) &&
      dependencies.persistLocalConfiguration &&
      !(await dependencies.persistLocalConfiguration())
    )
      return { ok: false }
    switch (command.kind) {
      case 'check-workflow':
        return checkWorkflow()
      case 'save':
        return save()
      case 'start':
        return start()
      case 'start-debug':
        return startDebug(command.breakpoints)
      case 'control-debug':
        return controlDebug(command.action)
      case 'cancel':
        return cancel()
      case 'refresh':
        return refresh()
      case 'load-timeline-page':
        return loadTimelinePage(command.page)
      case 'export-timeline':
        return exportTimeline()
    }
  }

  async function checkWorkflow(): Promise<EditorRunCommandResult> {
    try {
      const result = await dependencies.session.check()
      if (result.diagnostics.length > 0) dependencies.openWorkbench('diagnostics')
      else dependencies.showSuccess(dependencies.translate('workflow.toast.check_succeeded'))
      return { ok: true }
    } catch (error) {
      dependencies.showError(dependencies.translate('workflow.toast.check_failed'), error)
      return { ok: false }
    }
  }

  async function save(): Promise<EditorRunCommandResult> {
    flashSave(false)
    try {
      await dependencies.session.save()
      flashSave(true)
      return { ok: true }
    } catch (error) {
      if (!dependencies.session.saveError)
        dependencies.showError(dependencies.translate('workflow.toast.save_failed'), error)
      return { ok: false }
    }
  }

  async function start(): Promise<EditorRunCommandResult> {
    try {
      const run = await dependencies.session.run()
      if (run) dependencies.openWorkbench('timeline')
      else if (dependencies.session.diagnostics.length > 0)
        dependencies.openWorkbench('diagnostics')
      else if (
        dependencies.session.lastRunOutcome?.state &&
        dependencies.session.lastRunOutcome.state !== 'started'
      )
        dependencies.showError(
          dependencies.translate('workflow.toast.not_started'),
          runReadinessMessage(dependencies.session.lastRunOutcome),
        )
      return { ok: Boolean(run) }
    } catch (error) {
      if (!dependencies.session.saveError)
        dependencies.showError(dependencies.translate('workflow.toast.run_failed'), error)
      return { ok: false }
    }
  }

  async function startDebug(breakpoints: DebugBreakpoint[]): Promise<EditorRunCommandResult> {
    try {
      const run = await dependencies.session.startDebug(breakpoints)
      if (!run) {
        if (dependencies.session.diagnostics.length > 0) dependencies.openWorkbench('diagnostics')
        return { ok: false }
      }
      dependencies.openWorkbench('debug')
      const snapshot = dependencies.session.debugSnapshot
      if (snapshot?.status === 'paused' && snapshot.nodeId) {
        await dependencies.focusDebugNode(
          snapshot.graphPath ?? (snapshot.graphId ? [snapshot.graphId] : []),
          snapshot.nodeId,
        )
      }
      return { ok: true }
    } catch (error) {
      if (!dependencies.session.saveError)
        dependencies.showError(dependencies.translate('workflow.toast.debug_failed'), error)
      return { ok: false }
    }
  }

  async function controlDebug(
    action: 'continue' | 'pause' | 'step',
  ): Promise<EditorRunCommandResult> {
    if (debugControlBusy.value) return { ok: false }
    debugControlBusy.value = true
    try {
      await dependencies.session.controlDebug(action)
      return { ok: true }
    } catch (error) {
      dependencies.showError(dependencies.translate('workflow.toast.debug_failed'), error)
      return { ok: false }
    } finally {
      debugControlBusy.value = false
    }
  }

  async function cancel(): Promise<EditorRunCommandResult> {
    try {
      await dependencies.session.cancelRun()
      return { ok: true }
    } catch (error) {
      dependencies.showError(dependencies.translate('workflow.toast.stop_failed'), error)
      return { ok: false }
    }
  }

  async function refresh(): Promise<EditorRunCommandResult> {
    try {
      const run = await dependencies.session.refreshRun()
      if (run?.failure) dependencies.openWorkbench('timeline')
      return { ok: true }
    } catch (error) {
      dependencies.showError(dependencies.translate('workflow.toast.refresh_failed'), error)
      return { ok: false }
    }
  }

  async function loadTimelinePage(page: number): Promise<EditorRunCommandResult> {
    try {
      await dependencies.session.loadTimelinePage(page)
      return { ok: true }
    } catch (error) {
      dependencies.showError(dependencies.translate('workflow.toast.refresh_failed'), error)
      return { ok: false }
    }
  }

  async function exportTimeline(): Promise<EditorRunCommandResult> {
    const run = dependencies.activeRun()
    if (!run || timelineExporting.value) return { ok: false }
    timelineExporting.value = true
    try {
      const destination = await dependencies.chooseTimelineDestination(
        `yotta-run-${run.runId}.json`,
      )
      if (!destination) return { ok: false }
      const result = await dependencies.exportTimeline(run.runId, destination)
      dependencies.showSuccess(
        dependencies.translate('workflow.timeline.export_succeeded', { count: result.entries }),
      )
      return { ok: true }
    } catch (error) {
      dependencies.showError(dependencies.translate('workflow.timeline.export_failed'), error)
      return { ok: false }
    } finally {
      timelineExporting.value = false
    }
  }

  function flashSave(value: boolean): void {
    clearTimeout(saveFlashTimer)
    saveSucceeded.value = value
    if (value) saveFlashTimer = setTimeout(() => (saveSucceeded.value = false), 1600)
  }

  function dispose(): void {
    clearTimeout(saveFlashTimer)
  }

  return {
    saveSucceeded,
    debugControlBusy,
    timelineExporting,
    execute,
    dispose,
  }
}
