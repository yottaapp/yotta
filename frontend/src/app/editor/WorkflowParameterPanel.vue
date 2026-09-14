<template>
  <section
    :class="embedded ? 'flex flex-col' : 'flex h-full min-h-0 flex-col'"
    data-testid="workflow-parameter-panel"
  >
    <header v-if="!embedded" class="space-y-1 border-b border-default px-4 py-3">
      <h2 class="text-sm font-semibold">{{ t('workflow.parameters.title') }}</h2>
      <p class="text-xs leading-5 text-muted">{{ t('workflow.parameters.author_hint') }}</p>
    </header>
    <div class="flex-1 space-y-4 overflow-y-auto p-4">
      <div class="flex gap-2">
        <AdaptiveSelect
          v-model="selected"
          :items="available"
          class="min-w-0 flex-1"
          :placeholder="t('workflow.parameters.choose_variable')"
        />
        <UButton
          icon="i-tabler-plus"
          :disabled="!selected"
          :aria-label="t('workflow.parameters.expose')"
          @click="expose"
        />
      </div>
      <div class="flex flex-wrap gap-2 border-y border-dashed border-default py-3">
        <UButton
          size="xs"
          variant="soft"
          color="neutral"
          icon="i-tabler-heading"
          :label="t('workflow.parameters.text_block')"
          @click="addBlock('label')"
        />
        <UButton
          size="xs"
          variant="soft"
          color="neutral"
          icon="i-tabler-separator-horizontal"
          :label="t('workflow.parameters.separator')"
          @click="addBlock('separator')"
        />
      </div>
      <p v-if="!rows.length" class="py-6 text-xs leading-6 text-muted">
        {{ t('workflow.parameters.empty_author') }}
      </p>
      <ArrangementTable
        v-else
        :model-value="rows"
        v-model:selection="selection"
        :columns="columns"
        :row-label="(row) => row.label"
        min-width="280px"
        @update:model-value="reorder"
        @remove="removeRows"
        @activate="activeId = $event"
      >
        <template #cells="{ row }">
          <td class="w-7 px-1 py-2">
            <UIcon
              :name="
                row.kind === 'parameter'
                  ? 'i-tabler-adjustments-horizontal'
                  : row.kind === 'label'
                    ? 'i-tabler-heading'
                    : 'i-tabler-separator-horizontal'
              "
              :class="row.kind === 'parameter' ? 'text-primary' : 'text-muted'"
              class="size-4"
            />
          </td>
          <td class="px-2 py-2">
            <UInput
              v-if="row.kind === 'label'"
              class="w-full"
              size="xs"
              :model-value="row.label"
              :placeholder="t('workflow.parameters.text_placeholder')"
              :aria-label="t('workflow.parameters.text_block')"
              @change="renameBlock(row.id, $event)"
            />
            <button
              v-else
              class="w-full truncate text-left"
              :class="activeId === row.id ? 'text-primary' : ''"
              @click="activeId = row.id"
            >
              {{ row.label }}
            </button>
          </td>
        </template>
      </ArrangementTable>
      <details
        v-for="block in activeBlocks"
        :key="block.id"
        open
        class="group/parameter-details rounded-lg border border-default p-3"
      >
        <summary class="flex cursor-pointer items-center gap-2 text-xs font-semibold">
          <UIcon
            name="i-tabler-chevron-right"
            class="size-4 transition-transform group-open/parameter-details:rotate-90 motion-reduce:transition-none"
          />{{ block.label || t('workflow.parameters.text_block') }}
        </summary>
        <div class="mt-3 space-y-3">
          <UFormField :label="t('workflow.parameters.label')"
            ><UInput
              class="w-full"
              :model-value="block.label"
              @change="renameBlock(block.id, $event)"
          /></UFormField>
          <UFormField :label="t('workflow.parameters.description')"
            ><UTextarea
              class="w-full"
              :rows="2"
              :model-value="block.description"
              @change="describeBlock(block.id, $event)"
          /></UFormField>
        </div>
      </details>
      <details
        v-for="variable in activeVariables"
        :key="variable.parameter!.id"
        open
        class="group/parameter-details rounded-lg border border-default p-3"
      >
        <summary class="flex cursor-pointer items-center gap-2 text-xs font-semibold">
          <UIcon
            name="i-tabler-chevron-right"
            class="size-4 transition-transform group-open/parameter-details:rotate-90 motion-reduce:transition-none"
          />{{ variable.parameter!.label
          }}<span class="font-normal text-dimmed">· {{ variable.name }}</span>
        </summary>
        <div class="mt-3 space-y-3">
          <UFormField :label="t('workflow.parameters.label')"
            ><UInput
              class="w-full"
              :model-value="variable.parameter!.label"
              @change="textChange(variable, 'label', $event)"
          /></UFormField>
          <UFormField :label="t('workflow.parameters.description')"
            ><UTextarea
              class="w-full"
              :rows="2"
              :model-value="variable.parameter!.description"
              @change="textChange(variable, 'description', $event)"
          /></UFormField>
          <UFormField :label="t('workflow.parameters.group')"
            ><UInput
              class="w-full"
              :model-value="variable.parameter!.group"
              @change="textChange(variable, 'group', $event)"
          /></UFormField>
          <UFormField :label="t('workflow.parameters.control')">
            <AdaptiveSelect
              :model-value="variable.parameter!.control"
              :items="controls(variable)"
              width-mode="fill"
              @update:model-value="changeControl(variable, String($event))"
            />
          </UFormField>
          <template v-if="variable.parameter!.control !== 'auto'">
            <div
              v-for="(option, optionIndex) in variable.parameter!.options"
              :key="optionIndex"
              class="flex items-center gap-2"
            >
              <UInput
                class="min-w-0 flex-1"
                :aria-label="t('workflow.parameters.option_label')"
                :model-value="option.label"
                @change="optionChange(variable, optionIndex, 'label', $event)"
              />
              <UInput
                class="min-w-0 flex-1"
                :aria-label="t('workflow.parameters.option_value')"
                :model-value="String(option.value)"
                @change="optionChange(variable, optionIndex, 'value', $event)"
              />
              <UButton
                icon="i-tabler-minus"
                size="xs"
                variant="ghost"
                :disabled="variable.parameter!.options!.length <= 1"
                :aria-label="t('workflow.parameters.remove_option')"
                @click="removeOption(variable, optionIndex)"
              />
            </div>
            <UButton
              variant="soft"
              size="xs"
              icon="i-tabler-plus"
              :label="t('workflow.parameters.add_option')"
              @click="addOption(variable)"
            />
          </template>
          <UFormField :label="t('workflow.parameters.default')">
            <ParameterValueEditor
              :variable="variable"
              :model-value="variable.default"
              :types="types"
              @update:model-value="update(variable, variable.parameter, $event)"
            />
          </UFormField>
          <div v-if="typeof variable.default === 'number'" class="grid grid-cols-2 gap-2">
            <UFormField :label="t('workflow.parameters.minimum')"
              ><UInputNumber
                :model-value="variable.parameter!.minimum"
                @update:model-value="
                  update(variable, { ...variable.parameter!, minimum: $event ?? undefined })
                "
            /></UFormField>
            <UFormField :label="t('workflow.parameters.maximum')"
              ><UInputNumber
                :model-value="variable.parameter!.maximum"
                @update:model-value="
                  update(variable, { ...variable.parameter!, maximum: $event ?? undefined })
                "
            /></UFormField>
          </div>
          <UCheckbox
            :label="t('workflow.parameters.required')"
            :model-value="variable.parameter!.required"
            @update:model-value="
              update(variable, { ...variable.parameter!, required: Boolean($event) })
            "
          />
          <UFormField :label="t('workflow.parameters.visible_when')">
            <AdaptiveSelect
              :model-value="variable.parameter!.visibleWhen?.parameterId ?? '::always'"
              :items="visibilityItems(variable)"
              width-mode="fill"
              @update:model-value="
                update(variable, {
                  ...variable.parameter!,
                  visibleWhen:
                    $event !== '::always'
                      ? { parameterId: String($event), equals: true }
                      : undefined,
                })
              "
            />
          </UFormField>
        </div>
      </details>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  Parameter,
  ParameterBlock,
  Variable,
} from '../../../../contracts/workflow/current/workflow-source'
import type { TypeProjection } from '../../../../contracts/node/current/authoring-projection'
import type { EditorCommand } from './EditorSession'
import ArrangementTable from '@/components/arrangement/ArrangementTable.vue'
import AdaptiveSelect from '@/components/common/AdaptiveSelect.vue'
import ParameterValueEditor from '@/components/workflow/ParameterValueEditor.vue'
function cloneReactiveValue<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

const props = defineProps<{
  embedded?: boolean
  variables: Variable[]
  types: TypeProjection[]
  blocks?: ParameterBlock[]
}>()
const emit = defineEmits<{ command: [command: EditorCommand] }>()
const { t } = useI18n()
const selected = ref('')
const activeId = ref('')
const selection = ref<string[]>([])
const columns = computed(() => [
  { key: 'icon', label: '', width: '28px' },
  { key: 'name', label: t('workflow.parameters.label') },
])
const available = computed(() =>
  props.variables.filter((v) => !v.parameter).map((v) => ({ label: v.name, value: v.name })),
)
const exposed = computed(() =>
  props.variables
    .filter((v) => v.parameter)
    .sort((a, b) => (a.parameter!.order ?? 0) - (b.parameter!.order ?? 0)),
)
function controls(variable: Variable) {
  const modes = ['auto']
  if (['string', 'number', 'boolean'].includes(typeof variable.default)) modes.push('select')
  if (variable.type.kind === 'list') modes.push('multiselect')
  return modes.map((value) => ({ value, label: t(`workflow.parameters.${value}`) }))
}
function visibilityItems(variable: Variable) {
  return [
    { label: t('workflow.parameters.always_visible'), value: '::always' },
    ...exposed.value
      .filter((v) => v !== variable && typeof v.default === 'boolean' && !v.parameter!.visibleWhen)
      .map((v) => ({
        label: t('workflow.parameters.when_enabled', { name: v.parameter!.label }),
        value: v.parameter!.id,
      })),
  ]
}
function update(variable: Variable, parameter?: Parameter, value: unknown = variable.default) {
  emit('command', {
    kind: 'update-state-variable',
    name: variable.name,
    type: cloneReactiveValue(variable.type),
    defaultValue: cloneReactiveValue(value),
    ...(parameter ? { parameter: cloneReactiveValue(parameter) } : { clearParameter: true }),
  })
}
function expose() {
  const variable = props.variables.find((v) => v.name === selected.value)
  if (!variable) return
  const id = crypto.randomUUID()
  activeId.value = id
  update(variable, {
    id,
    label: variable.name,
    control: 'auto',
    order: Math.max(-1, ...rows.value.map((row) => row.order)) + 1,
  })
  selected.value = ''
}
function textChange(variable: Variable, field: 'label' | 'description' | 'group', event: Event) {
  update(variable, { ...variable.parameter!, [field]: (event.target as HTMLInputElement).value })
}
function changeControl(variable: Variable, control: string) {
  const mode = control as Parameter['control']
  const initial = Array.isArray(variable.default) ? (variable.default[0] ?? '') : variable.default
  update(variable, {
    ...variable.parameter!,
    control: mode,
    options:
      mode === 'auto'
        ? undefined
        : [{ label: String(initial) || t('workflow.parameters.option_label'), value: initial }],
  })
}
function optionChange(variable: Variable, index: number, field: 'label' | 'value', event: Event) {
  const parameter = cloneReactiveValue(variable.parameter!)
  const option = parameter.options![index]
  const text = (event.target as HTMLInputElement).value
  const previous = option.value
  if (field === 'label') option.label = text
  else
    option.value =
      typeof option.value === 'number'
        ? Number(text)
        : typeof option.value === 'boolean'
          ? text === 'true'
          : text
  const replace = (value: unknown) =>
    JSON.stringify(value) === JSON.stringify(previous) ? option.value : value
  const value =
    field === 'value'
      ? Array.isArray(variable.default)
        ? variable.default.map(replace)
        : replace(variable.default)
      : variable.default
  update(variable, parameter, value)
}
function addOption(variable: Variable) {
  const parameter = cloneReactiveValue(variable.parameter!)
  const first = parameter.options?.[0]?.value
  let index = (parameter.options?.length ?? 0) + 1
  let value: unknown =
    typeof first === 'number' ? index : typeof first === 'boolean' ? !first : `option_${index}`
  while (
    typeof value !== 'boolean' &&
    parameter.options?.some((option) => option.value === value)
  ) {
    index++
    value = typeof first === 'number' ? index : `option_${index}`
  }
  if (parameter.options?.some((option) => option.value === value)) return
  parameter.options = [...(parameter.options ?? []), { label: String(value), value }]
  update(variable, parameter)
}
function removeOption(variable: Variable, index: number) {
  const parameter = cloneReactiveValue(variable.parameter!)
  const [removed] = parameter.options!.splice(index, 1)
  const value = Array.isArray(variable.default)
    ? variable.default.filter((item) => JSON.stringify(item) !== JSON.stringify(removed?.value))
    : JSON.stringify(variable.default) === JSON.stringify(removed?.value)
      ? parameter.options![0]!.value
      : variable.default
  update(variable, parameter, value)
}
type Row = {
  id: string
  kind: 'parameter' | 'label' | 'separator'
  label: string
  order: number
  variable?: Variable
}
const rows = computed<Row[]>(() =>
  [
    ...exposed.value.map((variable) => ({
      id: variable.parameter!.id,
      kind: 'parameter' as const,
      label: variable.parameter!.label,
      order: variable.parameter!.order ?? 0,
      variable,
    })),
    ...(props.blocks ?? []).map((block) => ({
      ...block,
      label: block.kind === 'separator' ? t('workflow.parameters.separator') : (block.label ?? ''),
      order: block.order ?? 0,
    })),
  ].sort((a, b) => a.order - b.order),
)
const activeBlocks = computed(() =>
  (props.blocks ?? []).filter((b) => b.id === activeId.value && b.kind === 'label'),
)
function describeBlock(id: string, event: Event) {
  setBlocks(
    (props.blocks ?? []).map((b) =>
      b.id === id ? { ...b, description: (event.target as HTMLTextAreaElement).value } : b,
    ),
  )
}
const activeVariables = computed(() =>
  exposed.value.filter((v) => v.parameter!.id === activeId.value),
)
function setBlocks(blocks: ParameterBlock[]) {
  emit('command', { kind: 'set-parameter-blocks', blocks: cloneReactiveValue(blocks) })
}
function addBlock(kind: ParameterBlock['kind']) {
  const id = crypto.randomUUID()
  setBlocks([
    ...(props.blocks ?? []),
    { id, kind, label: '', order: Math.max(-1, ...rows.value.map((row) => row.order)) + 1 },
  ])
  activeId.value = id
}
function renameBlock(id: string, event: Event) {
  setBlocks(
    (props.blocks ?? []).map((b) =>
      b.id === id ? { ...b, label: (event.target as HTMLInputElement).value } : b,
    ),
  )
}
function reorder(ordered: Row[]) {
  const commands: EditorCommand[] = []
  const blocks: ParameterBlock[] = []
  ordered.forEach((row, order) => {
    if (row.variable)
      commands.push({
        kind: 'update-state-variable',
        name: row.variable.name,
        type: cloneReactiveValue(row.variable.type),
        defaultValue: cloneReactiveValue(row.variable.default),
        parameter: { ...cloneReactiveValue(row.variable.parameter!), order },
      })
    else {
      const block = props.blocks?.find((b) => b.id === row.id)
      if (block) blocks.push({ ...block, order })
    }
  })
  commands.push({ kind: 'set-parameter-blocks', blocks })
  emit('command', { kind: 'batch', commands })
}
function removeRows(ids: string[]) {
  const removedParameters = new Set(
    exposed.value.filter((v) => ids.includes(v.parameter!.id)).map((v) => v.parameter!.id),
  )
  const commands: EditorCommand[] = []
  for (const variable of exposed.value) {
    const parameter = variable.parameter!
    if (
      removedParameters.has(parameter.id) ||
      (parameter.visibleWhen && removedParameters.has(parameter.visibleWhen.parameterId))
    ) {
      commands.push({
        kind: 'update-state-variable',
        name: variable.name,
        type: cloneReactiveValue(variable.type),
        defaultValue: cloneReactiveValue(variable.default),
        ...(removedParameters.has(parameter.id)
          ? { clearParameter: true }
          : { parameter: { ...cloneReactiveValue(parameter), visibleWhen: undefined } }),
      })
    }
  }
  commands.push({
    kind: 'set-parameter-blocks',
    blocks: cloneReactiveValue((props.blocks ?? []).filter((b) => !ids.includes(b.id))),
  })
  emit('command', { kind: 'batch', commands })
}
</script>
