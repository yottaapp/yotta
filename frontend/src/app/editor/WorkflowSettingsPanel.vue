<template>
  <section class="min-h-0 flex-1 overflow-y-auto" data-testid="workflow-settings-panel">
    <header class="border-b border-default px-4 py-3">
      <h2 class="text-sm font-semibold">{{ t('workflow.editor.settings') }}</h2>
    </header>
    <details open class="border-b border-default">
      <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
        {{ t('workflow.settings_panel.basic') }}
      </summary>
      <WorkflowMetadataDialog
        inline
        :open="true"
        class="px-4 pb-4"
        :workflow-id="workflowId"
        :name="workflowMetadata.name"
        :description="workflowMetadata.description"
        :category="workflowMetadata.category"
        :tags="workflowMetadata.tags"
        :busy="workflowSettingsBusy"
        :error="workflowSettingsError"
        @submit="emit('metadata', $event)"
        @draft="emit('metadata-draft', $event)"
      />
    </details>
    <details open class="border-b border-default">
      <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
        {{ t('workflow.settings_panel.targets') }}
      </summary>
      <WorkflowTargetPanel
        :targets="workflowTargets"
        :graphs="graphs"
        :values="targetBindingDraft"
        :dirty="targetBindingsDirty"
        :busy="targetBindingsBusy"
        :error="targetBindingsError"
        @command="emit('command', $event)"
        @binding="(key, value) => emit('binding', key, value)"
        @save-bindings="emit('save-bindings')"
      />
    </details>
    <details open>
      <summary class="cursor-pointer px-4 py-3 text-sm font-medium">
        {{ t('workflow.parameters.title') }}
      </summary>
      <WorkflowParameterPanel
        embedded
        :variables="variables"
        :blocks="blocks"
        :types="types"
        @command="emit('command', $event)"
      />
    </details>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import WorkflowMetadataDialog from './WorkflowMetadataDialog.vue'
import WorkflowTargetPanel from './WorkflowTargetPanel.vue'
import WorkflowParameterPanel from './WorkflowParameterPanel.vue'
import type { WorkflowMetadataDraft } from './EditorWorkflowMetadataController'
import type {
  WorkflowTarget,
  Graph,
  Variable,
  ParameterBlock,
} from '../../../../contracts/workflow/current/workflow-source'
import type { TypeProjection } from '../../../../contracts/node/current/authoring-projection'
import type { EditorCommand } from './EditorSession'
defineProps<{
  workflowId: string
  workflowMetadata: WorkflowMetadataDraft
  workflowSettingsBusy: boolean
  workflowSettingsError: string
  workflowTargets: WorkflowTarget[]
  graphs: Graph[]
  targetBindingDraft: Record<string, unknown>
  targetBindingsDirty: boolean
  targetBindingsBusy: boolean
  targetBindingsError: string
  variables: Variable[]
  blocks?: ParameterBlock[]
  types: TypeProjection[]
}>()
const emit = defineEmits<{
  metadata: [draft: WorkflowMetadataDraft]
  'metadata-draft': [draft: WorkflowMetadataDraft]
  command: [command: EditorCommand]
  binding: [key: string, value: string]
  'save-bindings': []
}>()
const { t } = useI18n()
</script>
