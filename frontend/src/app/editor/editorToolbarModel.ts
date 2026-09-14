export type EditorToolbarCommand =
  | 'undo'
  | 'redo'
  | 'find-node'
  | 'toggle-ai'
  | 'toggle-state'
  | 'toggle-inspector'
  | 'check-workflow'
  | 'toggle-diagnostics'
  | 'toggle-timeline'
  | 'toggle-debugger'
  | 'start-debug'
  | 'pause-recording'
  | 'resume-recording'
  | 'stop-recording'
  | 'run'
  | 'stop'
  | 'save'
  | 'settings'
  | 'reload'

export type EditorRecordingPhase =
  | 'idle'
  | 'armed'
  | 'countdown'
  | 'recording'
  | 'paused'
  | 'finalizing'
  | 'pending'

export interface EditorToolbarContext {
  canUndo: boolean
  canRedo: boolean
  dirty: boolean
  aiPanelOpen: boolean
  statePanelOpen: boolean
  inspectorOpen: boolean
  runActive: boolean
  saving: boolean
  saveSucceeded: boolean
  diagnosticCount: number
  diagnosticsOpen: boolean
  hasRunTimeline: boolean
  runTimelineOpen: boolean
  debugModeActive: boolean
  debuggerOpen: boolean
  recordingPhase: EditorRecordingPhase
}

export interface EditorToolbarAction {
  command: EditorToolbarCommand
  labelKey: string
  icon: string
  testId?: string
  color?: 'neutral' | 'primary' | 'success' | 'warning' | 'error'
  variant?: 'ghost' | 'soft' | 'solid'
  disabled?: boolean
  loading?: boolean
  active?: boolean
  labelParams?: Record<string, string | number>
}

export interface EditorToolbarModel {
  editing: EditorToolbarAction[]
  contextual: EditorToolbarAction[]
  primary: EditorToolbarAction[]
  ai: EditorToolbarAction
  tools: EditorToolbarAction[][]
  recordingStatusKey?: string
  toolsNeedAttention: boolean
}

const actionDefinitions: Record<
  EditorToolbarCommand,
  Pick<EditorToolbarAction, 'labelKey' | 'icon' | 'testId'>
> = {
  undo: {
    labelKey: 'workflow.action.undo',
    icon: 'i-tabler-arrow-back-up',
  },
  redo: {
    labelKey: 'workflow.action.redo',
    icon: 'i-tabler-arrow-forward-up',
  },
  'find-node': {
    labelKey: 'workflow.node_search.action',
    icon: 'i-tabler-search',
    testId: 'workflow-find-node',
  },
  'toggle-ai': {
    labelKey: 'workflow.ai.open',
    icon: 'i-tabler-sparkles',
    testId: 'ai-workflow-review-open',
  },
  'toggle-state': {
    labelKey: 'workflow.inspector.state_title',
    icon: 'i-tabler-database',
    testId: 'workflow-state-open',
  },
  'toggle-inspector': {
    labelKey: 'workflow.editor.inspector',
    icon: 'i-tabler-layout-sidebar-right',
    testId: 'workflow-inspector-toggle',
  },
  'check-workflow': {
    labelKey: 'workflow.action.check_workflow',
    icon: 'i-tabler-file-check',
    testId: 'workflow-check',
  },
  'toggle-diagnostics': {
    labelKey: 'workflow.diagnostics.badge',
    icon: 'i-tabler-alert-triangle',
    testId: 'workflow-diagnostics-open',
  },
  'toggle-timeline': {
    labelKey: 'workflow.timeline.open',
    icon: 'i-tabler-timeline-event',
    testId: 'workflow-timeline-open',
  },
  'toggle-debugger': {
    labelKey: 'workflow.debug.title',
    icon: 'i-tabler-bug',
    testId: 'workflow-debug-open',
  },
  'start-debug': {
    labelKey: 'workflow.debug.start',
    icon: 'i-tabler-bug',
    testId: 'workflow-debug-start',
  },
  'pause-recording': {
    labelKey: 'workflow.recording.pause',
    icon: 'i-tabler-player-pause',
  },
  'resume-recording': {
    labelKey: 'workflow.recording.resume',
    icon: 'i-tabler-player-play',
  },
  'stop-recording': {
    labelKey: 'workflow.recording.finish',
    icon: 'i-tabler-square',
    testId: 'workflow-recording-stop',
  },
  run: {
    labelKey: 'workflow.action.run',
    icon: 'i-tabler-player-play',
    testId: 'workflow-run-timeline',
  },
  stop: {
    labelKey: 'workflow.action.stop',
    icon: 'i-tabler-square',
    testId: 'workflow-run-stop',
  },
  save: {
    labelKey: 'workflow.action.save',
    icon: 'i-tabler-device-floppy',
    testId: 'workflow-save',
  },
  settings: {
    labelKey: 'workflow.editor.settings',
    icon: 'i-tabler-settings',
    testId: 'workflow-settings',
  },
  reload: {
    labelKey: 'common.refresh',
    icon: 'i-tabler-refresh',
    testId: 'workflow-reload',
  },
}

function action(
  command: EditorToolbarCommand,
  overrides: Partial<EditorToolbarAction> = {},
): EditorToolbarAction {
  return { command, ...actionDefinitions[command], ...overrides }
}

export function buildEditorToolbarModel(context: EditorToolbarContext): EditorToolbarModel {
  const contextual: EditorToolbarAction[] = []
  let recordingStatusKey: string | undefined

  if (context.recordingPhase === 'armed' || context.recordingPhase === 'countdown') {
    recordingStatusKey =
      context.recordingPhase === 'armed' ? 'recordingHud.waiting' : 'recordingHud.countdown'
    contextual.push(
      action('stop-recording', {
        labelKey: 'common.cancel',
        icon: 'i-tabler-x',
        color: 'error',
        variant: 'ghost',
        testId: 'workflow-recording-cancel-preparation',
      }),
    )
  } else if (
    context.recordingPhase === 'recording' ||
    context.recordingPhase === 'paused' ||
    context.recordingPhase === 'finalizing'
  ) {
    contextual.push(
      action(context.recordingPhase === 'paused' ? 'resume-recording' : 'pause-recording', {
        color: 'warning',
        variant: 'soft',
        disabled: context.recordingPhase === 'finalizing',
      }),
      action('stop-recording', {
        color: 'error',
        variant: 'soft',
        loading: context.recordingPhase === 'finalizing',
      }),
    )
  } else if (context.recordingPhase === 'pending') {
    recordingStatusKey = 'recordingSave.pending'
  }

  if (context.debugModeActive) {
    contextual.push(
      action('toggle-debugger', {
        color: 'warning',
        variant: context.debuggerOpen ? 'soft' : 'ghost',
        active: context.debuggerOpen,
      }),
    )
  }

  const executionTools = [action('check-workflow')]
  if (context.diagnosticCount > 0) {
    executionTools.push(
      action('toggle-diagnostics', {
        labelParams: { n: context.diagnosticCount },
        color: 'warning',
        active: context.diagnosticsOpen,
      }),
    )
  }
  if (context.hasRunTimeline) {
    executionTools.push(
      action('toggle-timeline', {
        active: context.runTimelineOpen,
      }),
    )
  }
  if (!context.runActive) executionTools.push(action('start-debug'))

  return {
    editing: [
      action('undo', { disabled: !context.canUndo }),
      action('redo', { disabled: !context.canRedo }),
      action('find-node'),
    ],
    contextual,
    primary: [
      context.runActive
        ? action('stop', { color: 'error', variant: 'soft' })
        : action('run', { color: 'primary', variant: 'solid' }),
      action('save', {
        labelKey: context.saveSucceeded ? 'workflow.action.saved' : 'workflow.action.save',
        icon: context.saveSucceeded ? 'i-tabler-check' : 'i-tabler-device-floppy',
        color: context.saveSucceeded ? 'success' : 'neutral',
        variant: 'soft',
        loading: context.saving,
        disabled: !context.dirty,
      }),
    ],
    ai: action('toggle-ai', {
      color: 'primary',
      variant: context.aiPanelOpen ? 'soft' : 'ghost',
      active: context.aiPanelOpen,
    }),
    tools: [
      [action('toggle-inspector', { active: context.inspectorOpen })],
      executionTools,
      [action('reload')],
    ],
    recordingStatusKey,
    toolsNeedAttention: context.diagnosticCount > 0,
  }
}
