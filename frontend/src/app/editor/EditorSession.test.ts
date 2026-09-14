import { connectPositionSource, POSITION_STRING_TYPE } from './positionSourceAuthoring'
import { describe, expect, it, vi } from 'vitest'
import { computed, ref } from 'vue'
import { useWorkflowConnectionAuthoring } from './useWorkflowConnectionAuthoring'
import { graphHandle } from './graphHandles'
import authoringDocument from '../../../../contracts/node/current/builtin-authoring'
import type {
  TypeExpression,
  YottaNodeAuthoringProjection,
} from '../../../../contracts/node/current/authoring-projection'
import type {
  Graph,
  NodePackageDependency,
  YottaWorkflowSource,
} from '../../../../contracts/workflow/current/workflow-source'
import type {
  CompileView,
  RunView,
  SourceView,
  StartRunView,
  WorkflowTransport,
  DebugSnapshot,
} from '@/app/transport/workflow'
import { RPCError, errorMessage } from '@/lib/invoke'
import { assignable, EditorSession } from './EditorSession'
import { createEditorSession } from './createEditorSession'

const authoring = authoringDocument as unknown as YottaNodeAuthoringProjection
const node = (nodeTypeId: string) => {
  const projection = authoring.body.nodes.find(
    (candidate) => candidate.nodeRef.nodeTypeId === nodeTypeId,
  )
  if (!projection) throw new Error(`missing builtin node projection: ${nodeTypeId}`)
  return projection
}
const concat = node('https://schemas.yotta.dev/nodes/text/concat')
const stateRead = node('https://schemas.yotta.dev/nodes/state/read')
const greater = node('https://schemas.yotta.dev/nodes/comparison/greater-than')
const toString = node('https://schemas.yotta.dev/nodes/conversion/to-string')
const select = node('https://schemas.yotta.dev/nodes/logic/select')
const delay = node('https://schemas.yotta.dev/nodes/control/delay')
const retry = node('https://schemas.yotta.dev/nodes/control/retry')
const log = node('https://schemas.yotta.dev/nodes/observability/log')
const blobToStream = node('https://schemas.yotta.dev/nodes/conversion/blob-to-stream')
const runStarted = node('https://schemas.yotta.dev/nodes/event/run-started')
const endBranch = node('https://schemas.yotta.dev/nodes/control/end-branch')
const clickPointer = node('https://schemas.yotta.dev/nodes/automation/click-pointer')
const pressKeys = node('https://schemas.yotta.dev/nodes/automation/press-keys')
const typedSwitch = node('https://schemas.yotta.dev/nodes/control/switch')
const playInputClip = node('https://schemas.yotta.dev/nodes/automation/play-input-clip')
const aiExtract = node('https://schemas.yotta.dev/nodes/ai/extract')

describe('EditorSession', () => {
  it('discards a failed draft locally even when reloading the workflow is unavailable', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    session.apply({ kind: 'rename-workflow', name: 'unsaved' })
    transport.applyPatch = vi.fn(async () => {
      throw new Error('save unavailable')
    })
    await expect(session.save()).rejects.toThrow()
    transport.getSource = vi.fn(async () => {
      throw new Error('reload unavailable')
    })
    session.discardDraft()
    expect(session.source?.workflow.name).toBe(source.workflow.name)
    expect(session.dirty).toBe(false)
    expect(session.canUndo).toBe(false)
    expect(session.saveError).toBe('')
    expect(transport.getSource).not.toHaveBeenCalled()
  })

  it('connects position sampling as one undoable edit and stops sampling on movement outcomes', async () => {
    const source = emptySource()
    const session = createEditorSession(mockTransport(sourceView(source), runView('QUEUED')))
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: 'https://schemas.yotta.dev/nodes/navigation/follow-saved-path',
      nodeId: 'follower',
      position: { x: 50, y: 100 },
    })
    session.apply({ kind: 'set-config', nodeId: 'follower', fieldId: 'slot', value: 'desktop' })
    session.apply({
      kind: 'bind-blob',
      nodeId: 'follower',
      portId: 'asset',
      blob: {
        digest: 'sha256:' + 'a'.repeat(64),
        size: 708,
        mediaType: 'application/vnd.yotta.path+json',
      },
    })
    const before = structuredClone(session.source)
    const name = connectPositionSource(session, 'follower', 'position-network')
    expect(connectPositionSource(session, 'follower', 'position-network')).toBe(name)
    expect(session.source?.variables).toHaveLength(2)
    expect(
      session.source?.variables.find((variable) => variable.name === name)?.type,
    ).toMatchObject({ kind: 'ref', ref: { typeId: POSITION_STRING_TYPE } })
    const nodes = session.currentGraph!.nodes
    const getter = nodes.find((node) => node.nodeRef.nodeTypeId.endsWith('/network/http-get'))!
    const timer = nodes.find((node) => node.nodeRef.nodeTypeId.endsWith('/control/periodic'))!
    const writer = nodes.find((node) => node.nodeRef.nodeTypeId.endsWith('/state/write'))!
    expect(getter.config.slot).toBe('position-network')
    expect(getter.bindings.path).toEqual({ kind: 'value', value: '/v1/position-source/sample' })
    expect(timer.bindings['interval-milliseconds']).toEqual({ kind: 'value', value: 100 })
    expect(writer.config.variable).toBe(name)
    expect(nodes.find((node) => node.id === 'follower')?.config['position-variable']).toBe(name)
    const stopWriter = nodes.find(
      (node) =>
        node.config.variable === `${name}.active` &&
        node.nodeRef.nodeTypeId.endsWith('/state/write'),
    )!
    expect(stopWriter.bindings.value).toEqual({ kind: 'value', value: false })
    expect(
      session
        .currentGraph!.edges.filter(
          (edge) => edge.from.nodeId === 'follower' && edge.to.nodeId === stopWriter.id,
        )
        .map((edge) => edge.from.portId)
        .sort(),
    ).toEqual([
      'arrived',
      'height-mismatch',
      'reference-mismatch',
      'stuck',
      'timeout',
      'unavailable',
    ])
    expect(session.currentGraph?.edges).toContainEqual({
      channel: 'exec',
      from: { nodeId: 'follower', portId: 'arrived' },
      to: { nodeId: stopWriter.id, portId: 'in' },
    })
    expect(session.currentGraph?.edges).toContainEqual({
      channel: 'data',
      from: { nodeId: getter.id, portId: 'body' },
      to: { nodeId: writer.id, portId: 'value' },
    })
    session.undo()
    expect(session.source?.variables).toEqual(before?.variables)
    expect(session.currentGraph?.nodes).toEqual(before?.graphs[0]?.nodes)
    session.redo()
    expect(session.source?.variables).toHaveLength(2)
  })

  it('identifies packaged nodes from the installed package catalog, not their category', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    transport.getNodePackageDependencies = vi.fn(async (): Promise<NodePackageDependency[]> => [
      {
        packageId: 'https://example.test/packages/tools/v1',
        packageVersion: '1.0.0',
        publisherNamespace: 'https://example.test',
        manifestDigest: `sha256:${'a'.repeat(64)}`,
        nodeRefs: [
          {
            nodeTypeId: 'https://example.test/nodes/measure',
            version: '1.0.0',
            semanticDigest: `sha256:${'b'.repeat(64)}`,
          },
        ],
      },
    ])
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    expect(session.isPackagedNode('https://example.test/nodes/measure')).toBe(true)
    expect(session.isPackagedNode(concat.nodeRef.nodeTypeId)).toBe(false)
    expect(session.isPackagedNode('https://example.test/nodes/not-installed')).toBe(false)
  })

  it('persists edits made while an earlier save is still awaiting its response', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    let release!: () => void
    let started!: () => void
    const ready = new Promise<void>((resolve) => {
      started = resolve
    })
    const hold = new Promise<void>((resolve) => {
      release = resolve
    })
    let writes = 0
    transport.applyPatch = vi.fn(async () => {
      const snapshot = JSON.parse(session.serialize()) as YottaWorkflowSource
      writes++
      if (writes === 1) {
        started()
        await hold
      }
      return { source: sourceView(snapshot), generatedNodes: [] }
    })
    session.apply({ kind: 'rename-workflow', name: 'First edit' })
    const saving = session.save()
    await ready
    session.apply({ kind: 'rename-workflow', name: 'Latest edit' })
    release()
    await saving
    expect(session.source?.workflow.name).toBe('Latest edit')
    expect(writes).toBe(2)
    expect(session.dirty).toBe(false)
  })

  it('retracts the temporary turn-scale contract on load', async () => {
    const source = emptySource()
    source.graphs[0]!.nodes.push({
      id: 'playback_old',
      nodeRef: {
        ...playInputClip.nodeRef,
        semanticDigest: 'sha256:ff7ea9d0b2ca91cb2062cff30dd5ca8575555ec5363b4c76e746925ee6ae027b',
      },
      position: { x: 0, y: 0 },
      config: {},
      bindings: {
        clip: {
          kind: 'blob',
          blob: {
            mediaType: 'application/vnd.yotta.input-clip+cbor',
            digest: `sha256:${'a'.repeat(64)}`,
            size: 1024,
          },
        },
        'turn-scale': { kind: 'value', value: 1 },
      },
    })
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport)

    await session.load(source.workflow.id)

    const upgraded = session.currentGraph!.nodes[0]!
    expect(upgraded.nodeRef).toEqual(playInputClip.nodeRef)
    expect(upgraded.bindings['turn-scale']).toBeUndefined()
    expect(session.nodeInstanceProjection(upgraded)).toBeDefined()
    expect(session.dirty).toBe(true)
    await session.save()
    expect(transport.applyPatch).toHaveBeenCalledWith(source.workflow.id, 0, [
      {
        kind: 'upgrade-node-contract',
        upgradeNodeContract: { graphId: 'main', nodeId: 'playback_old' },
      },
    ])
  })

  it('auto-upgrades a shape-compatible same-version node contract', async () => {
    const source = emptySource()
    const staleDigest = `sha256:${'b'.repeat(64)}`
    source.graphs[0]!.nodes.push({
      id: 'concat_old',
      nodeRef: { ...concat.nodeRef, semanticDigest: staleDigest },
      position: { x: 0, y: 0 },
      config: {},
      bindings: {
        a: { kind: 'value', value: 'hello' },
        b: { kind: 'value', value: ' world' },
      },
    })
    const session = new EditorSession(mockTransport(sourceView(source), runView('QUEUED')))

    await session.load(source.workflow.id)

    expect(session.currentGraph!.nodes[0]!.nodeRef).toEqual(concat.nodeRef)
    expect(session.dirty).toBe(true)
  })

  it('does not persist stale commands for a temporary node that was removed before save', async () => {
    const source = emptySource()
    source.graphs[0]!.nodes.push({
      id: 'start',
      nodeRef: runStarted.nodeRef,
      position: { x: 0, y: 0 },
      config: {},
      bindings: {},
    })
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport, () => 'temporary_delay')
    await session.load(source.workflow.id)

    session.apply({
      kind: 'add-node',
      nodeTypeId: delay.nodeRef.nodeTypeId,
      position: { x: 200, y: 0 },
    })
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'exec',
        from: { nodeId: 'start', portId: 'started' },
        to: { nodeId: 'temporary_delay', portId: 'in' },
      },
    })
    session.removeNodes(['temporary_delay'])

    await session.save()

    expect(transport.applyPatch).not.toHaveBeenCalled()
    expect(session.dirty).toBe(false)
    expect(session.currentGraph?.nodes.map((candidate) => candidate.id)).toEqual(['start'])
  })

  it('drops earlier edge commands when an existing node is removed before save', async () => {
    const source = emptySource()
    source.graphs[0]!.nodes.push(
      {
        id: 'start',
        nodeRef: runStarted.nodeRef,
        position: { x: 0, y: 0 },
        config: {},
        bindings: {},
      },
      {
        id: 'existing_delay',
        nodeRef: delay.nodeRef,
        position: { x: 200, y: 0 },
        config: {},
        bindings: { 'duration-milliseconds': { kind: 'value', value: 50 } },
      },
    )
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)

    session.apply({
      kind: 'connect',
      edge: {
        channel: 'exec',
        from: { nodeId: 'start', portId: 'started' },
        to: { nodeId: 'existing_delay', portId: 'in' },
      },
    })
    session.removeNodes(['existing_delay'])
    await session.save()

    expect(transport.applyPatch).toHaveBeenCalledWith(source.workflow.id, 0, [
      {
        kind: 'remove-node',
        removeNode: { graphId: 'main', nodeId: 'existing_delay' },
      },
    ])
  })

  it('refreshes stale node contracts before saving and running an open editor session', async () => {
    const source = emptySource()
    const staleDigest = `sha256:${'c'.repeat(64)}`
    source.graphs[0]!.nodes.push({
      id: 'extract_old',
      nodeRef: { ...aiExtract.nodeRef, semanticDigest: staleDigest },
      position: { x: 0, y: 0 },
      config: {
        fields: [{ name: 'field1', type: 'string', description: '', nullable: false }],
      },
      bindings: {},
    })
    const staleAuthoring = structuredClone(authoring)
    const staleExtract = staleAuthoring.body.nodes.find(
      (candidate) => candidate.nodeRef.nodeTypeId === aiExtract.nodeRef.nodeTypeId,
    )!
    staleExtract.nodeRef.semanticDigest = staleDigest

    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    vi.mocked(transport.getAuthoringProjection)
      .mockResolvedValueOnce(JSON.stringify(staleAuthoring))
      .mockResolvedValue(JSON.stringify(authoring))
    transport.applyPatch = vi.fn(async (_workflowId, baseRevision, commands) => {
      expect(commands.map((command: { kind: string }) => command.kind)).toEqual([
        'upgrade-node-contract',
        'rename-workflow',
      ])
      const upgraded = structuredClone(source)
      upgraded.workflow.name = 'renamed'
      upgraded.revision = baseRevision + 1
      upgraded.graphs[0]!.nodes[0]!.nodeRef = structuredClone(aiExtract.nodeRef)
      return { source: sourceView(upgraded), generatedNodes: [] }
    })
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    expect(session.dirty).toBe(false)
    session.apply({ kind: 'rename-workflow', name: 'renamed' })

    await session.run()

    const checked = JSON.parse(
      vi.mocked(transport.checkDraft).mock.calls[0]![0],
    ) as YottaWorkflowSource
    expect(checked.graphs[0]!.nodes[0]!.nodeRef).toEqual(aiExtract.nodeRef)
    expect(transport.startRun).toHaveBeenCalledOnce()
  })

  it('clears only a terminal run trace without mutating the workflow', () => {
    const session = new EditorSession(mockTransport(sourceView(emptySource()), runView('RUNNING')))
    session.activeRun = runView('RUNNING')

    expect(session.clearRunTrace()).toBe(false)
    expect(session.activeRun?.status).toBe('RUNNING')

    session.activeRun = runView('SUCCEEDED')
    expect(session.clearRunTrace()).toBe(true)
    expect(session.activeRun).toBeNull()
    expect(session.dirty).toBe(false)
  })

  it('persists workflow target defaults without copying them into node config', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)

    session.setTargetDefault('target', 'window-target')
    expect(session.source?.targetDefaults).toEqual([{ target: 'target', slot: 'window-target' }])
    await session.save()
    expect(transport.applyPatch).toHaveBeenCalledWith(source.workflow.id, 0, [
      {
        kind: 'set-target-default',
        setTargetDefault: { target: 'target', slot: 'window-target' },
      },
    ])
  })

  it('adds a catalog node when the session is consumed through Vue reactivity', async () => {
    const source = emptySource()
    const session = createEditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => 'node_concat',
    )

    await session.load(source.workflow.id)
    const before = session.currentGraph?.nodes.length ?? 0
    const projectedNodeCount = computed(() => session.currentGraph?.nodes.length ?? 0)

    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      position: { x: 100, y: 100 },
    })

    expect(session.currentGraph?.nodes).toHaveLength(before + 1)
    expect(projectedNodeCount.value).toBe(before + 1)
  })

  it('resolves switch case ports from instance config instead of a fixed catalog shape', async () => {
    const source = emptySource()
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => 'node_switch',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: typedSwitch.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })

    const switchNode = session.currentGraph!.nodes[0]!
    expect(switchNode.config.caseCount).toBe(3)
    expect(
      session
        .nodeInstanceProjection(switchNode)
        ?.signals.filter((signal) => signal.direction === 'output')
        .map((signal) => signal.id),
    ).toEqual(['default', 'failed', 'case-1', 'case-2', 'case-3'])

    session.apply({ kind: 'set-config', nodeId: switchNode.id, fieldId: 'caseCount', value: 2 })
    const updatedSwitch = session.currentGraph!.nodes[0]!
    expect(
      session.nodeInstanceProjection(updatedSwitch)?.dataInputs.map((port) => port.id),
    ).toEqual(['value', 'case-1', 'case-2'])
    expect(
      session.nodeInstanceProjection(updatedSwitch)?.dataInputs.map((port) => port.titleKey),
    ).toEqual([
      'node.control.switch.input.value.title',
      'node.control.switch.input.case-1.title',
      'node.control.switch.input.case-2.title',
    ])
  })

  it('owns revision, history, checks, save and Program Run facts', async () => {
    const source = emptySource()
    const saved = sourceView(source)
    const run = runView('QUEUED')
    const transport = mockTransport(saved, run)
    const session = new EditorSession(transport, () => 'node_concat')

    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      position: { x: 10, y: 20 },
    })
    session.apply({ kind: 'bind-value', nodeId: 'node_concat', portId: 'a', value: 'hello' })
    session.apply({ kind: 'bind-value', nodeId: 'node_concat', portId: 'b', value: ' world' })

    expect(session.source?.revision).toBe(1)
    expect(session.dirty).toBe(true)
    expect(session.canUndo).toBe(true)
    session.undo()
    expect(session.currentGraph?.nodes[0].bindings.b).toBeUndefined()
    session.redo()
    expect(session.currentGraph?.nodes[0].bindings.b?.value).toBe(' world')

    const started = await session.run()
    expect(started?.runId).toBe(run.runId)
    expect(session.lastRunHash).toBe('sha256:program')
    expect(session.dirty).toBe(false)
    expect(transport.applyPatch).toHaveBeenCalledWith(
      source.workflow.id,
      0,
      expect.arrayContaining([expect.objectContaining({ kind: 'add-node' })]),
    )
    expect(transport.startRun).toHaveBeenCalledWith(source.workflow.id)
  })

  it('coalesces concurrent saves so one base revision is only committed once', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    let releaseSave!: () => void
    const saveGate = new Promise<void>((resolve) => {
      releaseSave = resolve
    })
    transport.applyPatch = vi.fn(async (_workflowId, baseRevision) => {
      await saveGate
      const persisted = structuredClone(source)
      persisted.workflow.name = 'renamed'
      persisted.revision = baseRevision + 1
      return { source: sourceView(persisted), generatedNodes: [] }
    })
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    session.apply({ kind: 'rename-workflow', name: 'renamed' })

    const first = session.save()
    const second = session.save()

    await vi.waitFor(() => expect(transport.applyPatch).toHaveBeenCalledTimes(1))
    releaseSave()
    await expect(Promise.all([first, second])).resolves.toHaveLength(2)
    expect(session.baseRevision).toBe(1)
    expect(session.dirty).toBe(false)
  })

  it('refreshes a clean kept-alive session when the durable revision changes', async () => {
    const source = emptySource()
    const latest = structuredClone(source)
    latest.workflow.name = 'renamed elsewhere'
    latest.revision = 1
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    transport.getSource = vi
      .fn()
      .mockResolvedValueOnce(sourceView(source))
      .mockResolvedValueOnce(sourceView(latest))
    const session = new EditorSession(transport)

    await session.load(source.workflow.id)
    await expect(session.refreshIfClean()).resolves.toBe(true)

    expect(session.baseRevision).toBe(1)
    expect(session.source?.workflow.name).toBe('renamed elsewhere')
    expect(session.dirty).toBe(false)
  })

  it('rebases pending commands once when another writer advanced the revision', async () => {
    const source = emptySource()
    const latest = structuredClone(source)
    latest.revision = 1
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    transport.getSource = vi
      .fn()
      .mockResolvedValueOnce(sourceView(source))
      .mockResolvedValueOnce(sourceView(latest))
    transport.applyPatch = vi
      .fn()
      .mockRejectedValueOnce(
        new RPCError(
          { id: 'workflow.revision.conflict' },
          'workflow.applyPatch',
          'op-conflict',
          null,
        ),
      )
      .mockImplementationOnce(async (_workflowId, baseRevision) => {
        const persisted = structuredClone(latest)
        persisted.workflow.name = 'local edit'
        persisted.revision = baseRevision + 1
        return { source: sourceView(persisted), generatedNodes: [] }
      })
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    session.apply({ kind: 'rename-workflow', name: 'local edit' })

    await expect(session.save()).resolves.toMatchObject({ revision: 2 })

    expect(transport.applyPatch).toHaveBeenNthCalledWith(1, source.workflow.id, 0, [
      expect.objectContaining({ kind: 'rename-workflow' }),
    ])
    expect(transport.applyPatch).toHaveBeenNthCalledWith(2, source.workflow.id, 1, [
      expect.objectContaining({ kind: 'rename-workflow' }),
    ])
    expect(session.baseRevision).toBe(2)
    expect(session.dirty).toBe(false)
    expect(session.saveErrorKind).toBe('')
  })

  it('keeps the draft and explains a backend validation failure in the save banner', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    transport.applyPatch = vi.fn(async () => {
      throw new RPCError(
        { id: 'INVALID_FIELD', category: 'validation' },
        'workflow.applyPatch',
        'op-invalid',
        null,
      )
    })
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    session.apply({ kind: 'rename-workflow', name: 'renamed' })

    await expect(session.save()).rejects.toThrow('INVALID_FIELD')

    expect(session.dirty).toBe(true)
    expect(session.saveErrorKind).toBe('validation')
    expect(session.saveError).toContain('节点参数或连线')
    expect(session.saveError).not.toBe('INVALID_FIELD')
  })

  it('keeps the invalid field target so the save banner can locate the node', async () => {
    const source = emptySource()
    source.graphs[0]!.nodes.push({
      id: 'concat',
      nodeRef: concat.nodeRef,
      label: '',
      config: {},
      bindings: {},
      position: { x: 0, y: 0 },
      disabled: false,
    })
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    transport.applyPatch = vi.fn(async () => {
      throw new RPCError(
        {
          id: 'INVALID_CONFIG_VALUE',
          category: 'validation',
          params: { commandIndex: 0 },
        },
        'workflow.applyPatch',
        'op-1',
        null,
      )
    })
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    session.apply({
      kind: 'set-config',
      nodeId: 'concat',
      fieldId: 'separator',
      value: 42,
    })

    await expect(session.save()).rejects.toThrow()

    expect(session.saveErrorTarget).toEqual({
      graphId: 'main',
      nodeId: 'concat',
      fieldId: 'separator',
      portId: undefined,
    })
  })

  it('does not resurrect a raw save error after dismissing a failed run save banner', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    transport.applyPatch = vi.fn(async () => {
      throw new RPCError(
        { id: 'INVALID_FIELD', category: 'validation' },
        'workflow.applyPatch',
        'op-invalid',
        null,
      )
    })
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    session.apply({ kind: 'rename-workflow', name: 'renamed' })

    await expect(session.run()).rejects.toThrow('INVALID_FIELD')
    expect(session.saveError).toContain('节点参数或连线')
    expect(session.openFailure).toBe('')

    session.dismissSaveError()
    expect(session.saveError).toBe('')
    expect(session.openFailure).toBe('')
  })

  it('checks the current unsaved draft without persisting it', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport, () => 'node_concat')
    await session.load(source.workflow.id)

    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      position: { x: 10, y: 20 },
    })

    await session.check()

    expect(transport.applyPatch).not.toHaveBeenCalled()
    expect(transport.checkDraft).toHaveBeenCalledOnce()
    const checked = JSON.parse(
      vi.mocked(transport.checkDraft).mock.calls[0]![0],
    ) as YottaWorkflowSource
    expect(checked.graphs[0]!.nodes.map((candidate) => candidate.id)).toContain('node_concat')
    expect(session.dirty).toBe(true)
  })

  it('blocks save on draft diagnostics and preserves the editable draft', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    transport.checkDraft = vi.fn(
      async () =>
        ({
          sourceHash: 'draft-hash',
          programHash: '',
          diagnostics: [
            {
              severity: 'error' as const,
              code: 'UNKNOWN_NODE_TYPE',
              graphPath: ['main'],
              nodeId: 'legacy-node',
              fieldPath: ['config', 'target'],
            },
          ],
        }) as CompileView,
    )
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    session.apply({ kind: 'rename-workflow', name: '仍保留的修改' })

    await expect(session.save()).rejects.toMatchObject({ id: 'workflow.draft.invalid' })

    expect(transport.applyPatch).not.toHaveBeenCalled()
    expect(session.dirty).toBe(true)
    expect(session.source?.workflow.name).toBe('仍保留的修改')
    expect(session.saveErrorTarget).toEqual({
      graphId: 'main',
      nodeId: 'legacy-node',
      fieldId: 'target',
    })
  })

  it('starts and controls a true debug Run through the admitted Run transport', async () => {
    const source = emptySource()
    const run = runView('RUNNING')
    const transport = mockTransport(sourceView(source), run)
    const paused = {
      status: 'paused',
      generation: 4,
      graphId: 'main',
      nodeId: 'run-started',
      queue: [],
      inputs: {},
      outputs: {},
      state: {},
    } as DebugSnapshot
    vi.mocked(transport.startDebugRun).mockResolvedValue({
      sourceHash: 'sha256:source-next',
      programHash: 'sha256:program',
      diagnostics: [],
      run,
      debug: paused,
      readiness: { state: 'started' },
    } as StartRunView)
    vi.mocked(transport.controlDebugRun).mockResolvedValue({
      ...paused,
      status: 'running',
      generation: 5,
    } as DebugSnapshot)
    const session = new EditorSession(transport)

    await session.load(source.workflow.id)
    await session.startDebug([{ graphId: 'main', nodeId: 'run-started' } as never])

    expect(transport.startDebugRun).toHaveBeenCalledWith(source.workflow.id, [
      { graphId: 'main', nodeId: 'run-started' },
    ])
    expect(session.debugSnapshot?.nodeId).toBe('run-started')
    expect(
      session.acceptDebugSnapshot(run.runId, { ...paused, generation: 3 } as DebugSnapshot),
    ).toBe(false)
    await session.controlDebug('continue')
    expect(session.debugSnapshot?.status).toBe('running')
  })

  it('keeps the first paused event when it arrives before StartDebugRun returns', async () => {
    const source = emptySource()
    const run = runView('RUNNING')
    const transport = mockTransport(sourceView(source), run)
    let resolveStart!: (view: StartRunView) => void
    vi.mocked(transport.startDebugRun).mockImplementation(
      () => new Promise((resolve) => (resolveStart = resolve)),
    )
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)

    const starting = session.startDebug([])
    const paused = {
      status: 'paused',
      generation: 2,
      graphId: 'main',
      graphPath: ['main'],
      nodeId: 'run-started',
      queue: [],
      inputs: {},
      outputs: {},
      state: {},
    } as DebugSnapshot
    await vi.waitFor(() => expect(resolveStart).toBeTypeOf('function'))
    session.acceptDebugSnapshot(run.runId, paused)
    resolveStart({
      sourceHash: 'sha256:source-next',
      programHash: 'sha256:program',
      diagnostics: [],
      run,
      debug: { ...paused, status: 'running', generation: 1 },
      readiness: { state: 'started' },
    } as StartRunView)

    await starting
    expect(session.debugSnapshot).toMatchObject({
      status: 'paused',
      generation: 2,
      nodeId: 'run-started',
    })
  })

  it('does not let a stale debug control response overwrite a newer event', async () => {
    const source = emptySource()
    const run = runView('RUNNING')
    const transport = mockTransport(sourceView(source), run)
    const paused = {
      status: 'paused',
      generation: 4,
      graphId: 'main',
      nodeId: 'run-started',
      queue: [],
      inputs: {},
      outputs: {},
      state: {},
    } as DebugSnapshot
    vi.mocked(transport.startDebugRun).mockResolvedValue({
      sourceHash: 'sha256:source-next',
      programHash: 'sha256:program',
      diagnostics: [],
      run,
      debug: paused,
      readiness: { state: 'started' },
    } as StartRunView)
    let resolveControl!: (snapshot: DebugSnapshot) => void
    vi.mocked(transport.controlDebugRun).mockImplementation(
      () => new Promise((resolve) => (resolveControl = resolve)),
    )
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    await session.startDebug([])

    const control = session.controlDebug('step')
    const completed = {
      ...paused,
      status: 'completed',
      generation: 6,
      nodeId: '',
    } as DebugSnapshot
    expect(session.acceptDebugSnapshot(run.runId, completed)).toBe(true)
    resolveControl({ ...paused, status: 'running', generation: 5 } as DebugSnapshot)

    await expect(control).resolves.toEqual(completed)
    expect(session.debugSnapshot).toEqual(completed)
  })

  it('rejects an incompatible or wrong-carrier edge before compile', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const ids = ['blob_to_stream', 'concat']
    const session = new EditorSession(transport, () => ids.shift() ?? 'unused')
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: blobToStream.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })
    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      position: { x: 200, y: 0 },
    })

    expect(() =>
      session.apply({
        kind: 'connect',
        edge: {
          channel: 'data',
          from: { nodeId: 'blob_to_stream', portId: 'stream' },
          to: { nodeId: 'concat', portId: 'a' },
        },
      }),
    ).toThrow('not assignable')
  })

  it('persists BlobRef bindings through the authoring patch protocol', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport, () => 'blob_to_stream')
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: blobToStream.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })
    const blob = {
      mediaType: 'application/octet-stream',
      digest: `sha256:${'a'.repeat(64)}`,
      size: 12,
    }
    session.apply({ kind: 'bind-blob', nodeId: 'blob_to_stream', portId: 'blob', blob })
    expect(session.currentGraph?.nodes[0].bindings.blob).toEqual({ kind: 'blob', blob })
    await session.save()
    expect(transport.applyPatch).toHaveBeenCalledWith(
      source.workflow.id,
      0,
      expect.arrayContaining([expect.objectContaining({ kind: 'bind-blob' })]),
    )
  })

  it('uses nominal union and list assignability from the current contract', () => {
    const stringType = concat.dataInputs[0].type.expression
    const number = authoring.body.types.find((type) => type.typeRef.typeId.endsWith('/number/v1'))!
    const numberType: TypeExpression = { kind: 'ref', ref: number.typeRef }
    const union: TypeExpression = {
      kind: 'union',
      members: [stringType, numberType],
    }
    expect(assignable(stringType, union)).toBe(true)
    expect(assignable(union, stringType)).toBe(false)
    expect(
      assignable({ kind: 'list', element: stringType }, { kind: 'list', element: union }),
    ).toBe(false)
  })

  it('uses instruction semantics for exec and error input hints', async () => {
    const source = emptySource()
    const ids = ['delay', 'retry']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: delay.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })
    session.apply({
      kind: 'add-node',
      nodeTypeId: retry.nodeRef.nodeTypeId,
      position: { x: 200, y: 0 },
    })

    session.apply({
      kind: 'connect',
      edge: {
        channel: 'error',
        from: { nodeId: 'delay', portId: 'failed' },
        to: { nodeId: 'retry', portId: 'retry' },
      },
    })
    expect(session.currentGraph?.edges).toHaveLength(1)
    expect(() =>
      session.apply({
        kind: 'connect',
        edge: {
          channel: 'exec',
          from: { nodeId: 'delay', portId: 'done' },
          to: { nodeId: 'retry', portId: 'retry' },
        },
      }),
    ).toThrow('invalid signal ports')
  })

  it('inserts and connects a compatible node as one undoable edit', async () => {
    const source = emptySource()
    const ids = ['delay', 'retry']
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport, () => ids.shift() ?? 'unused')
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: delay.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })

    const inserted = session.insertConnectedNode(
      'delay',
      { channel: 'error', direction: 'output', portId: 'failed' },
      retry.nodeRef.nodeTypeId,
      { channel: 'error', direction: 'input', portId: 'retry' },
      { x: 220, y: 0 },
    )

    expect(inserted).toBe('retry')
    expect(session.currentGraph?.nodes.map((candidate) => candidate.id)).toEqual(['delay', 'retry'])
    expect(session.currentGraph?.edges).toEqual([
      {
        channel: 'error',
        from: { nodeId: 'delay', portId: 'failed' },
        to: { nodeId: 'retry', portId: 'retry' },
      },
    ])

    session.undo()
    expect(session.currentGraph?.nodes.map((candidate) => candidate.id)).toEqual(['delay'])
    expect(session.currentGraph?.edges).toEqual([])
    session.redo()
    expect(session.currentGraph?.nodes).toHaveLength(2)
    expect(session.currentGraph?.edges).toHaveLength(1)

    await session.save()
    expect(transport.applyPatch).toHaveBeenCalledWith(
      source.workflow.id,
      0,
      expect.arrayContaining([
        expect.objectContaining({ kind: 'add-node' }),
        expect.objectContaining({ kind: 'connect' }),
      ]),
    )
  })

  it('inserts a signal node into an existing edge as one undoable edit', async () => {
    const source = emptySource()
    const ids = ['first', 'second', 'inserted']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: delay.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })
    session.apply({
      kind: 'add-node',
      nodeTypeId: delay.nodeRef.nodeTypeId,
      position: { x: 500, y: 0 },
    })
    const edge = {
      channel: 'exec' as const,
      from: { nodeId: 'first', portId: 'done' },
      to: { nodeId: 'second', portId: 'in' },
    }
    session.apply({ kind: 'connect', edge })

    expect(session.insertNodeIntoSignalEdge(edge, delay.nodeRef.nodeTypeId, { x: 250, y: 0 })).toBe(
      'inserted',
    )
    expect(session.currentGraph?.edges).toEqual([
      { channel: 'exec', from: edge.from, to: { nodeId: 'inserted', portId: 'in' } },
      { channel: 'exec', from: { nodeId: 'inserted', portId: 'done' }, to: edge.to },
    ])
    session.undo()
    expect(session.currentGraph?.nodes.map((node) => node.id)).toEqual(['first', 'second'])
    expect(session.currentGraph?.edges).toEqual([edge])
  })

  it('inserts one node into each selected signal edge as one undoable edit', async () => {
    const source = emptySource()
    const ids = ['first_a', 'second_a', 'first_b', 'second_b', 'inserted_a', 'inserted_b']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    for (const position of [0, 500, 200, 700]) {
      session.apply({
        kind: 'add-node',
        nodeTypeId: delay.nodeRef.nodeTypeId,
        position: { x: position, y: position >= 200 && position < 500 ? 200 : 0 },
      })
    }
    const edges = [
      {
        channel: 'exec' as const,
        from: { nodeId: 'first_a', portId: 'done' },
        to: { nodeId: 'second_a', portId: 'in' },
      },
      {
        channel: 'exec' as const,
        from: { nodeId: 'first_b', portId: 'done' },
        to: { nodeId: 'second_b', portId: 'in' },
      },
    ]
    session.applyBatch(edges.map((edge) => ({ kind: 'connect', edge })))

    expect(
      session.insertNodesIntoSignalEdges(edges, delay.nodeRef.nodeTypeId, [
        { x: 250, y: 0 },
        { x: 450, y: 200 },
      ]),
    ).toEqual(['inserted_a', 'inserted_b'])
    expect(session.currentGraph?.nodes).toHaveLength(6)
    expect(session.currentGraph?.edges).toHaveLength(4)
    session.undo()
    expect(session.currentGraph?.nodes).toHaveLength(4)
    expect(session.currentGraph?.edges).toEqual(edges)
  })

  it('inserts an invoke node directly from a failure output', async () => {
    const source = emptySource()
    const ids = ['delay', 'log']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: delay.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })

    expect(
      session.insertConnectedNode(
        'delay',
        { channel: 'error', direction: 'output', portId: 'failed' },
        log.nodeRef.nodeTypeId,
        { channel: 'exec', direction: 'input', portId: 'in' },
        { x: 220, y: 0 },
      ),
    ).toBe('log')
    expect(session.currentGraph?.edges).toEqual([
      {
        channel: 'error',
        from: { nodeId: 'delay', portId: 'failed' },
        to: { nodeId: 'log', portId: 'in' },
      },
    ])
  })

  it('inserts number to string before a panel log using the generic converter', async () => {
    const source = emptySource()
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => 'number-as-text',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: 'https://schemas.yotta.dev/nodes/panels/read-number',
      nodeId: 'number',
      position: { x: 0, y: 0 },
    })
    session.apply({
      kind: 'add-node',
      nodeTypeId: 'https://schemas.yotta.dev/nodes/panels/log',
      nodeId: 'log',
      position: { x: 400, y: 0 },
    })
    const edge = {
      channel: 'data' as const,
      from: { nodeId: 'number', portId: 'value' },
      to: { nodeId: 'log', portId: 'value' },
    }
    const plan = session.connectionCompatibility(edge)
    const candidate = plan.conversions?.find((c) => c.nodeTypeId === toString.nodeRef.nodeTypeId)
    expect(candidate).toBeDefined()
    if (!candidate) throw new Error('missing to-string candidate')
    const showError = vi.fn()
    const connection = useWorkflowConnectionAuthoring({
      session,
      canvasElement: ref(null),
      selectedNodeId: ref(''),
      selectedNodeIds: ref(new Set<string>()),
      applyCommand: (command) => {
        session.apply(command)
        return true
      },
      addNode: vi.fn(),
      screenToFlowCoordinate: (p) => p,
      uniqueStateName: (n) => n,
      translate: (k) => k,
      translationExists: () => false,
      showError,
    })
    connection.connect({
      source: 'number',
      target: 'log',
      sourceHandle: graphHandle('data', 'output', 'value'),
      targetHandle: graphHandle('data', 'input', 'value'),
    })
    const offered = connection.pendingConversion.value?.candidates.find(
      (c) => c.nodeTypeId === toString.nodeRef.nodeTypeId,
    )
    if (!offered) throw new Error('conversion dialog did not open')
    connection.applyConversion(offered)
    expect(showError).not.toHaveBeenCalled()
    expect(session.currentGraph?.edges).toHaveLength(2)
    expect(session.currentGraph?.nodes.find((n) => n.id === 'number-as-text')?.nodeRef).toEqual(
      toString.nodeRef,
    )
    session.undo()
    expect(session.currentGraph?.nodes).toHaveLength(2)
    expect(session.currentGraph?.edges).toHaveLength(0)
    connection.connect({
      source: 'number',
      target: 'log',
      sourceHandle: graphHandle('data', 'output', 'value'),
      targetHandle: graphHandle('data', 'input', 'value'),
    })
    session.apply({ kind: 'remove-node', nodeId: 'log' })
    connection.applyConversion(candidate)
    expect(showError).toHaveBeenCalledOnce()
    const failure = showError.mock.calls[0]?.[1]
    expect(failure).toBeInstanceOf(RPCError)
    expect(errorMessage(failure)).toContain('重新连接')
    expect(errorMessage(failure)).not.toContain('未知错误')
    expect(connection.pendingConversion.value).not.toBeNull()
    expect(session.currentGraph?.nodes.map((n) => n.id)).toEqual(['number'])
  })

  it('inserts a visible conversion bridge as one undoable edit', async () => {
    const source = emptySource()
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => 'string-to-number',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      nodeId: 'concat',
      position: { x: 0, y: 0 },
    })
    session.apply({
      kind: 'add-node',
      nodeTypeId: greater.nodeRef.nodeTypeId,
      nodeId: 'greater',
      position: { x: 440, y: 0 },
    })
    const edge = {
      channel: 'data' as const,
      from: { nodeId: 'concat', portId: 'result' },
      to: { nodeId: 'greater', portId: 'a' },
    }
    const plan = session.connectionCompatibility(edge)
    expect(plan).toMatchObject({ valid: false, disposition: 'conversion' })
    const conversion = plan.conversions?.[0]
    if (!conversion) throw new Error('missing conversion plan')

    const inserted = session.insertConversionBridge(edge, conversion, { x: 220, y: 0 })

    expect(inserted).toBe('string-to-number')
    expect(session.currentGraph?.nodes.map((candidate) => candidate.id)).toEqual([
      'concat',
      'greater',
      'string-to-number',
    ])
    expect(session.currentGraph?.edges).toEqual([
      {
        channel: 'data',
        from: { nodeId: 'concat', portId: 'result' },
        to: { nodeId: 'string-to-number', portId: 'text' },
      },
      {
        channel: 'data',
        from: { nodeId: 'string-to-number', portId: 'result' },
        to: { nodeId: 'greater', portId: 'a' },
      },
    ])

    session.undo()
    expect(session.currentGraph?.nodes.map((candidate) => candidate.id)).toEqual([
      'concat',
      'greater',
    ])
    expect(session.currentGraph?.edges).toEqual([])
    session.redo()
    expect(session.currentGraph?.nodes).toHaveLength(3)
    expect(session.currentGraph?.edges).toHaveLength(2)
  })

  it('promotes a durable output to typed state as one undoable edit', async () => {
    const source = emptySource()
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => 'state-write',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      nodeId: 'concat',
      position: { x: 0, y: 0 },
    })

    const stateNodeId = session.promoteOutputToState('concat', 'result', 'message', {
      x: 220,
      y: 0,
    })

    expect(stateNodeId).toBe('state-write')
    expect(session.source?.variables).toEqual([
      expect.objectContaining({ name: 'message', type: concat.dataOutputs[0]!.type.expression }),
    ])
    expect(session.currentGraph?.nodes.find((candidate) => candidate.id === 'state-write')).toEqual(
      expect.objectContaining({ config: { variable: 'message' } }),
    )
    expect(session.currentGraph?.edges).toContainEqual({
      channel: 'data',
      from: { nodeId: 'concat', portId: 'result' },
      to: { nodeId: 'state-write', portId: 'value' },
    })

    session.undo()
    expect(session.source?.variables).toEqual([])
    expect(session.currentGraph?.nodes.map((candidate) => candidate.id)).toEqual(['concat'])
    session.redo()
    expect(session.source?.variables[0]?.name).toBe('message')
    expect(session.currentGraph?.nodes).toHaveLength(2)
  })

  it('changes an unreferenced state type and default atomically', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport, () => 'unused')
    await session.load(source.workflow.id)
    const stringType = authoring.body.types.find((type) =>
      type.typeRef.typeId.includes('/core/string/'),
    )!
    const integerType = authoring.body.types.find((type) =>
      type.typeRef.typeId.includes('/core/integer/'),
    )!
    session.apply({
      kind: 'add-state-variable',
      name: 'value',
      type: { kind: 'ref', ref: stringType.typeRef },
      defaultValue: '',
    })
    session.apply({
      kind: 'update-state-variable',
      name: 'value',
      type: { kind: 'ref', ref: integerType.typeRef },
      defaultValue: 0,
    })
    expect(session.source?.variables[0]).toEqual({
      name: 'value',
      type: { kind: 'ref', ref: integerType.typeRef },
      default: 0,
    })
    session.undo()
    expect(session.source?.variables[0]?.type).toEqual({ kind: 'ref', ref: stringType.typeRef })
    session.redo()
    await session.save()
    expect(transport.applyPatch).toHaveBeenCalledWith(
      source.workflow.id,
      0,
      expect.arrayContaining([
        expect.objectContaining({
          kind: 'update-state-variable',
          updateStateVariable: expect.objectContaining({ name: 'value', default: 0 }),
        }),
      ]),
    )
  })

  it('inserts Delay from the RunStarted exec output', async () => {
    const source = emptySource()
    const ids = ['run-started', 'delay']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: runStarted.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })

    expect(
      session.insertConnectedNode(
        'run-started',
        { channel: 'exec', direction: 'output', portId: 'started' },
        delay.nodeRef.nodeTypeId,
        { channel: 'exec', direction: 'input', portId: 'in' },
        { x: 220, y: 0 },
      ),
    ).toBe('delay')
    expect(session.currentGraph?.edges).toEqual([
      {
        channel: 'exec',
        from: { nodeId: 'run-started', portId: 'started' },
        to: { nodeId: 'delay', portId: 'in' },
      },
    ])
  })

  it('keeps duplicate, batch move and batch delete atomic', async () => {
    const source = emptySource()
    const ids = ['concat_a', 'concat_b', 'copy_a', 'copy_b']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      position: { x: 10, y: 20 },
    })
    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      position: { x: 250, y: 20 },
    })
    session.apply({ kind: 'set-node-label', nodeId: 'concat_a', label: 'Source' })
    session.apply({ kind: 'bind-value', nodeId: 'concat_a', portId: 'a', value: 'hello' })
    session.apply({ kind: 'bind-value', nodeId: 'concat_a', portId: 'b', value: ' world' })
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'data',
        from: { nodeId: 'concat_a', portId: 'result' },
        to: { nodeId: 'concat_b', portId: 'a' },
      },
    })

    expect(session.duplicateNodes(['concat_a', 'concat_b'])).toEqual(['copy_a', 'copy_b'])
    expect(session.currentGraph?.nodes).toHaveLength(4)
    expect(session.currentGraph?.nodes.find((node) => node.id === 'copy_a')).toMatchObject({
      label: 'Source',
      bindings: { a: { kind: 'value', value: 'hello' } },
      position: { x: 42, y: 52 },
    })
    expect(session.currentGraph?.edges.at(-1)).toEqual({
      channel: 'data',
      from: { nodeId: 'copy_a', portId: 'result' },
      to: { nodeId: 'copy_b', portId: 'a' },
    })
    session.undo()
    expect(session.currentGraph?.nodes).toHaveLength(2)

    session.moveNodes([
      { nodeId: 'concat_a', position: { x: 100, y: 120 } },
      { nodeId: 'concat_b', position: { x: 340, y: 120 } },
    ])
    expect(session.currentGraph?.nodes.map((node) => node.position)).toEqual([
      { x: 100, y: 120 },
      { x: 340, y: 120 },
    ])
    session.undo()
    expect(session.currentGraph?.nodes.map((node) => node.position)).toEqual([
      { x: 10, y: 20 },
      { x: 250, y: 20 },
    ])

    session.removeNodes(['concat_a', 'concat_b'])
    expect(session.currentGraph?.nodes).toEqual([])
    session.undo()
    expect(session.currentGraph?.nodes).toHaveLength(2)
    expect(session.currentGraph?.edges).toHaveLength(1)
  })

  it('inserts a linear recording draft as one undoable edit', async () => {
    const source = emptySource()
    const ids = ['recorded_a', 'recorded_b']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)

    const inserted = session.insertLinearDraft(
      [
        {
          nodeTypeID: delay.nodeRef.nodeTypeId,
          config: {},
          values: { 'duration-milliseconds': 100 },
          blobs: {},
          execInput: 'in',
          execOutput: 'done',
        },
        {
          nodeTypeID: delay.nodeRef.nodeTypeId,
          config: {},
          values: { 'duration-milliseconds': 250 },
          blobs: {},
          execInput: 'in',
          execOutput: 'done',
        },
      ],
      { x: 320, y: 180 },
    )

    expect(inserted).toEqual(['recorded_a', 'recorded_b'])
    expect(session.currentGraph?.nodes).toHaveLength(2)
    expect(session.currentGraph?.edges).toEqual([
      {
        channel: 'exec',
        from: { nodeId: 'recorded_a', portId: 'done' },
        to: { nodeId: 'recorded_b', portId: 'in' },
      },
    ])
    session.undo()
    expect(session.currentGraph?.nodes).toEqual([])
    expect(session.currentGraph?.edges).toEqual([])
  })

  it('lets recorded target nodes inherit the matching workflow default', async () => {
    const source = emptySource()
    source.targetDefaults = [{ target: 'target', slot: 'window-target' }]
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => 'recorded_click',
    )
    await session.load(source.workflow.id)

    session.insertLinearDraft(
      [
        {
          nodeTypeID: clickPointer.nodeRef.nodeTypeId,
          config: { slot: 'window-target' },
          values: {
            point: { x: 0.5, y: 0.5, unit: 'ratio' },
            button: 'left',
            'hold-duration': 0,
          },
          blobs: {},
          execInput: 'in',
          execOutput: 'completed',
        },
      ],
      { x: 100, y: 100 },
    )

    expect(session.currentGraph?.nodes[0]?.config).toEqual({})
  })

  it('collapses a selection into a navigable graph call as one undoable edit', async () => {
    const source = emptySource()
    const ids = [
      'start',
      'delay',
      'end',
      'child',
      'call_child',
      'comment',
      'call_copy',
      'comment_copy',
    ]
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport, () => ids.shift() ?? 'unused')
    await session.load(source.workflow.id)
    for (const [projection, x] of [
      [runStarted, 0],
      [delay, 240],
      [endBranch, 480],
    ] as const) {
      session.apply({
        kind: 'add-node',
        nodeTypeId: projection.nodeRef.nodeTypeId,
        position: { x, y: 0 },
      })
    }
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'exec',
        from: { nodeId: 'start', portId: 'started' },
        to: { nodeId: 'delay', portId: 'in' },
      },
    })
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'exec',
        from: { nodeId: 'delay', portId: 'done' },
        to: { nodeId: 'end', portId: 'in' },
      },
    })

    expect(session.collapseSelection(['delay'], 'Reusable delay')).toBe('call_child')
    expect(session.currentGraph?.calls).toEqual([
      expect.objectContaining({ id: 'call_child', graphId: 'child', label: 'Reusable delay' }),
    ])
    expect(session.currentGraph?.edges).toEqual([
      expect.objectContaining({ to: { nodeId: 'call_child', portId: 'in' } }),
      expect.objectContaining({ from: { nodeId: 'call_child', portId: 'exit_done_1' } }),
    ])
    expect(session.source?.graphs.find((graph) => graph.id === 'child')).toMatchObject({
      kind: 'subgraph',
      entries: [{ nodeId: 'delay', portId: 'in' }],
      exits: [
        { id: 'exit_done_1', channel: 'exec', endpoint: { nodeId: 'delay', portId: 'done' } },
        {
          id: 'exit_failed_2',
          channel: 'error',
          endpoint: { nodeId: 'delay', portId: 'failed' },
        },
      ],
    })

    session.undo()
    expect(session.source?.graphs).toHaveLength(1)
    expect(session.currentGraph?.nodes.map((candidate) => candidate.id)).toEqual([
      'start',
      'delay',
      'end',
    ])
    session.redo()
    session.openGraphPath(['main', 'call_child', 'child'])
    expect(session.currentGraph?.id).toBe('child')
    expect(session.graphPath).toEqual(['main', 'child'])

    session.openGraphPath(['main'])
    expect(session.addAnnotation({ x: 80, y: 160 })).toBe('comment')
    const copied = session.insertNodeSelection(
      session.selectionSnapshot(['call_child', 'comment']),
      { x: 32, y: 32 },
    )
    expect(copied).toEqual(['call_copy', 'comment_copy'])
    expect(session.currentGraph?.calls).toHaveLength(2)
    expect(session.currentGraph?.annotations).toHaveLength(2)

    await session.save()
    expect(transport.applyPatch).toHaveBeenCalledWith(
      source.workflow.id,
      0,
      expect.arrayContaining([
        expect.objectContaining({ kind: 'collapse-selection' }),
        expect.objectContaining({ kind: 'add-graph-call' }),
        expect.objectContaining({ kind: 'add-annotation' }),
      ]),
    )
  })

  it('exposes unconnected signal outputs when collapsing a trailing node', async () => {
    const source = emptySource()
    const ids = ['start', 'keys', 'child', 'call_child']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    for (const [projection, x] of [
      [runStarted, 0],
      [pressKeys, 240],
    ] as const) {
      session.apply({
        kind: 'add-node',
        nodeTypeId: projection.nodeRef.nodeTypeId,
        position: { x, y: 0 },
      })
    }
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'exec',
        from: { nodeId: 'start', portId: 'started' },
        to: { nodeId: 'keys', portId: 'in' },
      },
    })

    expect(session.collapseSelection(['keys'], 'Press keys')).toBe('call_child')
    expect(session.source?.graphs.find((graph) => graph.id === 'child')?.exits).toEqual([
      {
        id: 'exit_completed_1',
        name: 'completed',
        channel: 'exec',
        endpoint: { nodeId: 'keys', portId: 'completed' },
      },
      {
        id: 'exit_failed_2',
        name: 'failed',
        channel: 'error',
        endpoint: { nodeId: 'keys', portId: 'failed' },
      },
    ])
  })

  it('inserts an existing subgraph call and reuses the callee input projection', async () => {
    const source = emptySource()
    const duration = delay.dataInputs.find((port) => port.id === 'duration-milliseconds')!
    source.graphs.push({
      id: 'child',
      name: 'Child',
      kind: 'subgraph',
      nodes: [
        {
          id: 'wait',
          nodeRef: delay.nodeRef,
          position: { x: 0, y: 0 },
          config: {},
          bindings: {},
        },
      ],
      edges: [],
      inputs: [],
      outputs: [],
      entries: [],
      exits: [],
    })
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => 'call_child',
    )
    await session.load(source.workflow.id)
    session.enterGraph('child')
    session.inferCurrentGraphInterface()
    expect(session.currentGraph).toMatchObject({
      entries: [{ nodeId: 'wait', portId: 'in' }],
      inputs: [
        {
          id: 'input_duration-milliseconds_1',
          nodeId: 'wait',
          portId: 'duration-milliseconds',
        },
      ],
      exits: expect.arrayContaining([
        expect.objectContaining({
          channel: 'exec',
          endpoint: { nodeId: 'wait', portId: 'done' },
        }),
      ]),
    })
    session.openGraphPath(['main'])

    expect(session.graphInputProjection('child', 'input_duration-milliseconds_1')).toMatchObject({
      id: 'input_duration-milliseconds_1',
      type: duration.type,
    })
    expect(session.insertGraphCall('child', { x: 120, y: 80 })).toBe('call_child')
    session.apply({
      kind: 'update-graph-call',
      call: {
        ...session.currentGraph!.calls![0],
        bindings: { 'input_duration-milliseconds_1': { kind: 'value', value: 250 } },
      },
    })
    expect(session.currentGraph?.calls?.[0]).toMatchObject({
      graphId: 'child',
      bindings: { 'input_duration-milliseconds_1': { kind: 'value', value: 250 } },
    })
  })

  it('separates deleting a graph call from deleting its reusable definition', async () => {
    const source = emptySource()
    source.graphs[0]!.calls = [
      {
        id: 'call-child',
        graphId: 'child',
        label: '等待',
        position: { x: 120, y: 80 },
        bindings: {},
      },
    ]
    source.graphs.push({
      id: 'child',
      name: '等待',
      kind: 'subgraph',
      nodes: [],
      calls: [],
      edges: [],
      inputs: [],
      outputs: [],
      entries: [],
      exits: [],
      annotations: [],
    })
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)

    expect(() => session.removeGraph('child')).toThrow('graph child is still referenced')
    expect(session.source?.graphs.map((graph) => graph.id)).toEqual(['main', 'child'])
    expect(session.dirty).toBe(false)

    session.apply({ kind: 'remove-graph-call', callId: 'call-child' })
    expect(session.currentGraph?.calls).toEqual([])
    expect(session.source?.graphs.some((graph) => graph.id === 'child')).toBe(true)

    session.removeGraph('child')
    expect(session.source?.graphs.map((graph) => graph.id)).toEqual(['main'])
    await session.save()
    expect(transport.applyPatch).toHaveBeenCalledWith(source.workflow.id, 0, [
      {
        kind: 'remove-graph-call',
        removeGraphCall: { graphId: 'main', callId: 'call-child' },
      },
      { kind: 'remove-graph', removeGraph: { graphId: 'child' } },
    ])
  })

  it('does not remove another definition when a graph ID is missing', async () => {
    const source = emptySource()
    source.graphs.push({
      id: 'child',
      name: '等待',
      kind: 'subgraph',
      nodes: [],
      calls: [],
      edges: [],
      inputs: [],
      outputs: [],
      entries: [],
      exits: [],
      annotations: [],
    })
    const session = new EditorSession(mockTransport(sourceView(source), runView('QUEUED')))
    await session.load(source.workflow.id)

    expect(() => session.removeGraph('missing')).toThrow('graph missing does not exist')
    expect(session.source?.graphs.map((graph) => graph.id)).toEqual(['main', 'child'])
    expect(session.dirty).toBe(false)
  })

  it('publishes entry, typed input, and named exits explicitly with stable IDs', async () => {
    const source = emptySource()
    source.graphs.push({
      id: 'child',
      name: '等待',
      kind: 'subgraph',
      nodes: [
        {
          id: 'wait',
          nodeRef: delay.nodeRef,
          position: { x: 0, y: 0 },
          config: {},
          bindings: {},
        },
      ],
      calls: [],
      edges: [],
      inputs: [],
      outputs: [],
      entries: [],
      exits: [],
      annotations: [],
    })
    const session = new EditorSession(mockTransport(sourceView(source), runView('QUEUED')))
    await session.load(source.workflow.id)
    session.enterGraph('child')

    const candidates = () => session.currentGraphInterfaceCandidates()
    const entry = candidates().find(
      (candidate) =>
        candidate.kind === 'entry' &&
        candidate.endpoint.nodeId === 'wait' &&
        candidate.endpoint.portId === 'in',
    )!
    const input = candidates().find(
      (candidate) =>
        candidate.kind === 'input' &&
        candidate.endpoint.nodeId === 'wait' &&
        candidate.endpoint.portId === 'duration-milliseconds',
    )!
    const exit = candidates().find(
      (candidate) =>
        candidate.kind === 'exit' &&
        candidate.endpoint.nodeId === 'wait' &&
        candidate.endpoint.portId === 'done',
    )!

    session.addCurrentGraphInterfaceCandidate(entry.key)
    session.addCurrentGraphInterfaceCandidate(input.key)
    session.addCurrentGraphInterfaceCandidate(exit.key)
    const inputID = session.currentGraph!.inputs[0]!.id
    const exitID = session.currentGraph!.exits![0]!.id
    session.renameCurrentGraphInterfaceItem('input', inputID, '等待时长')
    session.renameCurrentGraphInterfaceItem('exit', exitID, '等待完成')

    expect(session.currentGraph).toMatchObject({
      entries: [{ nodeId: 'wait', portId: 'in' }],
      inputs: [
        {
          id: inputID,
          name: '等待时长',
          nodeId: 'wait',
          portId: 'duration-milliseconds',
        },
      ],
      exits: [
        {
          id: exitID,
          name: '等待完成',
          channel: 'exec',
          endpoint: { nodeId: 'wait', portId: 'done' },
        },
      ],
    })
    expect(candidates().find((candidate) => candidate.key === input.key)?.published).toBe(true)
    expect(session.currentGraph!.inputs[0]!.id).toBe(inputID)
    expect(session.currentGraph!.exits![0]!.id).toBe(exitID)
  })

  it('duplicates calls and forks one call to an independent graph definition atomically', async () => {
    const source = subgraphLifecycleSource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const ids = ['call-copy', 'child-copy']
    const session = new EditorSession(transport, () => ids.shift() ?? '')
    await session.load(source.workflow.id)

    expect(session.duplicateCurrentGraphCall('call-child')).toBe('call-copy')
    expect(session.currentGraph?.calls).toEqual([
      expect.objectContaining({ id: 'call-child', graphId: 'child' }),
      expect.objectContaining({
        id: 'call-copy',
        graphId: 'child',
        position: { x: 152, y: 112 },
      }),
    ])

    expect(session.forkCurrentGraphCall('call-child')).toBe('child-copy')
    expect(session.currentGraph?.calls?.find((call) => call.id === 'call-child')?.graphId).toBe(
      'child-copy',
    )
    expect(session.source?.graphs.find((graph) => graph.id === 'child-copy')).toMatchObject({
      name: '等待 Copy',
      kind: 'subgraph',
    })

    session.undo()
    expect(session.source?.graphs.some((graph) => graph.id === 'child-copy')).toBe(false)
    expect(session.currentGraph?.calls?.find((call) => call.id === 'call-child')?.graphId).toBe(
      'child',
    )
    session.redo()
    await session.save()
    const commands = vi.mocked(transport.applyPatch).mock.calls[0]![2]
    expect(commands.map((command) => command.kind)).toEqual([
      'add-graph-call',
      'add-graph',
      'update-graph-call',
    ])
  })

  it('expands a call as one undoable edit while retaining its reusable definition', async () => {
    const source = subgraphLifecycleSource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport, () => 'expanded-wait')
    await session.load(source.workflow.id)

    expect(session.expandCurrentGraphCall('call-child')).toEqual(['expanded-wait'])
    expect(session.currentGraph?.calls).toEqual([])
    expect(session.currentGraph?.nodes).toEqual([
      expect.objectContaining({
        id: 'expanded-wait',
        position: { x: 120, y: 80 },
        bindings: { 'duration-milliseconds': { kind: 'value', value: 250 } },
      }),
    ])
    expect(session.source?.graphs.some((graph) => graph.id === 'child')).toBe(true)

    session.undo()
    expect(session.currentGraph?.calls?.map((call) => call.id)).toEqual(['call-child'])
    expect(session.currentGraph?.nodes).toEqual([])
    session.redo()
    await session.save()
    const commands = vi.mocked(transport.applyPatch).mock.calls[0]![2]
    expect(commands.map((command) => command.kind)).toEqual([
      'remove-graph-call',
      'add-node',
      'bind-value',
    ])
  })

  it('preserves workflow resource bindings when expanding a call', async () => {
    const source = subgraphLifecycleSource()
    source.graphs[0]!.calls![0]!.bindings = {}
    source.graphs[1]!.nodes[0]!.bindings = {
      'duration-milliseconds': {
        kind: 'resource',
        resource: { resourceId: 'template', variantId: 'default' },
      },
    }
    source.resources = [
      {
        id: 'template',
        kind: 'image',
        name: 'Template',
        image: {
          variants: [
            {
              id: 'default',
              resolution: [1, 1],
              bbox: [0, 0, 1, 1],
              blob: {
                mediaType: 'image/png',
                digest: `sha256:${'1'.repeat(64)}`,
                size: 1,
              },
            },
          ],
        },
      },
    ]
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport, () => 'expanded-wait')
    await session.load(source.workflow.id)

    session.expandCurrentGraphCall('call-child')
    await session.save()

    expect(vi.mocked(transport.applyPatch).mock.calls[0]![2]).toEqual([
      {
        kind: 'remove-graph-call',
        removeGraphCall: { graphId: 'main', callId: 'call-child' },
      },
      expect.objectContaining({ kind: 'add-node' }),
      {
        kind: 'bind-resource',
        bindResource: {
          graphId: 'main',
          nodeId: '$expanded-wait',
          portId: 'duration-milliseconds',
          resource: { resourceId: 'template', variantId: 'default' },
        },
      },
    ])
  })

  it('authors workflow resources as one undoable batch and protects referenced resources', async () => {
    const source = emptySource()
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)
    const resource = {
      id: 'template',
      kind: 'image' as const,
      name: 'Template',
      image: {
        variants: [
          {
            id: 'default',
            resolution: [1, 1] as [number, number],
            bbox: [0, 0, 1, 1] as [number, number, number, number],
            blob: {
              mediaType: 'image/png',
              digest: `sha256:${'1'.repeat(64)}`,
              size: 1,
            },
          },
        ] as [
          {
            id: string
            resolution: [number, number]
            bbox: [number, number, number, number]
            blob: { mediaType: string; digest: string; size: number }
          },
        ],
      },
    }

    session.applyBatch([
      { kind: 'add-resource', resource },
      {
        kind: 'update-resource-metadata',
        resourceId: resource.id,
        name: ' Updated ',
        description: ' local ',
        category: ' Fishing ',
        tags: ['ui', ' UI ', 'fishing'],
      },
    ])
    expect(session.source?.resources).toEqual([
      expect.objectContaining({
        id: 'template',
        name: 'Updated',
        description: 'local',
        category: 'Fishing',
        tags: ['fishing', 'ui'],
      }),
    ])

    session.undo()
    expect(session.source?.resources).toEqual([])
    session.redo()
    await session.save()
    expect(vi.mocked(transport.applyPatch).mock.calls[0]![2]).toEqual([
      { kind: 'add-resource', addResource: { resource } },
      {
        kind: 'update-resource-metadata',
        updateResourceMetadata: {
          resourceId: 'template',
          name: ' Updated ',
          description: ' local ',
          category: ' Fishing ',
          tags: ['ui', ' UI ', 'fishing'],
        },
      },
    ])

    const referenced = emptySource()
    referenced.resources = [resource]
    referenced.graphs[0]!.nodes.push({
      id: 'concat',
      nodeRef: concat.nodeRef,
      position: { x: 0, y: 0 },
      config: {},
      bindings: {
        a: {
          kind: 'resource',
          resource: { resourceId: resource.id, variantId: 'default' },
        },
      },
    })
    const referencedSession = new EditorSession(
      mockTransport(sourceView(referenced), runView('QUEUED')),
    )
    await referencedSession.load(referenced.workflow.id)
    const replacement = structuredClone(resource)
    replacement.image!.variants[0]!.resolution = [2, 2]
    replacement.image!.variants[0]!.bbox = [0, 0, 2, 2]
    replacement.image!.variants[0]!.blob = {
      mediaType: 'image/png',
      digest: `sha256:${'2'.repeat(64)}`,
      size: 2,
    }
    referencedSession.apply({
      kind: 'replace-resource',
      resourceId: resource.id,
      resource: replacement,
    })
    expect(referencedSession.source?.resources[0]?.image?.variants[0]?.blob).toEqual(
      replacement.image!.variants[0]!.blob,
    )
    expect(referencedSession.source?.graphs[0]?.nodes[0]?.bindings.a?.resource?.resourceId).toBe(
      resource.id,
    )
    referencedSession.undo()
    expect(referencedSession.source?.resources[0]?.image?.variants[0]?.blob).toEqual(
      resource.image!.variants[0]!.blob,
    )
    expect(() =>
      referencedSession.apply({
        kind: 'replace-resource',
        resourceId: resource.id,
        resource: { ...replacement, id: 'different' },
      }),
    ).toThrow('preserve identity')
    expect(() =>
      referencedSession.apply({ kind: 'remove-resource', resourceId: resource.id }),
    ).toThrow('still in use')
  })

  it('removes every call site and its definition as one explicit cascade edit', async () => {
    const source = subgraphLifecycleSource()
    source.graphs.push({
      id: 'wrapper',
      name: '包装',
      kind: 'subgraph',
      nodes: [],
      calls: [
        {
          id: 'nested-child',
          graphId: 'child',
          position: { x: 0, y: 0 },
          bindings: {},
        },
      ],
      edges: [],
      inputs: [],
      outputs: [],
      entries: [],
      exits: [],
      annotations: [],
    })
    const transport = mockTransport(sourceView(source), runView('QUEUED'))
    const session = new EditorSession(transport)
    await session.load(source.workflow.id)

    session.removeGraphCascade('child')
    expect(session.source?.graphs.map((graph) => graph.id)).toEqual(['main', 'wrapper'])
    expect(session.source?.graphs[0]!.calls).toEqual([])
    expect(session.source?.graphs[1]!.calls).toEqual([])

    session.undo()
    expect(session.source?.graphs.map((graph) => graph.id)).toEqual(['main', 'child', 'wrapper'])
    session.redo()
    await session.save()
    expect(vi.mocked(transport.applyPatch).mock.calls[0]![2]).toEqual([
      {
        kind: 'remove-graph-call',
        removeGraphCall: { graphId: 'main', callId: 'call-child' },
      },
      {
        kind: 'remove-graph-call',
        removeGraphCall: { graphId: 'wrapper', callId: 'nested-child' },
      },
      { kind: 'remove-graph', removeGraph: { graphId: 'child' } },
    ])
  })

  it('binds visible subgraph boundaries back into the canonical graph interface', async () => {
    const source = emptySource()
    const duration = delay.dataInputs.find((port) => port.id === 'duration-milliseconds')!
    source.graphs.push({
      id: 'child',
      name: 'Child',
      kind: 'subgraph',
      nodes: [
        {
          id: 'wait',
          nodeRef: delay.nodeRef,
          position: { x: 0, y: 0 },
          config: {},
          bindings: {},
        },
      ],
      edges: [],
      inputs: [
        {
          id: 'duration',
          type: duration.type.expression,
          nodeId: 'wait',
          portId: 'duration-milliseconds',
        },
      ],
      outputs: [],
      entries: [],
      exits: [{ id: 'done', channel: 'exec', endpoint: { nodeId: 'wait', portId: 'done' } }],
    })
    const session = new EditorSession(mockTransport(sourceView(source), runView('QUEUED')))
    await session.load(source.workflow.id)
    session.enterGraph('child')

    expect(
      session.graphBoundaryCompatibility({
        kind: 'input',
        boundaryId: 'duration',
        endpoint: { nodeId: 'wait', portId: 'duration-milliseconds' },
      }),
    ).toMatchObject({ valid: true })
    session.bindGraphBoundary({
      kind: 'entry',
      endpoint: { nodeId: 'wait', portId: 'in' },
    })
    expect(session.currentGraph?.entries).toEqual([{ nodeId: 'wait', portId: 'in' }])

    session.unbindGraphBoundary({ kind: 'entry' })
    expect(session.currentGraph?.entries).toEqual([])
    session.unbindGraphBoundary({ kind: 'input', boundaryId: 'duration' })
    expect(session.currentGraph?.inputs).toEqual([])
  })

  it('rejects automatic subgraph inference when it finds multiple execution entries', async () => {
    const source = emptySource()
    source.graphs.push({
      id: 'child',
      name: 'Child',
      kind: 'subgraph',
      nodes: ['first', 'second'].map((id, index) => ({
        id,
        nodeRef: delay.nodeRef,
        position: { x: index * 240, y: 0 },
        config: {},
        bindings: {},
      })),
      edges: [],
      inputs: [],
      outputs: [],
      entries: [],
      exits: [],
    })
    const session = new EditorSession(mockTransport(sourceView(source), runView('QUEUED')))
    await session.load(source.workflow.id)
    session.enterGraph('child')

    expect(() => session.inferCurrentGraphInterface()).toThrow(
      'subgraph has multiple unconnected execution entries',
    )
  })

  it('owns typed state declarations and prevents deleting referenced slots', async () => {
    const source = emptySource()
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => 'read',
    )
    await session.load(source.workflow.id)
    const stringType = authoring.body.types.find((type) =>
      type.typeRef.typeId.includes('/core/string/'),
    )!

    session.apply({
      kind: 'add-state-variable',
      name: 'message',
      type: { kind: 'ref', ref: stringType.typeRef },
      defaultValue: 'initial',
    })
    expect(session.source?.variables).toEqual([
      { name: 'message', type: { kind: 'ref', ref: stringType.typeRef }, default: 'initial' },
    ])
    expect(() =>
      session.apply({
        kind: 'add-state-variable',
        name: 'message',
        type: { kind: 'ref', ref: stringType.typeRef },
        defaultValue: '',
      }),
    ).toThrow('duplicate state variable')

    session.apply({
      kind: 'add-node',
      nodeTypeId: stateRead.nodeRef.nodeTypeId,
      position: { x: 0, y: 0 },
    })
    session.apply({ kind: 'set-config', nodeId: 'read', fieldId: 'variable', value: 'message' })
    expect(
      session.nodeInstanceProjection(session.currentGraph!.nodes[0]!)?.dataOutputs[0]?.type
        .expression,
    ).toEqual({
      kind: 'ref',
      ref: stringType.typeRef,
    })
    expect(() => session.apply({ kind: 'remove-state-variable', name: 'message' })).toThrow(
      'still referenced',
    )
    session.apply({ kind: 'remove-node', nodeId: 'read' })
    session.apply({ kind: 'remove-state-variable', name: 'message' })
    expect(session.source?.variables).toEqual([])
  })

  it('creates a typed state read atomically and connects Integer to Number consumers', async () => {
    const source = emptySource()
    const ids = ['read', 'greater', 'to-string', 'last-change', 'increment']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    const integer = authoring.body.types.find((type) =>
      type.typeRef.typeId.includes('/core/integer/'),
    )!
    session.apply({
      kind: 'add-state-variable',
      name: 'index',
      type: { kind: 'ref', ref: integer.typeRef },
      defaultValue: 0,
    })
    expect(session.insertStateReference('index', 'read', { x: 0, y: 0 })).toBe('read')
    session.apply({
      kind: 'add-node',
      nodeTypeId: greater.nodeRef.nodeTypeId,
      position: { x: 200, y: 0 },
    })
    expect(() =>
      session.apply({
        kind: 'connect',
        edge: {
          channel: 'data',
          from: { nodeId: 'read', portId: 'result' },
          to: { nodeId: 'greater', portId: 'a' },
        },
      }),
    ).not.toThrow()
    session.apply({
      kind: 'add-node',
      nodeTypeId: toString.nodeRef.nodeTypeId,
      position: { x: 200, y: 100 },
    })

    expect(session.insertStateReference('index', 'last-change', { x: 0, y: 200 })).toBe(
      'last-change',
    )
    expect(session.insertStateReference('index', 'increment', { x: 0, y: 300 })).toBe('increment')
    expect(
      session.currentGraph?.nodes.find((candidate) => candidate.id === 'increment')?.config,
    ).toEqual({ variable: 'index' })
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'data',
        from: { nodeId: 'read', portId: 'result' },
        to: { nodeId: 'to-string', portId: 'value' },
      },
    })
    expect(
      session.nodeInstanceProjection(session.currentGraph!.nodes[2]!)?.dataInputs[0]?.type
        .expression,
    ).toEqual({ kind: 'ref', ref: integer.typeRef })
    const number = authoring.body.types.find((type) =>
      type.typeRef.typeId.includes('/core/number/'),
    )!
    expect(
      session.stateTypeChangeImpact('index', { kind: 'ref', ref: number.typeRef }),
    ).toMatchObject({
      references: [
        { nodeId: 'read', mode: 'read' },
        { nodeId: 'last-change', mode: 'read' },
        { nodeId: 'increment', mode: 'write' },
      ],
      issues: [],
    })
    expect(() =>
      session.apply({
        kind: 'update-state-variable',
        name: 'index',
        type: { kind: 'ref', ref: number.typeRef },
        defaultValue: 0,
      }),
    ).not.toThrow()
  })

  it('connects a key-chord state read directly to Press Keys', async () => {
    const source = emptySource()
    const ids = ['read-keys', 'press-keys']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    const keyCode = authoring.body.types.find((type) =>
      type.typeRef.typeId.endsWith('/automation/key-code/v1'),
    )!
    session.apply({
      kind: 'add-state-variable',
      name: 'shortcut',
      type: {
        kind: 'list',
        element: { kind: 'ref', ref: keyCode.typeRef },
      },
      defaultValue: ['CTRL', 'K'],
    })
    session.insertStateReference('shortcut', 'read', { x: 0, y: 0 })
    session.apply({
      kind: 'add-node',
      nodeTypeId: pressKeys.nodeRef.nodeTypeId,
      position: { x: 200, y: 0 },
    })

    expect(() =>
      session.apply({
        kind: 'connect',
        edge: {
          channel: 'data',
          from: { nodeId: 'read-keys', portId: 'result' },
          to: { nodeId: 'press-keys', portId: 'keys' },
        },
      }),
    ).not.toThrow()
  })

  it('previews every referenced state edge and blocks implicit conversion migrations', async () => {
    const source = emptySource()
    const ids = ['read', 'concat']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    const string = authoring.body.types.find((type) =>
      type.typeRef.typeId.includes('/core/string/'),
    )!
    const integer = authoring.body.types.find((type) =>
      type.typeRef.typeId.includes('/core/integer/'),
    )!
    session.apply({
      kind: 'add-state-variable',
      name: 'message',
      type: { kind: 'ref', ref: string.typeRef },
      defaultValue: '',
    })
    session.insertStateReference('message', 'read', { x: 0, y: 0 })
    session.apply({
      kind: 'add-node',
      nodeTypeId: concat.nodeRef.nodeTypeId,
      position: { x: 200, y: 0 },
    })
    session.apply({ kind: 'bind-value', nodeId: 'concat', portId: 'b', value: '' })
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'data',
        from: { nodeId: 'read', portId: 'result' },
        to: { nodeId: 'concat', portId: 'a' },
      },
    })

    const impact = session.stateTypeChangeImpact('message', {
      kind: 'ref',
      ref: integer.typeRef,
    })
    expect(impact.references).toEqual([{ graphId: 'main', nodeId: 'read', mode: 'read' }])
    expect(impact.issues).toEqual([
      expect.objectContaining({
        graphId: 'main',
        disposition: 'conversion',
        conversions: expect.arrayContaining([
          expect.objectContaining({ nodeTypeId: toString.nodeRef.nodeTypeId }),
        ]),
      }),
    ])
    expect(
      session.stateTypeChangeImpact('message', { kind: 'ref', ref: string.typeRef }).issues,
    ).toEqual([])
  })

  it('propagates instance types through a chain of generic nodes', async () => {
    const source = emptySource()
    const ids = ['read', 'select', 'to-string']
    const session = new EditorSession(
      mockTransport(sourceView(source), runView('QUEUED')),
      () => ids.shift() ?? 'unused',
    )
    await session.load(source.workflow.id)
    const integer = authoring.body.types.find((type) =>
      type.typeRef.typeId.includes('/core/integer/'),
    )!
    session.apply({
      kind: 'add-state-variable',
      name: 'index',
      type: { kind: 'ref', ref: integer.typeRef },
      defaultValue: 0,
    })
    session.insertStateReference('index', 'read', { x: 0, y: 0 })
    session.apply({
      kind: 'add-node',
      nodeTypeId: select.nodeRef.nodeTypeId,
      position: { x: 200, y: 0 },
    })
    session.apply({
      kind: 'add-node',
      nodeTypeId: toString.nodeRef.nodeTypeId,
      position: { x: 400, y: 0 },
    })
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'data',
        from: { nodeId: 'read', portId: 'result' },
        to: { nodeId: 'select', portId: 'when_true' },
      },
    })
    session.apply({
      kind: 'connect',
      edge: {
        channel: 'data',
        from: { nodeId: 'select', portId: 'result' },
        to: { nodeId: 'to-string', portId: 'value' },
      },
    })
    expect(
      session.nodeInstanceProjection(session.currentGraph!.nodes[2]!)?.dataInputs[0]?.type
        .expression,
    ).toEqual({ kind: 'ref', ref: integer.typeRef })
  })
})

function subgraphLifecycleSource(): YottaWorkflowSource {
  const source = emptySource()
  const duration = delay.dataInputs.find((port) => port.id === 'duration-milliseconds')!
  source.graphs[0]!.calls = [
    {
      id: 'call-child',
      graphId: 'child',
      label: '等待',
      position: { x: 120, y: 80 },
      bindings: { 'duration-milliseconds': { kind: 'value', value: 250 } },
    },
  ]
  const child: Graph = {
    id: 'child',
    name: '等待',
    kind: 'subgraph',
    nodes: [
      {
        id: 'wait',
        nodeRef: delay.nodeRef,
        position: { x: 0, y: 0 },
        config: {},
        bindings: {},
      },
    ],
    calls: [],
    edges: [],
    inputs: [
      {
        id: 'duration-milliseconds',
        name: '等待时长',
        type: duration.type.expression,
        nodeId: 'wait',
        portId: 'duration-milliseconds',
      },
    ],
    outputs: [],
    entries: [{ nodeId: 'wait', portId: 'in' }],
    exits: [
      {
        id: 'done',
        name: '完成',
        channel: 'exec',
        endpoint: { nodeId: 'wait', portId: 'done' },
      },
    ],
    annotations: [],
  }
  source.graphs.push(child)
  return source
}

function emptySource(): YottaWorkflowSource {
  return {
    format: 'yotta.workflow',
    version: '5',
    workflow: { id: 'workflow_test', name: 'Test workflow' },
    revision: 0,
    entryGraph: 'main',
    graphs: [{ id: 'main', kind: 'main', nodes: [], edges: [], inputs: [], outputs: [] }],
    variables: [],
    resources: [],
    targetProfileDefinitions: [],
    credentialRequirements: [],
    dependencies: [],
  }
}

function sourceView(source: YottaWorkflowSource): SourceView {
  return {
    workflowId: source.workflow.id,
    name: source.workflow.name,
    revision: source.revision,
    sourceHash: 'sha256:source',
    sourceJson: JSON.stringify(source),
  } as SourceView
}

function runView(status: string): RunView {
  return {
    runId: '01980a13-0000-7000-8000-000000000001',
    status,
    generation: 0,
    recordDigest: 'sha256:record',
    programHash: 'sha256:program',
    queuedAt: '2026-07-15T00:00:00Z',
    timeline: [],
    timelinePage: 1,
    timelinePages: 1,
    timelineTotal: 0,
  } as RunView
}

function mockTransport(saved: SourceView, run: RunView): WorkflowTransport {
  return {
    listSources: vi.fn(async () => [saved]),
    querySources: vi.fn(async () => ({
      items: [saved],
      total: 1,
      page: 1,
      pageSize: 20,
      categories: [],
      tags: [],
    })),
    listSourceRecoveries: vi.fn(async () => []),
    repairSourceRecovery: vi.fn(async () => saved),
    deleteSourceRecovery: vi.fn(async () => undefined),
    previewDeleteSources: vi.fn(async () => []),
    deleteSources: vi.fn(async () => []),
    createSource: vi.fn(async () => saved),
    createSourceWithMetadata: vi.fn(async () => saved),
    updateSourceMetadata: vi.fn(async () => saved),
    batchUpdateSourceMetadata: vi.fn(async () => []),
    chooseSourceBundle: vi.fn(async () => ''),
    chooseSourceBundleDestination: vi.fn(async () => ''),
    chooseSourceBundleDirectory: vi.fn(async () => ''),
    inspectSourceBundle: vi.fn(async () => ({
      panels: [],
      workflowId: saved.workflowId,
      name: saved.name,
      revision: saved.revision,
      sourceHash: saved.sourceHash,
      publishedSourceHash: saved.sourceHash,
      blobCount: 0,
      blobBytes: 0,
    })),
    previewSourceBundle: vi.fn(async () => ({
      panels: [],
      workflowId: saved.workflowId,
      name: saved.name,
      revision: saved.revision,
      sourceHash: saved.sourceHash,
      publishedSourceHash: saved.sourceHash,
      blobCount: 0,
      blobBytes: 0,
    })),
    importSourceBundle: vi.fn(async () => saved),
    replaceSourceFromBundle: vi.fn(async () => saved),
    exportSourceBundle: vi.fn(async () => ({
      workflowId: saved.workflowId,
      exported: true,
    })),
    exportSourceBundles: vi.fn(async () => []),
    getSource: vi.fn(async () => saved),
    applyPatch: vi.fn(async (_workflowId: string, baseRevision: number) => {
      if (!saved.sourceJson) throw new Error('mock source omitted sourceJson')
      const source = JSON.parse(saved.sourceJson) as YottaWorkflowSource
      source.revision = baseRevision + 1
      return { source: sourceView(source), generatedNodes: [] }
    }),
    checkDraft: vi.fn(
      async () =>
        ({
          sourceHash: 'sha256:source-next',
          programHash: 'sha256:program',
          diagnostics: [],
        }) as CompileView,
    ),
    startRun: vi.fn(
      async () =>
        ({
          sourceHash: 'sha256:source-next',
          programHash: 'sha256:program',
          diagnostics: [],
          run,
          readiness: { state: 'started' },
        }) as StartRunView,
    ),
    startDebugRun: vi.fn(
      async () =>
        ({
          sourceHash: 'sha256:source-next',
          programHash: 'sha256:program',
          diagnostics: [],
          run,
          debug: null,
          readiness: { state: 'started' },
        }) as StartRunView,
    ),
    getDebugSnapshot: vi.fn(async () => ({ status: 'paused', generation: 1 }) as never),
    controlDebugRun: vi.fn(async () => ({ status: 'paused', generation: 2 }) as never),
    setDebugBreakpoints: vi.fn(async () => ({ status: 'paused', generation: 2 }) as never),
    getActiveSourceRuns: vi.fn(async () => ({})),
    cancelRun: vi.fn(async () => runView('CANCELLED')),
    cancelAllRuns: vi.fn(async () => undefined),
    getRunTimeline: vi.fn(async () => run),
    getRunTimelinePage: vi.fn(async () => run),
    chooseRunTimelineDestination: vi.fn(async () => ''),
    exportRunTimeline: vi.fn(async () => ({ path: '', entries: run.timelineTotal })),
    getAuthoringProjection: vi.fn(async () => JSON.stringify(authoring)),
    searchRegistry: vi.fn(async () => ({
      items: [],
      facets: { categories: [], tags: [], systemFacets: [] },
    })),
    publishSourceToRegistry: vi.fn(async () => ({}) as never),
    installRegistryWorkflow: vi.fn(async () => saved),
  }
}

describe('cross-workflow insertion targets', () => {
  it('groups foreign target IDs into declarations atomically and keeps compatible IDs', async () => {
    const source = emptySource()
    source.targets = [{ id: 'destination', name: 'Destination', kind: 'automation', default: true }]
    source.targetDefaults = [{ target: 'target', slot: 'destination' }]
    const session = new EditorSession(mockTransport(sourceView(source), runView('QUEUED')))
    await session.load(source.workflow.id)
    const before = JSON.stringify(session.source)
    const nodes = ['foreign-a', 'foreign-a', 'foreign-b', 'destination'].map((slot, index) => ({
      id: `copy-${index}`,
      nodeRef: clickPointer.nodeRef,
      position: { x: index * 30, y: 0 },
      config: { slot },
      bindings: {},
    }))
    const inserted = session.insertNodeSelection({ nodes, edges: [] }, { x: 100, y: 100 })
    const roles = inserted.map(
      (id) => session.currentGraph!.nodes.find((node) => node.id === id)!.config.slot,
    )
    expect(roles[0]).toBe(roles[1])
    expect(roles[2]).not.toBe(roles[0])
    expect(roles[3]).toBe('destination')
    expect(session.source!.targets).toHaveLength(3)
    expect(
      roles.slice(0, 3).every((id) => session.source!.targets!.some((target) => target.id === id)),
    ).toBe(true)
    expect(JSON.stringify(session.source)).not.toContain('foreign-a')
    expect(JSON.stringify(nodes)).toContain('foreign-a')
    session.undo()
    const restored = JSON.parse(before)
    restored.revision = session.source!.revision
    expect(session.source).toEqual(restored)
    session.redo()
    expect(session.source!.targets).toHaveLength(3)
  })
  it('leaves declarations and nodes untouched when pasted topology is invalid', async () => {
    const source = emptySource()
    source.targets = [{ id: 'destination', name: 'Destination', kind: 'automation', default: true }]
    const session = new EditorSession(mockTransport(sourceView(source), runView('QUEUED')))
    await session.load(source.workflow.id)
    const before = JSON.stringify(session.source)
    const nodes = ['one', 'two'].map((id) => ({
      id,
      nodeRef: clickPointer.nodeRef,
      position: { x: 0, y: 0 },
      config: { slot: 'physical-or-foreign' },
      bindings: {},
    }))
    expect(() =>
      session.insertNodeSelection(
        {
          nodes,
          edges: [
            {
              channel: 'exec',
              from: { nodeId: 'one', portId: 'missing' },
              to: { nodeId: 'two', portId: 'missing' },
            },
          ],
        },
        { x: 0, y: 0 },
      ),
    ).toThrow()
    expect(JSON.stringify(session.source)).toBe(before)
  })
})
