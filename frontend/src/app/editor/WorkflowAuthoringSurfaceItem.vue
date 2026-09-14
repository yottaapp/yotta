<template>
  <PanelNodeField
    v-if="
      item.kind === 'config' &&
      (item.field.editorAdapter === 'panel-reference' ||
        item.field.editorAdapter?.startsWith('panel-component-'))
    "
    :component="item.field.id === 'component'"
    :write="
      projection.nodeRef.nodeTypeId.includes('/write-') ||
      projection.nodeRef.nodeTypeId.endsWith('/log')
    "
    :kind="item.field.editorAdapter?.replace('panel-component-', '')"
    :panel="
      String(node.config.panel ?? targetDefaults.find((d) => d.target === 'panel')?.slot ?? '')
    "
    :value="
      String(
        item.field.id === 'panel'
          ? (node.config.panel ?? targetDefaults.find((d) => d.target === 'panel')?.slot ?? '')
          : (node.config.component ?? ''),
      )
    "
    :override="Object.prototype.hasOwnProperty.call(node.config, item.field.id)"
    :connected="connectedInputIds?.has(item.field.id === 'panel' ? 'panel-ref' : 'component-ref')"
    @change="
      emit('command', {
        kind: 'set-config',
        nodeId: node.id,
        fieldId: item.field.id,
        value: $event,
      })
    "
    @inherit="emit('command', { kind: 'clear-config', nodeId: node.id, fieldId: item.field.id })"
  />
  <div v-else-if="item.kind === 'config'" class="space-y-2">
    <PositionSourceField
      v-if="
        item.field.id === 'position-variable' &&
        projection.nodeRef.nodeTypeId.includes('/navigation/')
      "
      :node-id="node.id"
      :value="String(effectiveConfigValue ?? '')"
      :label="t(item.field.titleKey || 'paths.source_choose_variable')"
      @change="
        emit('command', {
          kind: 'set-config',
          nodeId: node.id,
          fieldId: item.field.id,
          value: $event,
        })
      "
    />
    <GeneratedFieldEditor
      v-else
      :field="item.field"
      :model-value="effectiveConfigValue"
      :state-variables="variables"
      :select-items="targetOptions"
      :select-placeholder="t('workflow.inspector.select_target')"
      @update:model-value="
        emit('command', {
          kind: 'set-config',
          nodeId: node.id,
          fieldId: item.field.id,
          value: $event,
        })
      "
    />
    <div v-if="targetCapability" class="flex items-center gap-2 text-[11px]">
      <UBadge
        :color="hasOverride ? 'warning' : inheritedTarget ? 'primary' : 'error'"
        variant="soft"
        size="sm"
      >
        {{
          t(
            hasOverride
              ? 'workflow.inspector.target_overridden'
              : inheritedTarget
                ? 'workflow.inspector.target_inherited'
                : 'workflow.inspector.target_missing',
          )
        }}
      </UBadge>
      <span v-if="inheritedTarget && !hasOverride" class="truncate text-muted">
        {{
          workflowTargets?.targets.value.find((target) => target.id === inheritedTarget)?.name ??
          inheritedTarget
        }}
      </span>
      <UButton
        v-if="hasOverride && inheritedTarget"
        class="ml-auto"
        color="neutral"
        variant="ghost"
        size="xs"
        :label="t('workflow.inspector.restore_inherited')"
        @click="emit('command', { kind: 'clear-config', nodeId: node.id, fieldId: item.field.id })"
      />
    </div>
    <div
      v-if="targetCapability && targetOptions?.length === 0"
      class="flex items-center gap-2 rounded-lg border border-warning/30 bg-warning/10 px-3 py-2"
    >
      <p class="min-w-0 flex-1 text-[11px] leading-5 text-warning">
        {{
          t(
            roleTarget
              ? 'workflow.settings_panel.missing'
              : 'workflow.inspector.no_installed_target',
          )
        }}
      </p>
      <UButton
        :to="
          roleTarget ? undefined : { path: '/settings', query: { section: targetSettingsSection } }
        "
        :label="t('workflow.inspector.configure_target')"
        @click="roleTarget && workflowTargets?.openSettings()"
        icon="i-tabler-settings"
        color="warning"
        variant="soft"
        size="xs"
      />
    </div>
  </div>

  <WorkflowInputBindingEditor
    v-else-if="item.kind === 'input'"
    :node="node"
    :port="item.port"
    :target-slot="targetSlot"
    :connected="connectedInputIds?.has(item.port.id)"
    :resources="resources"
    @command="emit('command', $event)"
    @capture-template="emit('capture-template')"
    @locate-resource="emit('locate-resource', $event)"
  />

  <div
    v-else
    class="flex items-center gap-2 rounded-lg border border-default bg-muted/20 px-3 py-2"
  >
    <span
      class="size-2 rounded-full"
      :style="{ backgroundColor: item.port.type.color || '#a1a1aa' }"
      aria-hidden="true"
    />
    <div class="min-w-0 flex-1">
      <p class="truncate text-xs font-medium text-toned">{{ portTitle }}</p>
      <p class="truncate text-[10px] text-dimmed">{{ typeTitle }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { WORKFLOW_TARGETS, isWorkflowTargetKind, targetAcceptsKinds } from './workflowTargets'
import PositionSourceField from './PositionSourceField.vue'
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  TargetDefault,
  WorkflowResource,
} from '../../../../contracts/workflow/current/workflow-source'
import type {
  CapabilityProjection,
  ConfiguredTargetProjection,
} from '../../../../contracts/node/current/authoring-projection'
import { projectionLabel } from '@/app/editor/projectionLabels'
import type { EditorCommand, Node, NodeProjection } from './EditorSession'
import type { AuthoringSurfaceItem } from './authoringSurface'
import PanelNodeField from './PanelNodeField.vue'
import GeneratedFieldEditor from './GeneratedFieldEditor.vue'
import WorkflowInputBindingEditor from './WorkflowInputBindingEditor.vue'
import { useSettingsStore } from '@/stores/settings'
import type { ResourceLocation } from './resourceLocator'

const props = defineProps<{
  item: AuthoringSurfaceItem
  node: Node
  projection: NodeProjection
  variables: string[]
  targetDefaults: TargetDefault[]
  targetSlot?: string
  connectedInputIds?: ReadonlySet<string>
  resources?: WorkflowResource[]
}>()
const emit = defineEmits<{
  command: [command: EditorCommand]
  'capture-template': []
  'locate-resource': [location: ResourceLocation]
}>()
const { t, te } = useI18n()
const workflowTargets = inject(WORKFLOW_TARGETS, undefined)
const roleTarget = computed(() =>
  Boolean(workflowTargets && isWorkflowTargetKind(targetCapability.value?.targetKinds ?? [])),
)
const settingsStore = useSettingsStore()
const configFieldID = computed(() => (props.item.kind === 'config' ? props.item.field.id : ''))
type TargetBinding = ConfiguredTargetProjection | CapabilityProjection
const targetCapability = computed<TargetBinding | undefined>(
  () =>
    (props.projection.configuredTargets ?? []).find(
      (candidate) => candidate.slotConfigKey === configFieldID.value,
    ) ??
    props.projection.capabilities.find(
      (candidate) => candidate.targetSlotConfigKey === configFieldID.value,
    ),
)
const inheritedTarget = computed(() => {
  const target = targetCapability.value?.targetSlot
  const slot =
    props.targetDefaults.find((candidate) => candidate.target === target)?.slot ??
    (target === 'application'
      ? props.targetDefaults.find((candidate) => candidate.target === 'target')?.slot
      : '') ??
    ''
  if (workflowTargets && isWorkflowTargetKind(targetCapability.value?.targetKinds ?? [])) {
    const role = workflowTargets.targets.value.find((candidate) => candidate.id === slot)
    return role && targetAcceptsKinds(role, targetCapability.value!.targetKinds) ? slot : ''
  }
  return slot
})
const hasOverride = computed(() =>
  configFieldID.value
    ? Object.prototype.hasOwnProperty.call(props.node.config, configFieldID.value)
    : false,
)
const effectiveConfigValue = computed(() => {
  if (!configFieldID.value) return undefined
  return hasOverride.value
    ? props.node.config[configFieldID.value]
    : inheritedTarget.value || undefined
})
const targetOptions = computed<Array<{ label: string; value: string }> | undefined>(() => {
  const capability = targetCapability.value
  if (!capability) return undefined
  if (workflowTargets && isWorkflowTargetKind(capability.targetKinds))
    return workflowTargets.targets.value
      .filter((target) => targetAcceptsKinds(target, capability.targetKinds))
      .map((target) => ({ label: target.name, value: target.id }))
  const settings = settingsStore.data
  if (!settings) return []
  if (
    capability.targetKinds.some((kind) =>
      settings.automation.targets.some((target) => target.targetKind === kind),
    )
  ) {
    return settings.automation.targets
      .filter((target) => capability.targetKinds.includes(target.targetKind))
      .map((target) => ({ label: `${target.label} · ${target.slot}`, value: target.slot }))
  }
  if (capability.targetKinds.includes('configured-application'))
    return settings.applications.profiles.map((application) => ({
      label: `${application.label} · ${application.slot}`,
      value: application.slot,
    }))
  if (capability.targetKinds.includes('ai-model'))
    return settings.ai.profiles.map((profile) => ({
      label: `${profile.label} · ${profile.slot}`,
      value: profile.slot,
    }))
  if (capability.targetKinds.includes('http-target'))
    return settings.network.httpOrigins.map((origin) => ({
      label: `${origin.label} · ${origin.slot}`,
      value: origin.slot,
    }))
  return []
})
const targetSettingsSection = computed<'automation' | 'applications' | 'ai' | 'network'>(() => {
  const kinds = targetCapability.value?.targetKinds ?? []
  if (kinds.includes('configured-application')) return 'applications'
  if (kinds.includes('ai-model')) return 'ai'
  if (kinds.includes('http-target')) return 'network'
  return 'automation'
})
const portTitle = computed(() => {
  if (props.item.kind === 'config') return props.item.field.id
  return projectionLabel(props.item.port, t, te)
})
const typeTitle = computed(() => {
  if (props.item.kind === 'config') return props.item.field.control
  const key = props.item.port.type.titleKey
  return key && te(key) ? t(key) : props.item.port.type.label
})
</script>
