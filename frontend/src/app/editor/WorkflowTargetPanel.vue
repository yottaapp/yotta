<template>
  <div class="space-y-4 p-4" data-testid="workflow-target-panel">
    <UButton
      icon="i-tabler-plus"
      variant="soft"
      size="sm"
      :label="t('workflow.settings_panel.add')"
      :disabled="targets.length >= 64"
      @click="add"
    />
    <details
      v-for="target in targets"
      :key="target.id"
      class="group border-b border-default pb-3"
      open
    >
      <summary class="flex cursor-pointer items-center gap-2 py-2 text-sm font-medium">
        <UIcon name="i-tabler-chevron-right" class="group-open:rotate-90" />{{ target.name }}
        <UBadge v-if="target.default" size="sm" variant="soft">{{
          t('workflow.settings_panel.default')
        }}</UBadge>
      </summary>
      <div class="space-y-3 pt-2">
        <UFormField :label="t('workflow.settings_panel.name')"
          ><UInput
            class="w-full"
            :model-value="target.name"
            @change="
              update(target.id, {
                name: ($event.target as HTMLInputElement).value.trim() || target.name,
              })
            "
        /></UFormField>
        <UFormField :label="t('workflow.settings_panel.description')"
          ><UTextarea
            class="w-full"
            :model-value="target.description ?? ''"
            @change="
              update(target.id, { description: ($event.target as HTMLTextAreaElement).value })
            "
        /></UFormField>
        <UFormField :label="t('workflow.settings_panel.kind')"
          ><AdaptiveSelect
            class="w-full"
            :items="kinds"
            :model-value="target.kind"
            :disabled="used.has(target.id)"
            @update:model-value="update(target.id, { kind: String($event) })"
        /></UFormField>
        <div class="flex items-center gap-2">
          <UButton
            v-if="!target.default"
            size="xs"
            variant="ghost"
            :label="t('workflow.settings_panel.make_default')"
            @click="makeDefault(target.id)"
          />
          <UTooltip
            :text="t('workflow.settings_panel.referenced')"
            :disabled="!used.has(target.id)"
          >
            <UButton
              color="error"
              size="xs"
              variant="ghost"
              icon="i-tabler-trash"
              :label="t('common.delete')"
              :disabled="target.default || used.has(target.id)"
              @click="
                emit('command', {
                  kind: 'set-workflow-targets',
                  targets: targets.filter((item) => item.id !== target.id),
                })
              "
            />
          </UTooltip>
        </div>
      </div>
    </details>
    <USeparator :label="t('workflow.settings_panel.binding')" />
    <UAlert v-if="error" color="error" :description="error" />
    <WorkflowTargetBindings
      :targets="targets"
      :values="values"
      :disabled="busy"
      @change="(key, value) => emit('binding', key, value)"
    />
    <UButton
      :label="t('workflow.settings_panel.save_bindings')"
      :loading="busy"
      :disabled="!dirty"
      @click="emit('save-bindings')"
    />
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WorkflowTarget, Graph } from '../../../../contracts/workflow/current/workflow-source'
import type { EditorCommand } from './EditorSession'
import AdaptiveSelect from '@/components/common/AdaptiveSelect.vue'
import WorkflowTargetBindings from '@/components/workflow/WorkflowTargetBindings.vue'
const props = defineProps<{
  targets: WorkflowTarget[]
  graphs: Graph[]
  values: Record<string, unknown>
  busy: boolean
  dirty: boolean
  error: string
}>()
const emit = defineEmits<{
  command: [command: EditorCommand]
  binding: [key: string, value: string]
  'save-bindings': []
}>()
const { t } = useI18n()
const used = computed(
  () =>
    new Set(
      props.graphs.flatMap((graph) =>
        graph.nodes.flatMap((node) =>
          typeof node.config.slot === 'string' ? [node.config.slot] : [],
        ),
      ),
    ),
)
const kinds = computed(() => [
  { label: t('workflow.settings_panel.automation'), value: 'automation' },
  { label: t('workflow.settings_panel.application'), value: 'configured-application' },
  { label: t('settingsAutomation.targets.add_windows'), value: 'desktop-window' },
  { label: t('settingsAutomation.targets.add_android'), value: 'android-device' },
  { label: t('settingsAutomation.targets.add_browser'), value: 'browser-cdp' },
])
function update(id: string, patch: Partial<WorkflowTarget>) {
  emit('command', {
    kind: 'set-workflow-targets',
    targets: props.targets.map((target) => (target.id === id ? { ...target, ...patch } : target)),
  })
}
function makeDefault(id: string) {
  emit('command', {
    kind: 'set-workflow-targets',
    targets: props.targets.map((target) => ({ ...target, default: target.id === id })),
  })
}
function add() {
  emit('command', {
    kind: 'set-workflow-targets',
    targets: [
      ...props.targets,
      {
        id: `target-${crypto.randomUUID()}`,
        name: t('workflow.settings_panel.target_name', { number: props.targets.length + 1 }),
        kind: 'automation',
        default: !props.targets.length,
      },
    ],
  })
}
</script>
