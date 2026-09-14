<template>
  <aside class="flex h-full w-full min-w-0 flex-col border-l border-default bg-default">
    <div class="flex items-center justify-between border-b border-default px-4 py-3">
      <div class="min-w-0">
        <h2 class="truncate text-sm font-semibold text-highlighted">
          {{ t('workflow.inspector.title') }}
        </h2>
        <p class="truncate font-mono text-[10px] text-dimmed">
          {{ node?.id || t('workflow.inspector.no_selection') }}
        </p>
      </div>
      <UButton
        v-if="node"
        icon="i-tabler-trash"
        color="error"
        variant="ghost"
        size="xs"
        :aria-label="t('workflow.inspector.remove_node')"
        @click="emit('command', { kind: 'remove-node', nodeId: node.id })"
      />
    </div>

    <div
      v-if="!node || !projection || !surface"
      class="flex flex-1 items-center justify-center px-8 text-center"
    >
      <div>
        <UIcon name="i-tabler-pointer" class="mx-auto mb-3 size-6 text-dimmed" />
        <p class="text-xs text-muted">{{ t('workflow.inspector.select_hint') }}</p>
      </div>
    </div>

    <div v-else class="flex-1 space-y-5 overflow-y-auto p-4">
      <div class="flex items-start justify-between gap-2" data-testid="workflow-node-identity">
        <h3 class="min-w-0 text-sm font-semibold text-highlighted">{{ projectionTitle }}</h3>
        <UBadge color="neutral" variant="soft" size="xs" class="shrink-0">{{
          categoryLabel
        }}</UBadge>
      </div>
      <p
        v-if="projectionDescription"
        class="rounded-lg border border-default bg-elevated/30 px-3 py-2 text-[11px] leading-5 text-muted"
      >
        {{ projectionDescription }}
      </p>

      <PathFollowingHelp :node-type-id="projection.nodeRef.nodeTypeId" />

      <PlaybackCalibrationPanel v-if="isInputClipPlayback" :node="node" :target-slot="targetSlot" />

      <section class="space-y-2">
        <label class="block text-xs font-medium text-toned" for="workflow-node-label">
          {{ t('workflow.inspector.label') }}
        </label>
        <UInput
          id="workflow-node-label"
          :model-value="node.label || ''"
          :placeholder="t('workflow.inspector.label_placeholder')"
          class="w-full"
          @update:model-value="setLabel"
        />
      </section>

      <section
        v-for="group in primaryGroups"
        :key="group"
        v-show="surface.groups[group].length"
        class="space-y-3"
      >
        <div class="flex items-center gap-2 border-b border-default pb-2">
          <h3 class="text-xs font-semibold text-highlighted">{{ groupTitle(group) }}</h3>
          <UBadge color="neutral" variant="soft" size="sm">
            {{ surface.groups[group].length }}
          </UBadge>
        </div>
        <WorkflowAuthoringSurfaceItem
          v-for="item in surface.groups[group]"
          :key="item.key"
          :item="item"
          :node="node"
          :projection="projection"
          :variables="variables.map((variable) => variable.name)"
          :target-defaults="targetDefaults"
          :target-slot="targetSlot"
          :connected-input-ids="connectedInputIds"
          :resources="resources"
          @command="emit('command', $event)"
          @capture-template="emit('capture-template')"
          @locate-resource="emit('locate-resource', $event)"
        />
      </section>

      <TemplateMatchPreviewPanel
        v-if="supportsTemplatePreview(projection)"
        :key="node.id"
        :node="node"
        :projection="projection"
        :resources="resources"
        :target-slot="targetSlot"
        :connected-input-ids="connectedInputIds"
      />

      <UCollapsible
        v-if="
          surface.groups.advanced.length ||
          projection.capabilities.length ||
          (projection.configuredTargets?.length ?? 0) ||
          projection.statusEvents.length
        "
        v-model:open="advancedOpen"
      >
        <UButton
          :label="t('workflow.inspector.advanced')"
          icon="i-tabler-adjustments-horizontal"
          :trailing-icon="advancedOpen ? 'i-tabler-chevron-up' : 'i-tabler-chevron-down'"
          color="neutral"
          variant="ghost"
          class="w-full justify-start"
        />
        <template #content>
          <div class="space-y-4 pt-3">
            <WorkflowAuthoringSurfaceItem
              v-for="item in surface.groups.advanced"
              :key="item.key"
              :item="item"
              :node="node"
              :projection="projection"
              :variables="variables.map((variable) => variable.name)"
              :target-defaults="targetDefaults"
              :target-slot="targetSlot"
              :connected-input-ids="connectedInputIds"
              :resources="resources"
              @command="emit('command', $event)"
              @capture-template="emit('capture-template')"
              @locate-resource="emit('locate-resource', $event)"
            />

            <section v-if="projection.capabilities.length" class="space-y-2">
              <h3 class="text-xs font-semibold text-highlighted">
                {{ t('workflow.inspector.capabilities') }}
              </h3>
              <div
                v-for="capability in projection.capabilities"
                :key="capability.requirementId"
                class="rounded-lg border border-default px-3 py-2.5"
              >
                <p class="text-xs font-medium text-toned">{{ capability.requirementId }}</p>
                <p class="mt-1 text-[11px] text-muted">{{ capability.operations.join(', ') }}</p>
                <p class="mt-1 font-mono text-[10px] text-dimmed">
                  {{ capability.risk }} / {{ capability.consent }}
                </p>
              </div>
            </section>

            <section v-if="projection.configuredTargets?.length" class="space-y-2">
              <h3 class="text-xs font-semibold text-highlighted">
                {{ t('workflow.inspector.configured_targets') }}
              </h3>
              <div
                v-for="target in projection.configuredTargets ?? []"
                :key="target.id"
                class="rounded-lg border border-default px-3 py-2.5"
              >
                <p class="text-xs font-medium text-toned">{{ target.id }}</p>
                <p class="mt-1 text-[11px] text-muted">{{ target.targetKinds.join(', ') }}</p>
              </div>
            </section>

            <section v-if="projection.statusEvents.length" class="space-y-2">
              <h3 class="text-xs font-semibold text-highlighted">
                {{ t('workflow.inspector.observed_status') }}
              </h3>
              <p class="text-[11px] leading-5 text-muted">
                {{ t('workflow.inspector.status_hint') }}
              </p>
              <code class="block whitespace-pre-wrap text-[10px] text-toned">{{
                projection.statusEvents.map((event) => event.code).join('\n')
              }}</code>
            </section>
          </div>
        </template>
      </UCollapsible>

      <section v-if="surface.groups.output.length" class="space-y-3">
        <div class="flex items-center gap-2 border-b border-default pb-2">
          <h3 class="text-xs font-semibold text-highlighted">
            {{ t('workflow.inspector.group_output') }}
          </h3>
          <UBadge color="neutral" variant="soft" size="sm">
            {{ surface.groups.output.length }}
          </UBadge>
        </div>
        <WorkflowAuthoringSurfaceItem
          v-for="item in surface.groups.output"
          :key="item.key"
          :item="item"
          :node="node"
          :projection="projection"
          :variables="variables.map((variable) => variable.name)"
          :target-defaults="targetDefaults"
          :target-slot="targetSlot"
          :connected-input-ids="connectedInputIds"
          :resources="resources"
          @command="emit('command', $event)"
          @capture-template="emit('capture-template')"
          @locate-resource="emit('locate-resource', $event)"
        />
      </section>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { WORKFLOW_TARGETS } from './workflowTargets'
const workflowTargets = inject(WORKFLOW_TARGETS, undefined)
import { inject, computed, defineAsyncComponent, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  TargetDefault,
  Variable,
  WorkflowResource,
} from '../../../../contracts/workflow/current/workflow-source'
import type { TypeProjection } from '../../../../contracts/node/current/authoring-projection'
import type { EditorCommand, Node, NodeProjection } from './EditorSession'
import WorkflowAuthoringSurfaceItem from './WorkflowAuthoringSurfaceItem.vue'
import { supportsTemplatePreview } from './templateMatchPreview'
const TemplateMatchPreviewPanel = defineAsyncComponent(
  () => import('./TemplateMatchPreviewPanel.vue'),
)
import {
  effectiveTargetSlot,
  projectAuthoringSurface,
  type AuthoringGroup,
} from './authoringSurface'
import type { ResourceLocation } from './resourceLocator'

import PathFollowingHelp from './PathFollowingHelp.vue'

const PlaybackCalibrationPanel = defineAsyncComponent(
  () => import('./PlaybackCalibrationPanel.vue'),
)

const props = defineProps<{
  node: Node | null
  projection: NodeProjection | null
  variables: Variable[]
  targetDefaults: TargetDefault[]
  types: TypeProjection[]
  connectedInputIds?: ReadonlySet<string>
  resources?: WorkflowResource[]
}>()
const emit = defineEmits<{
  command: [command: EditorCommand]
  'capture-template': []
  'locate-resource': [location: ResourceLocation]
}>()
const { t, te } = useI18n()
const advancedOpen = ref(false)
const primaryGroups: AuthoringGroup[] = ['required', 'common']
const projectionTitle = computed(() => {
  const projection = props.projection
  if (!projection) return ''
  return projection.titleKey && te(projection.titleKey)
    ? t(projection.titleKey)
    : projection.nodeRef.nodeTypeId.split('/').filter(Boolean).at(-1) || ''
})
const categoryLabel = computed(() => {
  const category = props.projection?.category || 'other'
  const key = `workflow.catalog.category.${category}`
  return te(key) ? t(key) : category
})
const projectionDescription = computed(() => {
  const key = props.projection?.descriptionKey
  return key && te(key) ? t(key) : ''
})
const isInputClipPlayback = computed(
  () =>
    props.projection?.nodeRef.nodeTypeId ===
    'https://schemas.yotta.dev/nodes/automation/play-input-clip',
)
const surface = computed(() =>
  props.projection && props.node ? projectAuthoringSurface(props.projection, props.node) : null,
)
const targetSlot = computed(() =>
  props.projection && props.node
    ? (workflowTargets?.resolve(
        effectiveTargetSlot(props.projection, props.node, props.targetDefaults),
      ) ?? effectiveTargetSlot(props.projection, props.node, props.targetDefaults))
    : '',
)

function groupTitle(group: AuthoringGroup): string {
  return t(`workflow.inspector.group_${group}`)
}

function setLabel(value: string | number): void {
  if (!props.node) return
  emit('command', {
    kind: 'set-node-label',
    nodeId: props.node.id,
    label: String(value),
  })
}
</script>
