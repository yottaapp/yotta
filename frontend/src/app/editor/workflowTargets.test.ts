import { describe, expect, it } from 'vitest'
import type {
  YottaWorkflowSource,
  WorkflowTarget,
} from '../../../../contracts/workflow/current/workflow-source'
import { applyCommand } from './editorCommandApplication'
import { toWorkflowPatch } from './editorCommandPersistence'
import { isWorkflowTargetKind, targetAcceptsKinds, targetValueKey } from './workflowTargets'

const targets: WorkflowTarget[] = [
  { id: 'game', name: 'Game', kind: 'automation', default: true },
  { id: 'launcher', name: 'Launcher', kind: 'configured-application' },
]
function source(): YottaWorkflowSource {
  return {
    version: '5',
    workflow: { id: 'example', name: 'Example' },
    revision: 1,
    entryGraph: 'main',
    targets: structuredClone(targets),
    variables: [],
    graphs: [{ id: 'main', name: 'Main', nodes: [], edges: [] }],
  } as unknown as YottaWorkflowSource
}
describe('workflow target authoring', () => {
  it('keeps local binding keys outside portable declarations and node choices', () => {
    expect(targetValueKey('game')).toBe('@target/game')
    expect(targetAcceptsKinds(targets[0]!, ['desktop-window'])).toBe(true)
    expect(targetAcceptsKinds(targets[0]!, ['configured-application'])).toBe(false)
    expect(targetAcceptsKinds(targets[1]!, ['configured-application'])).toBe(true)
    expect(isWorkflowTargetKind(['http-target'])).toBe(false)
    expect(isWorkflowTargetKind(['ai-model'])).toBe(false)
    expect(isWorkflowTargetKind(['panel-instance'])).toBe(false)
  })
  it('changes default declaration and inherited slot in one command', () => {
    const draft = source()
    draft.targetDefaults = [{ target: 'application', slot: 'game' }]
    const command = {
      kind: 'set-workflow-targets' as const,
      targets: targets.map((target) => ({ ...target, default: target.id === 'launcher' })),
    }
    applyCommand(draft, draft.graphs[0]!, command, new Map(), new Map())
    expect(draft.targetDefaults).toEqual([{ target: 'target', slot: 'launcher' }])
    expect(draft.targets?.filter((target) => target.default).map((target) => target.id)).toEqual([
      'launcher',
    ])
    expect(toWorkflowPatch([{ graphId: 'main', command }])).toEqual([
      { kind: command.kind, setWorkflowTargets: { targets: command.targets } },
    ])
  })
  it('rejects removing a referenced role without partially mutating the source', () => {
    const draft = source()
    draft.graphs[0]!.nodes.push({ id: 'window', config: { slot: 'launcher' } } as never)
    const before = JSON.stringify(draft)
    expect(() =>
      applyCommand(
        draft,
        draft.graphs[0]!,
        { kind: 'set-workflow-targets', targets: [targets[0]!] },
        new Map(),
        new Map(),
      ),
    ).toThrow('referenced')
    expect(JSON.stringify(draft)).toBe(before)
  })
  it('rejects missing or duplicate defaults', () => {
    const draft = source()
    for (const next of [
      [],
      targets.map((target) => ({ ...target, default: false })),
      targets.map((target) => ({ ...target, default: true })),
    ])
      expect(() =>
        applyCommand(
          draft,
          draft.graphs[0]!,
          { kind: 'set-workflow-targets', targets: next },
          new Map(),
          new Map(),
        ),
      ).toThrow('one default')
  })
})
