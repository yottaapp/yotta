import { describe, expect, it } from 'vitest'
import { RPCError } from '@/lib/invoke'
import { describeWorkflowSaveError } from './workflowSaveError'

describe('workflow save errors', () => {
  it('turns a raw validation code into an actionable save message', () => {
    const failure = describeWorkflowSaveError({
      cause: { id: 'INVALID_FIELD', category: 'validation' },
    })

    expect(failure.kind).toBe('validation')
    expect(failure.message).toContain('节点参数或连线')
    expect(failure.message).not.toBe('INVALID_FIELD')
  })

  it('keeps an unknown internal code as supporting context instead of the whole message', () => {
    const failure = describeWorkflowSaveError(
      new RPCError({ id: 'PATCH_STORAGE_FAILED' }, 'workflow.applyPatch', 'op-1', null),
    )

    expect(failure.kind).toBe('unknown')
    expect(failure.message).toContain('保存失败')
    expect(failure.message).toContain('PATCH_STORAGE_FAILED')
  })

  it('identifies revision conflicts without relying on the rendered banner text', () => {
    const failure = describeWorkflowSaveError(
      new RPCError({ id: 'workflow.revision.conflict' }, 'workflow.applyPatch', 'op-1', null),
    )

    expect(failure.kind).toBe('revision')
    expect(failure.message).toContain('重新加载')
  })

  it('uses structured patch context to explain and locate the invalid node field', () => {
    const failure = describeWorkflowSaveError(
      new RPCError(
        {
          id: 'INVALID_CONFIG_VALUE',
          category: 'validation',
          params: { commandIndex: 0 },
        },
        'workflow.applyPatch',
        'op-1',
        null,
      ),
      [
        {
          kind: 'set-config',
          setConfig: {
            graphId: 'main',
            nodeId: 'delay',
            fieldId: 'duration-milliseconds',
            value: 5.3,
          },
        },
      ],
    )

    expect(failure.kind).toBe('validation')
    expect(failure.message).toContain('duration-milliseconds')
    expect(failure.message).toContain('定位')
    expect(failure.target).toEqual({
      graphId: 'main',
      nodeId: 'delay',
      fieldId: 'duration-milliseconds',
      portId: undefined,
    })
  })

  it('locates a pre-existing incompatible node from canonical diagnostic params', () => {
    const failure = describeWorkflowSaveError(
      new RPCError(
        {
          id: 'NODE_CONTRACT_MISMATCH',
          category: 'validation',
          params: {
            commandIndex: -1,
            graphPath: ['main'],
            nodeId: 'legacy-node',
            fieldPath: ['graphs', '0', 'nodes', '5', 'nodeRef'],
          },
        },
        'workflow.applyPatch',
        'op-1',
        null,
      ),
    )

    expect(failure.kind).toBe('validation')
    expect(failure.target).toEqual({
      graphId: 'main',
      nodeId: 'legacy-node',
      fieldId: 'nodeRef',
    })
  })
})

it.each(['INVALID_TARGET', 'REFERENCE_IN_USE'])(
  'projects %s to an actionable workflow settings error',
  (code) => {
    const failure = describeWorkflowSaveError(
      new RPCError(
        { id: code, category: 'validation' },
        'workflow.applyPatch',
        'target-operation',
        null,
      ),
    )
    expect(failure.kind).toBe('validation')
    expect(failure.message).toContain('工作流设置')
    expect(failure.message).not.toContain(code)
  },
)
