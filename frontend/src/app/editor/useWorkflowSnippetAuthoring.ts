import { computed, ref, type Ref } from 'vue'
import type { ConfirmOpts } from '@/composables/useConfirm'
import type { WorkflowSnippet } from '@/lib/backend'
import type { useSnippetsStore } from '@/stores/snippets'
import type { EditorSession, Node, NodeProjection } from './EditorSession'

interface WorkflowSnippetAuthoringOptions {
  session: EditorSession
  snippets: ReturnType<typeof useSnippetsStore>
  canvasElement: Ref<HTMLElement | null>
  screenToFlowCoordinate: (position: { x: number; y: number }) => { x: number; y: number }
  selectInsertedNodes: (nodeIds: string[]) => Promise<void>
  showSnippetPanel: () => void
  showTargetSetup?: () => void
  projectionTitle: (projection: NodeProjection) => string
  confirm: (options: ConfirmOpts) => Promise<boolean | string>
  translate: (key: string, params?: Record<string, unknown>) => string
  showError: (title: string, error: unknown) => void
}

export interface WorkflowSnippetMetadata {
  name: string
  description: string
  category: string
  tags: string[]
  shortcut: string
}

export function useWorkflowSnippetAuthoring(options: WorkflowSnippetAuthoringOptions) {
  const modalOpen = ref(false)
  const saveBusy = ref(false)
  const draft = ref<WorkflowSnippet | null>(null)
  const modalInitial = computed(() =>
    draft.value
      ? {
          name: draft.value.name,
          description: draft.value.description,
          category: draft.value.category,
          tags: draft.value.tags,
          shortcut: draft.value.shortcut,
        }
      : undefined,
  )

  function openForNode(node: Node): void {
    const projection = options.session.nodeProjection(node.nodeRef.nodeTypeId)
    draft.value = {
      schemaVersion: '1',
      id: '',
      name:
        node.label || (projection ? options.projectionTitle(projection) : node.nodeRef.nodeTypeId),
      description: '',
      category: projection?.category ?? '',
      tags: [],
      createdAt: new Date(0).toISOString(),
      updatedAt: new Date(0).toISOString(),
      usageCount: 0,
      payload: {
        nodeRef: copy(node.nodeRef),
        label: node.label,
        config: copy(node.config),
        bindings: copy(node.bindings),
        disabled: node.disabled,
      },
    }
    modalOpen.value = true
  }

  async function edit(id: string): Promise<void> {
    try {
      draft.value = await options.snippets.get(id)
      modalOpen.value = true
    } catch (error) {
      options.showError(options.translate('workflow.snippets.load_failed'), error)
    }
  }

  async function save(metadata: WorkflowSnippetMetadata): Promise<void> {
    if (!draft.value || saveBusy.value) return
    saveBusy.value = true
    try {
      await options.snippets.save({ ...copy(draft.value), ...metadata })
      modalOpen.value = false
      options.showSnippetPanel()
    } catch (error) {
      options.showError(options.translate('workflow.snippets.save_failed'), error)
    } finally {
      saveBusy.value = false
    }
  }

  async function remove(id: string): Promise<void> {
    const item = options.snippets.items.find((candidate) => candidate.id === id)
    const accepted = await options.confirm({
      title: options.translate('workflow.snippets.delete_title'),
      description: options.translate('workflow.snippets.delete_hint', { name: item?.name ?? id }),
      confirmText: options.translate('workflow.snippets.delete'),
      cancelText: options.translate('common.cancel'),
      color: 'error',
    })
    if (!accepted) return
    try {
      await options.snippets.remove(id)
    } catch (error) {
      options.showError(options.translate('workflow.snippets.delete_failed'), error)
    }
  }

  async function use(id: string, position?: { x: number; y: number }): Promise<void> {
    try {
      const snippet = await options.snippets.get(id)
      const current = options.session.nodeProjection(snippet.payload.nodeRef.nodeTypeId)
      if (!current) throw new Error(options.translate('workflow.snippets.node_unavailable'))
      if (
        current.nodeRef.semanticDigest !== snippet.payload.nodeRef.semanticDigest ||
        current.nodeRef.version !== snippet.payload.nodeRef.version
      ) {
        throw new Error(options.translate('workflow.snippets.contract_changed'))
      }
      const rect = options.canvasElement.value?.getBoundingClientRect()
      const origin =
        position ??
        (rect
          ? options.screenToFlowCoordinate({
              x: rect.left + rect.width / 2,
              y: rect.top + rect.height / 2,
            })
          : { x: 160, y: 160 })
      const targetCount = options.session.source?.targets?.length ?? 0
      const [nodeID] = options.session.insertNodeSelection(
        {
          nodes: [
            {
              id: 'snippet-template',
              nodeRef: copy(snippet.payload.nodeRef),
              label: snippet.payload.label,
              position: { x: 0, y: 0 },
              config: copy(snippet.payload.config),
              bindings: copy(snippet.payload.bindings) as Node['bindings'],
              disabled: snippet.payload.disabled,
            },
          ],
          edges: [],
        },
        origin,
      )
      if (!nodeID) return
      await options.selectInsertedNodes([nodeID])
      if ((options.session.source?.targets?.length ?? 0) > targetCount) options.showTargetSetup?.()
      try {
        await options.snippets.markUsed(id)
      } catch (error) {
        options.showError(options.translate('workflow.snippets.usage_failed'), error)
      }
    } catch (error) {
      options.showError(options.translate('workflow.snippets.insert_failed'), error)
    }
  }

  return { modalOpen, saveBusy, draft, modalInitial, openForNode, edit, save, remove, use }
}

function copy<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}
