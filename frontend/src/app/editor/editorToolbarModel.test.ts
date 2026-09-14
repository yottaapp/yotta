import { describe, expect, it } from 'vitest'
import {
  buildEditorToolbarModel,
  type EditorToolbarCommand,
  type EditorToolbarContext,
} from './editorToolbarModel'

function context(overrides: Partial<EditorToolbarContext> = {}): EditorToolbarContext {
  return {
    canUndo: false,
    canRedo: false,
    dirty: false,
    aiPanelOpen: false,
    statePanelOpen: false,
    inspectorOpen: true,
    runActive: false,
    saving: false,
    saveSucceeded: false,
    diagnosticCount: 0,
    diagnosticsOpen: false,
    hasRunTimeline: false,
    runTimelineOpen: false,
    debugModeActive: false,
    debuggerOpen: false,
    recordingPhase: 'idle',
    ...overrides,
  }
}

function commands(actions: Array<{ command: EditorToolbarCommand }>): EditorToolbarCommand[] {
  return actions.map((item) => item.command)
}

describe('editor toolbar command hierarchy', () => {
  it('keeps only editing, run, and save commands on the resting toolbar', () => {
    const model = buildEditorToolbarModel(context())

    expect(commands(model.editing)).toEqual(['undo', 'redo', 'find-node'])
    expect(commands(model.contextual)).toEqual([])
    expect(commands(model.primary)).toEqual(['run', 'save'])
    expect(model.ai?.command).toBe('toggle-ai')
    expect(model.tools.flatMap((group) => commands(group))).toEqual([
      'toggle-inspector',
      'check-workflow',
      'start-debug',
      'reload',
    ])
  })

  it('promotes debug only after debug mode is active', () => {
    const inactive = buildEditorToolbarModel(context())
    const active = buildEditorToolbarModel(
      context({ debugModeActive: true, debuggerOpen: true, runActive: true }),
    )

    expect(commands(inactive.contextual)).not.toContain('toggle-debugger')
    expect(commands(inactive.tools[1] ?? [])).toContain('start-debug')
    expect(commands(active.contextual)).toContain('toggle-debugger')
    expect(commands(active.tools[1] ?? [])).not.toContain('start-debug')
    expect(commands(active.primary)).toEqual(['stop', 'save'])
  })

  it('keeps diagnostics and timeline recoverable inside tools without making them permanent chrome', () => {
    const model = buildEditorToolbarModel(
      context({
        diagnosticCount: 3,
        diagnosticsOpen: true,
        hasRunTimeline: true,
        runTimelineOpen: false,
      }),
    )
    const execution = model.tools[1] ?? []

    expect(commands(execution)).toEqual([
      'check-workflow',
      'toggle-diagnostics',
      'toggle-timeline',
      'start-debug',
    ])
    expect(execution.find((item) => item.command === 'toggle-diagnostics')).toMatchObject({
      active: true,
      labelParams: { n: 3 },
    })
    expect(model.toolsNeedAttention).toBe(true)
  })

  it('replaces ordinary commands with recording controls only while recording is active', () => {
    const preparing = buildEditorToolbarModel(context({ recordingPhase: 'armed' }))
    const recording = buildEditorToolbarModel(context({ recordingPhase: 'recording' }))
    const paused = buildEditorToolbarModel(context({ recordingPhase: 'paused' }))

    expect(preparing.recordingStatusKey).toBe('recordingHud.waiting')
    expect(commands(preparing.contextual)).toEqual(['stop-recording'])
    expect(commands(recording.contextual)).toEqual(['pause-recording', 'stop-recording'])
    expect(commands(paused.contextual)).toEqual(['resume-recording', 'stop-recording'])
  })
})
