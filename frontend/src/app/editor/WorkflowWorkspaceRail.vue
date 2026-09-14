<template>
  <nav
    class="flex w-11 shrink-0 flex-col items-center gap-1 border-r border-default bg-elevated/20 py-2"
    :aria-label="t('workflow.workspace_tools')"
  >
    <UTooltip
      v-for="item in workspaceItems"
      :key="item.panel"
      :text="item.label"
      :content="{ side: 'right' }"
    >
      <UButton
        :data-testid="item.testId"
        :icon="item.icon"
        color="neutral"
        :variant="open && activePanel === item.panel ? 'soft' : 'ghost'"
        size="sm"
        :aria-label="item.label"
        :aria-pressed="open && activePanel === item.panel"
        @click="emit('select', item.panel)"
      />
    </UTooltip>
  </nav>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WorkflowWorkspacePanel } from './workspacePanel'

defineProps<{
  activePanel: WorkflowWorkspacePanel
  open: boolean
}>()
const emit = defineEmits<{
  select: [panel: WorkflowWorkspacePanel]
}>()
const { t } = useI18n()
const workspaceItems = computed<
  Array<{
    panel: WorkflowWorkspacePanel
    label: string
    icon: string
    testId: string
  }>
>(() => [
  workspaceItem('settings', 'workflow.editor.settings', 'i-tabler-settings'),
  workspaceItem('graphs', 'workflow.graphs.manager', 'i-tabler-folders'),
  workspaceItem('variables', 'workflow.state_panel.title', 'i-tabler-variable'),
  workspaceItem('macro', 'assets.tabs.macros', 'i-tabler-list-details'),
  workspaceItem('clip', 'assets.tabs.clips', 'i-tabler-route-alt-left'),
  workspaceItem('template', 'assets.tabs.templates', 'i-tabler-photo'),
  workspaceItem('path', 'paths.title', 'i-tabler-map-route'),
  workspaceItem('snippets', 'workflow.snippets.title', 'i-tabler-bookmarks'),
])

function workspaceItem(panel: WorkflowWorkspacePanel, key: string, icon: string) {
  return {
    panel,
    label: t(key),
    icon,
    testId: `workflow-workspace-${panel}`,
  }
}
</script>
