import { describe, expect, it } from 'vitest'
import type {
  Graph,
  YottaWorkflowSource,
} from '../../../../contracts/workflow/current/workflow-source'
import {
  duplicateGraphCall,
  duplicateGraphDefinition,
  expandGraphCall,
  graphCallSites,
} from './subgraphLifecycle'

describe('subgraph lifecycle', () => {
  it('duplicates a call as another instance of the same definition', () => {
    const source = fixture()
    const main = source.graphs[0]
    const copy = duplicateGraphCall(main, 'call-child', 'call-child-copy')

    expect(copy).toMatchObject({
      id: 'call-child-copy',
      graphId: 'child',
      bindings: { text: { kind: 'value', value: 'hello' } },
      position: { x: 232, y: 132 },
    })
    expect(main.calls).toHaveLength(1)
  })

  it('duplicates a definition under a new graph identity without sharing mutable state', () => {
    const source = fixture()
    const copy = duplicateGraphDefinition(source, 'child', 'child-copy', '发送文本副本')
    copy.nodes[0]!.config.changed = true

    expect(copy).toMatchObject({ id: 'child-copy', name: '发送文本副本', kind: 'subgraph' })
    expect(copy.nodes[0]!.id).toBe('inner')
    expect(source.graphs[1]!.nodes[0]!.config.changed).toBeUndefined()
  })

  it('expands a call and reconnects bindings, data edges, and named signal exits', () => {
    const source = fixture()
    const ids = ['expanded-inner', 'expanded-note']
    const expansion = expandGraphCall(source, 'main', 'call-child', () => ids.shift() ?? '')

    expect(expansion.callId).toBe('call-child')
    expect(expansion.nodes).toEqual([
      expect.objectContaining({
        id: 'expanded-inner',
        position: { x: 200, y: 100 },
        bindings: { text: { kind: 'value', value: 'hello' } },
      }),
    ])
    expect(expansion.annotations[0]).toMatchObject({
      id: 'expanded-note',
      position: { x: 210, y: 110 },
    })
    expect(expansion.edges).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          channel: 'exec',
          from: { nodeId: 'start', portId: 'done' },
          to: { nodeId: 'expanded-inner', portId: 'in' },
        }),
        expect.objectContaining({
          channel: 'data',
          from: { nodeId: 'expanded-inner', portId: 'result' },
          to: { nodeId: 'sink', portId: 'value' },
        }),
        expect.objectContaining({
          channel: 'error',
          from: { nodeId: 'expanded-inner', portId: 'failed' },
          to: { nodeId: 'failed', portId: 'in' },
        }),
      ]),
    )
    expect(source.graphs[0]!.calls).toHaveLength(1)
    expect(source.graphs).toHaveLength(2)
  })

  it('lists every call site for an explicit cascade impact review', () => {
    const source = fixture()
    source.graphs.push({
      id: 'wrapper',
      name: '包装子图',
      kind: 'subgraph',
      nodes: [],
      calls: [
        {
          id: 'nested-call',
          graphId: 'child',
          position: { x: 0, y: 0 },
          bindings: {},
        },
      ],
      edges: [],
      inputs: [],
      outputs: [],
    })

    expect(graphCallSites(source, 'child')).toEqual([
      { parentGraphId: 'main', callId: 'call-child' },
      { parentGraphId: 'wrapper', callId: 'nested-call' },
    ])
  })
})

function fixture(): YottaWorkflowSource {
  const main: Graph = {
    id: 'main',
    name: '主图',
    kind: 'main',
    nodes: [],
    calls: [
      {
        id: 'call-child',
        graphId: 'child',
        label: '发送文本',
        position: { x: 200, y: 100 },
        bindings: { text: { kind: 'value', value: 'hello' } },
      },
    ],
    edges: [
      {
        channel: 'exec',
        from: { nodeId: 'start', portId: 'done' },
        to: { nodeId: 'call-child', portId: 'in' },
      },
      {
        channel: 'data',
        from: { nodeId: 'call-child', portId: 'result' },
        to: { nodeId: 'sink', portId: 'value' },
      },
      {
        channel: 'error',
        from: { nodeId: 'call-child', portId: 'failed' },
        to: { nodeId: 'failed', portId: 'in' },
      },
    ],
    inputs: [],
    outputs: [],
    annotations: [],
  }
  const child: Graph = {
    id: 'child',
    name: '发送文本',
    kind: 'subgraph',
    nodes: [
      {
        id: 'inner',
        nodeRef: {
          nodeTypeId: 'https://schemas.yotta.dev/nodes/test',
          version: '1.0.0',
          semanticDigest: `sha256:${'1'.repeat(64)}`,
        },
        position: { x: 20, y: 30 },
        config: {},
        bindings: {},
      },
    ],
    calls: [],
    edges: [],
    entries: [{ nodeId: 'inner', portId: 'in' }],
    inputs: [
      {
        id: 'text',
        name: '文本',
        type: {
          kind: 'ref',
          ref: {
            typeId: 'https://schemas.yotta.dev/types/core/string/v1',
            semanticDigest: `sha256:${'2'.repeat(64)}`,
          },
        },
        nodeId: 'inner',
        portId: 'text',
      },
    ],
    outputs: [
      {
        id: 'result',
        name: '结果',
        type: {
          kind: 'ref',
          ref: {
            typeId: 'https://schemas.yotta.dev/types/core/string/v1',
            semanticDigest: `sha256:${'2'.repeat(64)}`,
          },
        },
        nodeId: 'inner',
        portId: 'result',
      },
    ],
    exits: [
      {
        id: 'done',
        name: '完成',
        channel: 'exec',
        endpoint: { nodeId: 'inner', portId: 'done' },
      },
      {
        id: 'failed',
        name: '失败',
        channel: 'error',
        endpoint: { nodeId: 'inner', portId: 'failed' },
      },
    ],
    annotations: [
      {
        id: 'note',
        text: '说明',
        position: { x: 30, y: 40 },
        size: { width: 180, height: 80 },
      },
    ],
  }
  return {
    format: 'yotta.workflow',
    version: '5',
    workflow: { id: 'wf', name: 'Workflow' },
    revision: 0,
    entryGraph: 'main',
    graphs: [main, child],
    variables: [],
    resources: [],
    targetProfileDefinitions: [],
    credentialRequirements: [],
    dependencies: [],
  }
}
