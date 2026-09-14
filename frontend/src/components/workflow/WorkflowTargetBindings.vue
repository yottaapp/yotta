<template>
  <div class="space-y-1" data-testid="workflow-target-bindings">
    <p class="text-xs leading-5 text-muted">{{ t('workflow.settings_panel.local_hint') }}</p>
    <div
      v-for="target in targets"
      :key="target.id"
      :class="
        compact
          ? 'flex flex-wrap items-center gap-x-3 gap-y-1 py-1'
          : 'flex flex-wrap items-center justify-between gap-x-6 gap-y-2 py-3'
      "
    >
      <div class="min-w-36 flex-1">
        <label :for="`target-${instanceId}-${target.id}`" class="text-sm font-medium">{{
          target.name
        }}</label>
        <UBadge v-if="target.default" class="ml-2" size="sm" variant="soft">{{
          t('workflow.settings_panel.default')
        }}</UBadge>
        <p v-if="target.description" class="mt-1 whitespace-pre-wrap text-xs text-muted">
          {{ target.description }}
        </p>
      </div>
      <div
        :class="
          compact
            ? 'flex w-full items-center gap-1 sm:w-56'
            : 'flex w-full items-center gap-1 sm:w-[30%] sm:min-w-56 sm:max-w-96'
        "
      >
        <AdaptiveSelect
          :id="`target-${instanceId}-${target.id}`"
          :model-value="String(values[targetValueKey(target.id)] ?? '')"
          :items="options(target)"
          :placeholder="t('workflow.settings_panel.unbound')"
          :disabled="disabled"
          class="min-w-0 flex-1"
          @update:model-value="emit('change', targetValueKey(target.id), String($event ?? ''))"
        />
        <UButton
          v-if="values[targetValueKey(target.id)]"
          color="neutral"
          variant="ghost"
          icon="i-tabler-x"
          :disabled="disabled"
          :aria-label="t('workflow.settings_panel.clear_binding')"
          @click="emit('change', targetValueKey(target.id), '')"
        />
      </div>
      <div
        :class="
          compact
            ? 'flex items-center justify-end gap-2'
            : 'flex w-full items-center justify-end gap-2'
        "
      >
        <p v-if="!options(target).length" class="text-xs text-muted">
          {{ t('workflow.settings_panel.no_local_target') }}
        </p>
        <UButton
          size="xs"
          color="neutral"
          variant="ghost"
          icon="i-tabler-settings"
          :disabled="disabled"
          :label="t('workflow.settings_panel.configure_local')"
          @click="
            configure = target.kind === 'configured-application' ? 'application' : 'automation'
          "
        />
      </div>
    </div>
    <BaseModal
      :open="configure !== null"
      :title="t('workflow.settings_panel.configure_local')"
      size="xl"
      @update:open="!$event && (configure = null)"
    >
      <ApplicationSettings v-if="configure === 'application'" />
      <AutomationSettings v-else-if="configure === 'automation'" />
    </BaseModal>
  </div>
</template>
<script setup lang="ts">
import { useId, ref, defineAsyncComponent } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSettingsStore } from '@/stores/settings'
import type { WorkflowTarget } from '../../../../contracts/workflow/current/workflow-source'
import { targetValueKey } from '@/app/editor/workflowTargets'
import BaseModal from '@/components/common/BaseModal.vue'
import AdaptiveSelect from '@/components/common/AdaptiveSelect.vue'
defineProps<{
  targets: WorkflowTarget[]
  values: Record<string, unknown>
  disabled?: boolean
  compact?: boolean
}>()
const emit = defineEmits<{ change: [key: string, value: string] }>()
const { t } = useI18n()
const settings = useSettingsStore()
const instanceId = useId()
const configure = ref<'automation' | 'application' | null>(null)
const AutomationSettings = defineAsyncComponent(() => import('@/views/SettingsAutomation.vue'))
const ApplicationSettings = defineAsyncComponent(() => import('@/views/SettingsApplications.vue'))
function options(target: WorkflowTarget) {
  const choices =
    target.kind === 'configured-application'
      ? (settings.data?.applications.profiles ?? [])
      : (settings.data?.automation.targets ?? []).filter(
          (candidate) => target.kind === 'automation' || candidate.targetKind === target.kind,
        )
  return choices.map((candidate) => ({ label: candidate.label, value: candidate.slot }))
}
</script>

<style src="../../views/SettingsView.css"></style>
