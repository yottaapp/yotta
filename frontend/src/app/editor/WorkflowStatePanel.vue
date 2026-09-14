<template>
  <aside
    data-testid="workflow-state-panel"
    class="flex h-full w-full min-w-0 flex-col border-l border-default bg-default"
  >
    <div class="flex items-center justify-between border-b border-default px-4 py-3">
      <div class="min-w-0">
        <h2 class="text-sm font-semibold text-highlighted">
          {{ t('workflow.state_panel.title') }}
        </h2>
        <p class="mt-0.5 text-[10px] text-dimmed">
          {{ t('workflow.state_panel.hint') }}
        </p>
      </div>
      <UButton
        icon="i-tabler-x"
        color="neutral"
        variant="ghost"
        size="xs"
        :aria-label="t('common.close')"
        @click="emit('close')"
      />
    </div>

    <div class="flex-1 space-y-4 overflow-y-auto p-4">
      <section class="space-y-3 rounded-lg border border-default p-3">
        <div class="flex items-center justify-between">
          <h3 class="text-xs font-semibold text-highlighted">
            {{ t('workflow.inspector.state_title') }}
          </h3>
          <UBadge data-testid="workflow-state-count" color="neutral" variant="soft" size="sm">{{
            variables.length
          }}</UBadge>
        </div>
        <p class="text-[11px] leading-5 text-muted">
          {{ t('workflow.inspector.state_hint') }}
        </p>
        <p class="text-[11px] leading-5 text-muted">{{ t('paths.variable_types_hint') }}</p>
        <div class="grid grid-cols-[1fr_1fr_auto] gap-2">
          <UInput
            v-model="newVariableName"
            data-testid="workflow-state-new-name"
            :placeholder="t('workflow.inspector.state_name_placeholder')"
            size="sm"
          />
          <TypeSelect
            v-model="newVariableTypeId"
            data-testid="workflow-state-new-type"
            :items="stateTypeItems"
            value-key="value"
            label-key="label"
            size="sm"
            :max-width="28"
          />
          <UButton
            data-testid="workflow-state-add"
            icon="i-tabler-plus"
            size="sm"
            color="primary"
            :disabled="!canAddVariable"
            :aria-label="t('workflow.inspector.state_add')"
            @click="addStateVariable"
          />
        </div>
        <UFormField
          :label="t('workflow.state_panel.initial_value')"
          :hint="t('workflow.state_panel.initial_value_hint')"
        >
          <StateDefaultValueEditor
            v-model="newVariableDefault"
            :type="selectedStateTypeChoice?.projection"
            :editor-adapter="selectedStateTypeChoice?.editorAdapter"
          />
        </UFormField>
      </section>

      <UInput
        v-model="searchQuery"
        icon="i-tabler-search"
        size="sm"
        :placeholder="t('workflow.state_panel.search')"
      />

      <div v-if="filteredVariables.length" class="space-y-2">
        <div
          v-for="variable in visibleVariables"
          :key="variable.name"
          :data-testid="`workflow-state-variable-${variable.name}`"
          class="rounded-lg border border-default bg-elevated/35"
        >
          <div
            draggable="true"
            class="flex cursor-grab items-center gap-2 px-3 py-2.5 active:cursor-grabbing"
            :title="t('workflow.state_panel.drag_hint')"
            @dragstart="startStateDrag($event, variable.name)"
          >
            <UIcon name="i-tabler-grip-vertical" class="size-4 shrink-0 text-dimmed" />
            <span class="min-w-0 flex-1 truncate font-mono text-xs text-toned">{{
              variable.name
            }}</span>
            <span
              class="size-2 shrink-0 rounded-full"
              :style="{ backgroundColor: typeForVariable(variable)?.color || '#a1a1aa' }"
              aria-hidden="true"
            />
            <span class="max-w-28 truncate text-[10px] text-dimmed">{{
              variableTypeLabel(variable)
            }}</span>
            <UButton
              v-if="referenceCount(variable.name)"
              icon="i-tabler-focus-2"
              color="neutral"
              variant="soft"
              size="xs"
              :label="String(referenceCount(variable.name))"
              :aria-label="
                t('workflow.state_panel.locate_references', {
                  name: variable.name,
                  count: referenceCount(variable.name),
                })
              "
              @click="emit('locate', variable.name)"
            />
            <UButton
              icon="i-tabler-database-export"
              color="neutral"
              variant="ghost"
              size="xs"
              :aria-label="t('workflow.state_panel.insert_read', { name: variable.name })"
              @click="emit('insert', variable.name, 'read')"
            />
            <UButton
              icon="i-tabler-database-import"
              color="primary"
              variant="ghost"
              size="xs"
              :aria-label="t('workflow.state_panel.insert_write', { name: variable.name })"
              @click="emit('insert', variable.name, 'write')"
            />
            <UDropdownMenu :items="variableActions(variable)">
              <UButton
                icon="i-tabler-dots-vertical"
                color="neutral"
                variant="ghost"
                size="xs"
                :aria-label="t('workflow.state_panel.actions', { name: variable.name })"
              />
            </UDropdownMenu>
          </div>
          <UFormField
            class="border-t border-default px-3 py-2"
            :label="t('workflow.state_panel.initial_value')"
          >
            <StateDefaultValueEditor
              :model-value="variable.default"
              :type="typeForVariable(variable)"
              :editor-adapter="editorAdapterForVariable(variable)"
              @update:model-value="updateVariableDefault(variable, $event)"
            />
          </UFormField>
          <div
            v-if="editingName === variable.name"
            class="space-y-2 border-t border-default px-3 py-2"
          >
            <div class="flex items-center gap-2">
              <TypeSelect
                v-model="editingTypeId"
                class="min-w-0 flex-1"
                width-mode="fill"
                :items="stateTypeItems"
                value-key="value"
                label-key="label"
                size="sm"
              />
              <UButton
                color="neutral"
                variant="ghost"
                size="xs"
                :label="t('common.cancel')"
                @click="cancelTypeChange"
              />
              <UButton
                size="xs"
                :label="t('common.confirm')"
                :disabled="!editingTypeId || editingImpact.issues.length > 0"
                @click="commitTypeChange(variable.name)"
              />
            </div>
            <div v-if="referenceCount(variable.name)" class="rounded-md bg-warning/10 p-2">
              <p class="text-[11px] leading-4 text-warning">
                {{
                  t('workflow.state_panel.type_change_impact', {
                    count: referenceCount(variable.name),
                  })
                }}
              </p>
              <div class="mt-2 max-h-36 space-y-1 overflow-y-auto">
                <UButton
                  v-for="reference in referencesFor(variable.name)"
                  :key="`${reference.graphId}:${reference.nodeId}`"
                  color="neutral"
                  variant="ghost"
                  size="xs"
                  class="w-full justify-start font-mono"
                  icon="i-tabler-focus-2"
                  :label="`${reference.graphId} / ${reference.nodeId} · ${reference.mode}`"
                  @click="emit('locate-reference', reference.graphId, reference.nodeId)"
                />
              </div>
            </div>
            <div
              v-if="editingImpact.issues.length"
              class="rounded-md border border-error/30 bg-error/10 p-2"
            >
              <p class="text-[11px] leading-4 text-error">
                {{
                  t('workflow.state_panel.type_change_blocked', {
                    count: editingImpact.issues.length,
                  })
                }}
              </p>
              <UButton
                v-for="issue in editingImpact.issues"
                :key="`${issue.graphId}:${issue.edge.from.nodeId}:${issue.edge.from.portId}:${issue.edge.to.nodeId}:${issue.edge.to.portId}`"
                color="neutral"
                variant="ghost"
                size="xs"
                class="mt-1 w-full justify-start font-mono"
                icon="i-tabler-plug-connected-x"
                :label="`${issue.graphId} · ${issue.edge.from.nodeId}.${issue.edge.from.portId} → ${issue.edge.to.nodeId}.${issue.edge.to.portId} · ${issueDispositionLabel(issue.disposition)}`"
                @click="emit('locate-reference', issue.graphId, issue.edge.to.nodeId)"
              />
            </div>
            <p v-else-if="referenceCount(variable.name)" class="text-[11px] leading-4 text-success">
              {{ t('workflow.state_panel.type_change_safe') }}
            </p>
          </div>
        </div>
        <UButton
          v-if="visibleVariables.length < filteredVariables.length"
          color="neutral"
          variant="soft"
          size="sm"
          class="w-full justify-center"
          :label="
            t('workflow.state_panel.show_more', {
              remaining: filteredVariables.length - visibleVariables.length,
            })
          "
          @click="visibleLimit += STATE_VARIABLE_PAGE_SIZE"
        />
      </div>

      <div v-else class="rounded-lg border border-dashed border-default px-4 py-8 text-center">
        <UIcon name="i-tabler-database" class="mx-auto mb-2 size-6 text-dimmed" />
        <p class="text-xs text-muted">
          {{
            variables.length
              ? t('workflow.state_panel.no_results')
              : t('workflow.state_panel.empty')
          }}
        </p>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed, ref, toRaw, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Variable } from '../../../../contracts/workflow/current/workflow-source'
import type {
  TypeExpression,
  TypeProjection,
} from '../../../../contracts/node/current/authoring-projection'
import type {
  EditorCommand,
  StateReferenceMode,
  StateReferenceLocation,
  StateTypeChangeImpact,
} from '@/app/editor/EditorSession'
import { filterStateVariables, STATE_VARIABLE_PAGE_SIZE } from '@/app/editor/stateVariableQuery'
import TypeSelect from '@/components/common/TypeSelect.vue'
import StateDefaultValueEditor from '@/app/editor/StateDefaultValueEditor.vue'
import {
  buildStateTypeChoices,
  stateTypeChoiceForExpression,
} from '@/app/editor/stateVariableTypes'

const props = defineProps<{
  variables: Variable[]
  types: TypeProjection[]
  references: Record<string, StateReferenceLocation[]>
  typeChangeImpact: (name: string, type: TypeExpression) => StateTypeChangeImpact
}>()
const emit = defineEmits<{
  parameters: []
  command: [command: EditorCommand]
  insert: [name: string, mode: StateReferenceMode]
  locate: [name: string]
  'locate-reference': [graphId: string, nodeId: string]
  close: []
}>()
const { t, te } = useI18n()
const newVariableName = ref('')
const newVariableTypeId = ref('')
const newVariableDefault = ref<unknown>(null)
const searchQuery = ref('')
const editingName = ref('')
const editingTypeId = ref('')
const visibleLimit = ref(STATE_VARIABLE_PAGE_SIZE)
const filteredVariables = computed(() =>
  filterStateVariables(props.variables, searchQuery.value, variableTypeLabel),
)
const visibleVariables = computed(() => filteredVariables.value.slice(0, visibleLimit.value))
const stateTypeChoices = computed(() => buildStateTypeChoices(props.types))
const stateTypeItems = computed(() =>
  stateTypeChoices.value.map((choice) => ({
    label: choice.titleKey
      ? t(choice.titleKey)
      : choice.projection.titleKey && te(choice.projection.titleKey)
        ? t(choice.projection.titleKey)
        : choice.id.split('/').at(-2)!,
    value: choice.id,
    color: choice.projection.color || '#a1a1aa',
  })),
)
const selectedStateTypeChoice = computed(() =>
  stateTypeChoices.value.find((choice) => choice.id === newVariableTypeId.value),
)
const canAddVariable = computed(
  () =>
    /^[A-Za-z0-9_][A-Za-z0-9._-]*$/.test(newVariableName.value) &&
    Boolean(selectedStateTypeChoice.value),
)
const editingImpact = computed<StateTypeChangeImpact>(() => {
  if (!editingName.value || !editingTypeId.value) return { references: [], issues: [] }
  const choice = stateTypeChoices.value.find((candidate) => candidate.id === editingTypeId.value)
  return choice
    ? props.typeChangeImpact(editingName.value, choice.expression)
    : { references: [], issues: [] }
})

watch(
  stateTypeChoices,
  (values) => {
    if (!values.some((choice) => choice.id === newVariableTypeId.value))
      newVariableTypeId.value = values[0]?.id ?? ''
  },
  { immediate: true },
)

watch(
  selectedStateTypeChoice,
  (choice) => {
    newVariableDefault.value = choice ? cloneReactiveValue(choice.defaultValue) : null
  },
  { immediate: true },
)

watch(searchQuery, () => {
  visibleLimit.value = STATE_VARIABLE_PAGE_SIZE
})

function addStateVariable(): void {
  const choice = selectedStateTypeChoice.value
  if (!choice || !canAddVariable.value) return
  emit('command', {
    kind: 'add-state-variable',
    name: newVariableName.value,
    type: cloneReactiveValue(choice.expression),
    defaultValue: cloneReactiveValue(newVariableDefault.value),
  })
  newVariableName.value = ''
  newVariableDefault.value = cloneReactiveValue(choice.defaultValue)
}

function variableTypeLabel(variable: Variable): string {
  const choice = stateTypeChoiceForExpression(stateTypeChoices.value, variable.type)
  if (choice?.titleKey) return t(choice.titleKey)
  if (choice?.projection.titleKey && te(choice.projection.titleKey))
    return t(choice.projection.titleKey)
  return choice?.id.split('/').at(-2) ?? variable.type.kind
}

function typeForVariable(variable: Variable): TypeProjection | undefined {
  return stateTypeChoiceForExpression(stateTypeChoices.value, variable.type)?.projection
}

function editorAdapterForVariable(variable: Variable): 'key-chord' | undefined {
  return stateTypeChoiceForExpression(stateTypeChoices.value, variable.type)?.editorAdapter
}

function updateVariableDefault(variable: Variable, value: unknown): void {
  emit('command', {
    kind: 'update-state-variable',
    name: variable.name,
    type: cloneReactiveValue(variable.type),
    defaultValue: value,
  })
}

function referenceCount(name: string): number {
  return props.references[name]?.length ?? 0
}

function isNumericState(variable: Variable): boolean {
  if (variable.type.kind !== 'ref') return false
  const typeId = variable.type.ref.typeId
  return Boolean(
    props.types
      .find((candidate) => candidate.typeRef.typeId === typeId)
      ?.traits.includes('numeric'),
  )
}

function variableActions(variable: Variable) {
  const insertions = [
    {
      label: t('workflow.parameters.expose'),
      icon: 'i-tabler-adjustments-horizontal',
      onSelect: () => {
        if (!variable.parameter)
          emit('command', {
            kind: 'update-state-variable',
            name: variable.name,
            type: cloneReactiveValue(variable.type),
            defaultValue: cloneReactiveValue(variable.default),
            parameter: {
              id: crypto.randomUUID(),
              label: variable.name,
              control: 'auto',
              order: props.variables.filter((v) => v.parameter).length,
            },
          })
        emit('parameters')
      },
    },
    {
      label: t('workflow.state_panel.insert_last_change', { name: variable.name }),
      icon: 'i-tabler-history',
      onSelect: () => emit('insert', variable.name, 'last-change'),
    },
    ...(isNumericState(variable)
      ? [
          {
            label: t('workflow.state_panel.insert_increment', { name: variable.name }),
            icon: 'i-tabler-database-plus',
            onSelect: () => emit('insert', variable.name, 'increment'),
          },
        ]
      : []),
  ]
  return [
    insertions,
    [
      {
        label: t('workflow.state_panel.type_change', { name: variable.name }),
        icon: 'i-tabler-edit',
        onSelect: () => beginTypeChange(variable),
      },
      {
        label: t('workflow.inspector.state_remove', { name: variable.name }),
        icon: 'i-tabler-trash',
        color: 'error' as const,
        disabled: referenceCount(variable.name) > 0,
        onSelect: () => emit('command', { kind: 'remove-state-variable', name: variable.name }),
      },
    ],
  ]
}

function referencesFor(name: string): StateReferenceLocation[] {
  return props.references[name] ?? []
}

function issueDispositionLabel(disposition: 'conversion' | 'incompatible'): string {
  return t(`workflow.state_panel.type_change_${disposition}`)
}

function beginTypeChange(variable: Variable): void {
  editingName.value = variable.name
  editingTypeId.value =
    stateTypeChoiceForExpression(stateTypeChoices.value, variable.type)?.id ?? ''
}

function cancelTypeChange(): void {
  editingName.value = ''
  editingTypeId.value = ''
}

function commitTypeChange(name: string): void {
  if (editingImpact.value.issues.length) return
  const choice = stateTypeChoices.value.find((candidate) => candidate.id === editingTypeId.value)
  if (!choice) return
  emit('command', {
    kind: 'update-state-variable',
    name,
    type: cloneReactiveValue(choice.expression),
    defaultValue: cloneReactiveValue(choice.defaultValue),
  })
  cancelTypeChange()
}

function startStateDrag(event: DragEvent, name: string): void {
  if (!event.dataTransfer) return
  event.dataTransfer.effectAllowed = 'copy'
  event.dataTransfer.setData(
    'application/x-yotta-state-reference',
    JSON.stringify({ name, mode: event.altKey ? 'write' : 'read' }),
  )
}

function cloneReactiveValue<T>(value: T): T {
  return structuredClone(toRaw(value))
}
</script>
