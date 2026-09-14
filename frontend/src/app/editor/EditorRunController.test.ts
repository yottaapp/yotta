import { describe, expect, it, vi } from 'vitest'
import type { CompileView, DebugSnapshot, RunView } from '@/app/transport/workflow'
import {
  createEditorRunController,
  type EditorRunControllerDependencies,
  type EditorRunSession,
} from './EditorRunController'

function harness(overrides: Partial<EditorRunSession> = {}) {
  const session: EditorRunSession = {
    diagnostics: [],
    check: vi.fn(async () => ({ diagnostics: [] }) as CompileView),
    save: vi.fn(async () => undefined),
    run: vi.fn(async () => ({ runId: 'run-1' }) as RunView),
    startDebug: vi.fn(async () => ({ runId: 'debug-1' }) as RunView),
    controlDebug: vi.fn(async () => null),
    cancelRun: vi.fn(async () => null),
    refreshRun: vi.fn(async () => null),
    loadTimelinePage: vi.fn(async () => null),
    ...overrides,
  }
  const openWorkbench = vi.fn()
  const showError = vi.fn()
  const showSuccess = vi.fn()
  const focusDebugNode = vi.fn(async () => undefined)
  const dependencies: EditorRunControllerDependencies = {
    session,
    translate: (key) => `translated:${key}`,
    showError,
    showSuccess,
    openWorkbench,
    focusDebugNode,
    activeRun: () => ({ runId: 'run-1' }),
    chooseTimelineDestination: vi.fn(async () => 'C:/exports/run.json'),
    exportTimeline: vi.fn(async () => ({ entries: 3 })),
  }
  return {
    session,
    dependencies,
    openWorkbench,
    showError,
    showSuccess,
    focusDebugNode,
    controller: createEditorRunController(dependencies),
  }
}

describe('editor run controller', () => {
  it('commits focused input before explicit save but does not steal focus on autosave', async () => {
    const run = harness()
    run.dependencies.commitInputs = vi.fn(async () => true)
    await run.controller.execute({ kind: 'save' })
    expect(run.dependencies.commitInputs).toHaveBeenCalledOnce()
    await run.controller.execute({ kind: 'save', inputsCommitted: true })
    expect(run.dependencies.commitInputs).toHaveBeenCalledOnce()
    run.dependencies.commitInputs = vi.fn(async () => false)
    await expect(run.controller.execute({ kind: 'start' })).resolves.toEqual({ ok: false })
    expect(run.session.run).not.toHaveBeenCalled()
  })

  it('owns compile, start, and result-panel routing behind one command interface', async () => {
    const run = harness()

    await expect(run.controller.execute({ kind: 'check-workflow' })).resolves.toEqual({ ok: true })
    expect(run.showSuccess).toHaveBeenCalledWith('translated:workflow.toast.check_succeeded')
    await expect(run.controller.execute({ kind: 'start' })).resolves.toEqual({ ok: true })
    expect(run.openWorkbench).toHaveBeenLastCalledWith('timeline')
  })

  it('routes check errors without pretending a Run started', async () => {
    const run = harness({
      diagnostics: [{}],
      run: vi.fn(async () => null),
    })

    await expect(run.controller.execute({ kind: 'start' })).resolves.toEqual({ ok: false })
    expect(run.openWorkbench).toHaveBeenCalledWith('diagnostics')
    expect(run.showError).not.toHaveBeenCalled()
  })

  it('opens workflow issues when checking finds a non-blocking warning', async () => {
    const run = harness({
      check: vi.fn(
        async () =>
          ({
            diagnostics: [{ severity: 'warning', code: 'MISSING_INPUT_BINDING' }],
          }) as CompileView,
      ),
    })

    await expect(run.controller.execute({ kind: 'check-workflow' })).resolves.toEqual({ ok: true })
    expect(run.openWorkbench).toHaveBeenCalledWith('diagnostics')
    expect(run.showSuccess).not.toHaveBeenCalled()
  })

  it('opens debug context and focuses the paused node from the authoritative snapshot', async () => {
    const snapshot = {
      status: 'paused',
      graphPath: ['main', 'child'],
      nodeId: 'node-2',
    } as DebugSnapshot
    const run = harness({ debugSnapshot: snapshot })

    await expect(
      run.controller.execute({
        kind: 'start-debug',
        breakpoints: [{ graphId: 'child', nodeId: 'node-2' }],
      }),
    ).resolves.toEqual({ ok: true })
    expect(run.openWorkbench).toHaveBeenCalledWith('debug')
    expect(run.focusDebugNode).toHaveBeenCalledWith(['main', 'child'], 'node-2')
  })

  it('normalizes command failures and keeps save success observable', async () => {
    const failure = new Error('disk unavailable')
    const run = harness({
      save: vi.fn(async () => {
        throw failure
      }),
    })

    await expect(run.controller.execute({ kind: 'save' })).resolves.toEqual({ ok: false })
    expect(run.controller.saveSucceeded.value).toBe(false)
    expect(run.showError).toHaveBeenCalledWith('translated:workflow.toast.save_failed', failure)
  })

  it('does not duplicate a persistent save error with a toast', async () => {
    const failure = new Error('INVALID_FIELD')
    const run = harness({
      saveError: '请检查节点参数或连线',
      save: vi.fn(async () => {
        throw failure
      }),
      run: vi.fn(async () => {
        throw failure
      }),
    })

    await expect(run.controller.execute({ kind: 'save' })).resolves.toEqual({ ok: false })
    await expect(run.controller.execute({ kind: 'start' })).resolves.toEqual({ ok: false })
    expect(run.showError).not.toHaveBeenCalled()
  })

  it('owns timeline destination selection, export, and feedback', async () => {
    const run = harness()

    await expect(run.controller.execute({ kind: 'export-timeline' })).resolves.toEqual({ ok: true })
    expect(run.dependencies.chooseTimelineDestination).toHaveBeenCalledWith('yotta-run-run-1.json')
    expect(run.dependencies.exportTimeline).toHaveBeenCalledWith('run-1', 'C:/exports/run.json')
    expect(run.showSuccess).toHaveBeenCalledWith('translated:workflow.timeline.export_succeeded')
    expect(run.controller.timelineExporting.value).toBe(false)
  })
})

describe('local workflow configuration lifecycle', () => {
  it.each(['save', 'start', 'start-debug'] as const)(
    'persists local bindings before %s and blocks on failure',
    async (kind) => {
      const run = harness()
      const events: string[] = []
      run.dependencies.persistLocalConfiguration = async () => {
        events.push('bindings')
        return true
      }
      run.session.save = vi.fn(async () => {
        events.push('save')
      })
      run.session.run = vi.fn(async () => {
        events.push('start')
        return { runId: 'run' } as RunView
      })
      run.session.startDebug = vi.fn(async () => {
        events.push('start-debug')
        return { runId: 'run' } as RunView
      })
      const command = kind === 'start-debug' ? { kind, breakpoints: [] } : { kind }
      expect(await run.controller.execute(command)).toEqual({ ok: true })
      expect(events).toEqual(['bindings', kind])
      events.length = 0
      run.dependencies.persistLocalConfiguration = async () => false
      expect(await run.controller.execute(command)).toEqual({ ok: false })
      expect(events).toEqual([])
      run.controller.dispose()
    },
  )
})
