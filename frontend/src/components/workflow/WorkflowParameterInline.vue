<template>
  <section
    v-show="open"
    class="workflow-configuration border-t border-default p-3"
    data-testid="workflow-parameter-inline"
    @dblclick.stop
  >
    <div v-if="loading" class="space-y-4"><USkeleton class="h-16" /><USkeleton class="h-16" /></div>
    <div v-else class="w-full space-y-2">
      <UAlert v-if="error" color="error" :description="error" class="mb-4" />
      <UButton
        v-if="error"
        color="neutral"
        variant="soft"
        size="xs"
        icon="i-tabler-refresh"
        :disabled="busy || loading"
        :label="t(dirty ? 'workflow.parameters.discard_reload' : 'workflow.parameters.reload')"
        @click="reload"
      />
      <p
        v-if="!variables.length && !blocks.length && !targets.length"
        class="py-3 text-sm text-muted"
      >
        {{ t('workflow.parameters.empty_run') }}
      </p>
      <SettingsSection
        v-if="targets.length"
        :title="t('workflow.settings_panel.targets')"
        icon="i-tabler-target"
      >
        <WorkflowTargetBindings
          :targets="targets"
          compact
          :values="values"
          :disabled="busy"
          @change="edit"
        />
      </SettingsSection>
      <SettingsSection
        v-if="items.length"
        :title="t('workflow.parameters.run_title')"
        icon="i-tabler-adjustments-horizontal"
      >
        <div>
          <template v-for="item in items" :key="item.id">
            <hr v-if="item.kind === 'separator'" class="my-2 border-default" />
            <div v-else-if="item.kind === 'label'" class="min-h-4 px-1 py-1">
              <p
                v-if="item.label"
                class="whitespace-pre-wrap text-sm font-semibold text-highlighted"
              >
                {{ item.label }}
              </p>
              <p
                v-if="item.description"
                class="mt-1 whitespace-pre-wrap text-xs leading-5 text-muted"
              >
                {{ item.description }}
              </p>
            </div>
            <div
              v-else-if="item.variable"
              class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 rounded-lg px-1 py-2 hover:bg-elevated/50"
            >
              <div class="min-w-40 flex-1">
                <p v-if="item.variable.parameter!.group" class="mb-1 text-[11px] text-dimmed">
                  {{ item.variable.parameter!.group }}
                </p>
                <label :for="`parameter-${workflowId}-${item.id}`" class="text-sm font-medium"
                  >{{ item.variable.parameter!.label
                  }}<span v-if="item.variable.parameter!.required" class="ml-1 text-error"
                    >*</span
                  ></label
                >
                <p
                  v-if="item.variable.parameter!.description"
                  class="mt-1 whitespace-pre-wrap text-xs leading-5 text-muted"
                >
                  {{ item.variable.parameter!.description }}
                </p>
              </div>
              <fieldset
                :disabled="busy"
                :class="
                  typeof item.variable.default === 'boolean' &&
                  item.variable.parameter!.control === 'auto'
                    ? 'shrink-0'
                    : 'w-full sm:w-56 sm:shrink-0'
                "
              >
                <ParameterValueEditor
                  :id="`parameter-${workflowId}-${item.id}`"
                  :key="`${item.id}:${resetGeneration}`"
                  :variable="item.variable"
                  :model-value="effectiveValue(item.variable)"
                  :types="types"
                  @update:model-value="edit(item.id, $event)"
                  @validity="invalid[item.id] = !$event"
                />
              </fieldset>
            </div>
          </template>
        </div>
      </SettingsSection>
      <p v-if="saved && !dirty" role="status" class="pt-3 text-xs text-primary">
        {{ t('workflow.parameters.saved') }}
      </p>
      <div class="mt-2 flex flex-wrap items-center gap-2 border-t border-default pt-2">
        <UButton
          color="neutral"
          variant="ghost"
          :disabled="busy || loading"
          :label="t('workflow.parameters.reset')"
          @click="reset"
        />
        <UButton
          v-if="dirty"
          color="neutral"
          variant="ghost"
          :disabled="busy"
          :label="t('workflow.parameters.discard')"
          @click="discard"
        />
        <UButton
          class="ml-auto"
          variant="soft"
          :disabled="!dirty || loading || loadFailed || hasInvalidDraft"
          :loading="busy"
          :label="t('common.save')"
          @click="save"
        />
        <UButton
          icon="i-tabler-player-play"
          :disabled="loading || busy || loadFailed || hasInvalidDraft"
          :label="t('workflow.parameters.save_run')"
          @click="run"
        />
      </div>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  Variable,
  WorkflowTarget,
  ParameterBlock,
} from '../../../../contracts/workflow/current/workflow-source'
import type { TypeProjection } from '../../../../contracts/node/current/authoring-projection'
import { parameterTransport, workflowTransport } from '@/app/transport/workflow'
import { useSettingsStore } from '@/stores/settings'
import { errorMessage } from '@/lib/invoke'
import { workflowTargetIssue } from '@/app/editor/workflowTargets'
import WorkflowTargetBindings from './WorkflowTargetBindings.vue'
import ParameterValueEditor from './ParameterValueEditor.vue'
import SettingsSection from '@/components/settings/SettingsSection.vue'
const props = defineProps<{ workflowId: string; name: string; sourceRevision?: number }>()
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ run: [workflowId: string] }>()
const { t } = useI18n()
const settings = useSettingsStore()
const targets = ref<WorkflowTarget[]>([])
const variables = ref<Variable[]>([])
const values = ref<Record<string, unknown>>({})
const confirmed = ref<Record<string, unknown>>({})
const blocks = ref<ParameterBlock[]>([])
const revision = ref(0)
let loadedWorkflow = ''
const reloadVersion = ref(0)
function reload() {
  loadedWorkflow = ''
  reloadVersion.value++
}
const invalid = ref<Record<string, boolean>>({})
const resetGeneration = ref(0)
const hasInvalidDraft = computed(() => Object.values(invalid.value).some(Boolean))
const types = ref<TypeProjection[]>([])
const loading = ref(false),
  busy = ref(false),
  error = ref(''),
  saved = ref(false),
  loadFailed = ref(false)
let generation = 0
onBeforeUnmount(() => {
  generation++
})
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T
const dirty = computed(
  () => hasInvalidDraft.value || JSON.stringify(values.value) !== JSON.stringify(confirmed.value),
)
function effectiveValue(variable: Variable) {
  return Object.hasOwn(values.value, variable.parameter!.id)
    ? values.value[variable.parameter!.id]
    : variable.default
}
const groups = computed(() => {
  const result = new Map<string, Variable[]>()
  for (const variable of [...variables.value].sort(
    (a, b) => (a.parameter!.order ?? 0) - (b.parameter!.order ?? 0),
  )) {
    const condition = variable.parameter!.visibleWhen
    const dependency =
      condition && variables.value.find((v) => v.parameter!.id === condition.parameterId)
    if (
      condition &&
      (!dependency ||
        JSON.stringify(effectiveValue(dependency)) !== JSON.stringify(condition.equals))
    )
      continue
    const name = variable.parameter!.group ?? ''
    result.set(name, [...(result.get(name) ?? []), variable])
  }
  return Array.from(result, ([name, variables]) => ({ name, variables }))
})
const items = computed(() =>
  [
    ...groups.value.flatMap((group) =>
      group.variables.map((variable) => ({
        id: variable.parameter!.id,
        kind: 'parameter',
        order: variable.parameter!.order ?? 0,
        variable,
        label: '',
        description: '',
      })),
    ),
    ...blocks.value.map((block) => ({ ...block, order: block.order ?? 0, variable: undefined })),
  ].sort((a, b) => a.order - b.order),
)
defineExpose({ run })
watch(
  () => [open.value, props.workflowId, props.sourceRevision, reloadVersion.value],
  async () => {
    if (!open.value || !props.workflowId) return
    if (loadedWorkflow === props.workflowId && dirty.value) {
      if (props.sourceRevision !== undefined && props.sourceRevision !== revision.value)
        error.value = t('workflow.parameters.source_changed')
      return
    }
    const current = ++generation
    loading.value = true
    error.value = ''
    saved.value = false
    loadFailed.value = false
    variables.value = []
    invalid.value = {}
    values.value = {}
    confirmed.value = {}
    try {
      const [configuration, projection] = await Promise.all([
        parameterTransport.get(props.workflowId),
        workflowTransport.getAuthoringProjection(),
        settings.loaded ? Promise.resolve() : settings.load(),
      ])
      if (current !== generation) return
      targets.value = configuration.targets ?? []
      blocks.value = (configuration.blocks ?? []) as ParameterBlock[]
      loadedWorkflow = props.workflowId
      variables.value = configuration.variables as unknown as Variable[]
      values.value = clone(configuration.values ?? {})
      confirmed.value = clone(values.value)
      revision.value = configuration.revision
      types.value = JSON.parse(projection).body.types
    } catch (cause) {
      if (current === generation) {
        error.value = errorMessage(cause)
        loadFailed.value = true
      }
    } finally {
      if (current === generation) loading.value = false
    }
  },
  { immediate: true },
)
function edit(id: string, value: unknown) {
  values.value = { ...values.value, [id]: clone(value) }
  saved.value = false
}
function reset() {
  invalid.value = {}
  resetGeneration.value++
  values.value = {}
  saved.value = false
}
function discard() {
  values.value = clone(confirmed.value)
  error.value = ''
  invalid.value = {}
  resetGeneration.value++
}
async function save(): Promise<boolean> {
  if (loading.value || busy.value || loadFailed.value || hasInvalidDraft.value) return false
  busy.value = true
  error.value = ''
  const snapshot = clone(values.value)
  const current = generation
  try {
    const diagnostics = await parameterTransport.save(props.workflowId, revision.value, snapshot)
    if (current !== generation) return false
    if (diagnostics?.some((d) => d.severity === 'error')) {
      error.value =
        workflowTargetIssue(
          diagnostics.find((d) => d.severity === 'error')!.code,
          String(diagnostics.find((d) => d.severity === 'error')?.params?.parameterLabel ?? ''),
          t,
        ) ??
        t('workflow.parameters.invalid', {
          name: String(diagnostics[0]?.params?.parameterLabel ?? ''),
        })
      return false
    }
    confirmed.value = snapshot
    saved.value = true
    return true
  } catch (cause) {
    error.value = errorMessage(cause)
    return false
  } finally {
    busy.value = false
  }
}
async function run() {
  const workflowId = props.workflowId
  if (!(await save())) return
  emit('run', workflowId)
}
</script>

<style scoped>
.workflow-configuration {
  --settings-accent: var(--ui-secondary);
  --settings-section-bg: color-mix(in oklab, var(--settings-accent) 1.5%, var(--ui-surface));
  --settings-section-border: color-mix(in oklab, var(--settings-accent) 15%, var(--ui-border));
}
.workflow-configuration :deep(.settings-section) {
  gap: 8px;
  padding: 12px;
}
.workflow-configuration :deep(.settings-section__heading) {
  gap: 8px;
}
.workflow-configuration :deep(.settings-section__icon) {
  width: 24px;
  height: 24px;
}
</style>
