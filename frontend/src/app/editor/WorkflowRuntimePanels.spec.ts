import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const read = (name: string) => readFileSync(join(process.cwd(), 'src/app/editor', name), 'utf8')
const toolbar = read('WorkflowEditorToolbar.vue')
const toolbarModel = read('editorToolbarModel.ts')
const runController = read('EditorRunController.ts')
const diagnostics = read('WorkflowDiagnosticsPanel.vue')
const timeline = read('RunTimelinePanel.vue')
const debuggerPanel = read('WorkflowDebuggerPanel.vue')
const workbench = read('WorkflowRuntimeWorkbench.vue')
const node = read('WorkflowNode.vue')
const valueEditor = read('WorkflowValueEditor.vue')
const durationEditor = read('DurationValueEditor.vue')
const runtimeWorkbench = read('useWorkflowRuntimeWorkbench.ts')
const canvas = read('WorkflowEditorCanvas.vue')
const editor = readFileSync(join(process.cwd(), 'src/views/WorkflowEditorView.vue'), 'utf8')

describe('workflow runtime inspection UI', () => {
  it('labels the admitted Run honestly and keeps both result panels recoverable', () => {
    expect(toolbar).not.toContain('workflow.action.debug')
    expect(toolbarModel).toContain("labelKey: 'workflow.action.run'")
    expect(toolbarModel).toContain("action('toggle-diagnostics'")
    expect(toolbarModel).toContain("action('toggle-timeline'")
    expect(toolbar).toContain("emit('command', item.command)")
    expect(workbench).toContain("activate('diagnostics')")
  })

  it('uses the true debug transport and keeps breakpoints outside Workflow Source', () => {
    expect(toolbarModel).toContain("action('start-debug')")
    expect(editor).toContain('breakpoints: debugBreakpoints()')
    expect(runController).toContain('dependencies.session.startDebug(breakpoints)')
    expect(runController).toContain('dependencies.session.controlDebug(action)')
    expect(runtimeWorkbench).toContain('breakpointKeys')
    expect(debuggerPanel).toContain("emit('step')")
    expect(debuggerPanel).toContain("emit('continue')")
    expect(debuggerPanel).toContain("emit('pause')")
    expect(debuggerPanel).toContain('workflow.debug.will_execute')
    expect(debuggerPanel).toContain('snapshot.previousNodeId')
  })

  it('explains breakpoints and keeps the inactive control out of the resting node chrome', () => {
    expect(node).toContain('<UTooltip')
    expect(node).toContain(':text="breakpointLabel"')
    expect(node).toContain("'opacity-0 group-hover/node:opacity-100 focus-within:opacity-100'")
    expect(node).toContain('debugMode || breakpoint')
  })

  it('opens normal runs for inspection and unifies diagnostics, logs, timeline, and debug', () => {
    expect(runController).toContain("dependencies.openWorkbench('timeline')")
    expect(runController).toContain("dependencies.openWorkbench('diagnostics')")
    expect(runController).toContain("dependencies.openWorkbench('debug')")
    expect(workbench).toContain("activate('logs')")
    expect(workbench).toContain("activate('diagnostics')")
    expect(workbench).toContain("activate('timeline')")
    expect(workbench).toContain("activate('debug')")
    expect(workbench).toContain('<WorkflowDiagnosticsPanel')
    expect(workbench).toContain('<LogPanel v-else-if=')
    expect(runController).toContain("if (run?.failure) dependencies.openWorkbench('timeline')")
    expect(editor).toContain(
      "if (event.snapshot.status === 'paused') openRuntimeWorkbench('debug')",
    )
  })

  it('keeps timeline export orchestration in the run controller', () => {
    expect(runController).toContain('dependencies.chooseTimelineDestination(')
    expect(runController).toContain('dependencies.exportTimeline(run.runId, destination)')
    expect(editor).toContain('@export-timeline="editorRuns.execute({ kind: \'export-timeline\' })"')
    expect(editor).not.toContain('async function exportRunTimeline')
  })

  it('represents an unset target through the placeholder instead of an invalid empty select item', () => {
    expect(canvas).toContain('workflow.target_default.placeholder')
    expect(canvas).not.toContain('workflow.target_default.clear')
    expect(editor).not.toContain("label: t('workflow.target_default.none'), value: ''")
  })

  it('keeps inline duration controls at the compact node typography scale', () => {
    expect(valueEditor).toContain(':compact="compact"')
    expect(durationEditor).toContain(`:size="compact ? 'xs' : 'sm'"`)
    expect(durationEditor).toContain(`'grid-cols-[minmax(0,1fr)_80px] gap-1.5'`)
  })

  it('uses the full safe node surface for dragging while controls opt out', () => {
    expect(node).toContain('workflow-node-drag-surface')
    expect(node).not.toContain('workflow-node-drag-handle')
    expect(node).toContain('class="nodrag nopan transition-opacity"')
    expect(node).toContain('class="nodrag nopan space-y-2')
  })

  it('groups compiler diagnostics and shows only compiler-declared fixes', () => {
    expect(diagnostics).toContain('groupDiagnostics')
    expect(diagnostics).toContain('diagnostic.fix')
    expect(diagnostics).toContain('class="w-full min-w-0"')
    expect(diagnostics).toContain('block break-words')
    expect(diagnostics).not.toContain('lg:grid-cols-3')
    expect(diagnostics).not.toContain('block truncate')
    expect(diagnostics).not.toContain('suggestedFix')
    expect(editor).toContain('@focus="focusDiagnostic"')
    expect(editor).not.toContain(
      '<WorkflowDiagnosticsPanel\n        v-if="diagnosticsOpen && session.diagnostics.length"',
    )
  })

  it('locates timeline facts and projects journal-derived status onto nodes', () => {
    expect(timeline).toContain("emit('focus-node', entry.graphPath, entry.nodeId)")
    expect(editor).toContain('session.openGraphPath(graphPath)')
    expect(editor).toContain('await setCenter(')
    expect(node).toContain('data-testid="node-run-status"')
    expect(timeline).toContain("emit('page', run.timelinePage + 1)")
    expect(timeline).toContain('activeRunAttempt(props.run)')
    expect(timeline).toContain('activeAttemptElapsed')
    expect(timeline).toContain('activeAttemptTimeout')
    expect(timeline).toContain("emit('export')")
    expect(workbench).toContain("emit('export-timeline')")
    expect(workbench).toContain('expanded = !expanded')
    expect(editor).toContain('workflowTransport.exportRunTimeline')
    expect(workbench).toContain(':node-labels="nodeLabels"')
    expect(workbench).toContain(':unhandled-routes="unhandledRoutes"')
    expect(timeline).toContain('workflow.timeline.unhandled_route')
    expect(timeline).toContain('templateMatchEvidence(entry)')
    expect(timeline).toContain('workflow.timeline.locate_node')
    expect(timeline).toContain("emit('diagnose')")
    expect(workbench).toContain("emit('diagnose-run')")
    expect(editor).toContain('@diagnose-run="openAIDiagnosis"')
    expect(runController).toContain("dependencies.openWorkbench('timeline')")
  })

  it('renders structured run and RPC failures as localized messages', () => {
    expect(timeline).toContain('failureMessage')
    expect(editor).toContain('errorMessage(error)')
  })
})
