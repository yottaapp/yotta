<template>
  <KeyChordValueEditor
    v-if="editorAdapter === 'key-chord'"
    :model-value="keyChordValue"
    @update:model-value="emit('update:model-value', $event)"
  />
  <USwitch
    v-else-if="type?.control === 'toggle'"
    :model-value="Boolean(modelValue)"
    size="sm"
    @update:model-value="emit('update:model-value', $event)"
  />
  <UInputNumber
    v-else-if="type?.control === 'number' || type?.control === 'integer'"
    :model-value="numericValue"
    :min="numericConstraint(type.constraints.minimum)"
    :max="numericConstraint(type.constraints.maximum)"
    :step="type.control === 'integer' ? 1 : 'any'"
    size="sm"
    class="w-full"
    @update:model-value="emit('update:model-value', Number($event))"
  />
  <AdaptiveSelect
    v-else-if="type?.control === 'select'"
    :model-value="selectValue"
    :items="selectItems"
    width-mode="fill"
    size="sm"
    @update:model-value="emit('update:model-value', $event)"
  />
  <UInput
    v-else-if="type?.control === 'text'"
    :model-value="textValue"
    size="sm"
    class="w-full"
    @change="setText"
  />
  <div v-else class="space-y-1">
    <UTextarea
      :model-value="jsonDraft"
      :rows="2"
      size="sm"
      class="w-full font-mono text-xs"
      @update:model-value="setJSONDraft"
      @blur="commitJSON"
    />
    <p
      v-if="jsonError"
      data-testid="workflow-state-invalid-json"
      class="text-[11px] text-error"
      role="alert"
    >
      {{ t('workflow.state_panel.invalid_initial_json') }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TypeProjection } from '../../../../contracts/node/current/authoring-projection'
import AdaptiveSelect from '@/components/common/AdaptiveSelect.vue'

const KeyChordValueEditor = defineAsyncComponent(() => import('./KeyChordValueEditor.vue'))

const props = defineProps<{
  modelValue: unknown
  type?: TypeProjection
  editorAdapter?: 'key-chord'
}>()
const emit = defineEmits<{ 'update:model-value': [value: unknown]; validity: [valid: boolean] }>()
const { t } = useI18n()

const numericValue = computed(() =>
  typeof props.modelValue === 'number' ? props.modelValue : undefined,
)
const keyChordValue = computed(() =>
  Array.isArray(props.modelValue)
    ? props.modelValue.filter((value): value is string => typeof value === 'string')
    : [],
)
const textValue = computed(() => (typeof props.modelValue === 'string' ? props.modelValue : ''))
const selectValue = computed<string | number | boolean | null>(() => {
  const value = props.modelValue
  return typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean'
    ? value
    : null
})
const selectItems = computed(() =>
  (props.type?.constraints.enum ?? [])
    .filter((value): value is string | number | boolean =>
      ['string', 'number', 'boolean'].includes(typeof value),
    )
    .map((value) => ({ label: String(value), value })),
)
const jsonDraft = ref('')
const jsonError = ref(false)

watch(
  () => props.modelValue,
  (value) => {
    jsonDraft.value = JSON.stringify(value ?? null, null, 2)
    jsonError.value = false
  },
  { immediate: true, deep: true },
)

function numericConstraint(value: unknown): number | undefined {
  return typeof value === 'number' ? value : undefined
}

function setText(event: Event): void {
  emit('update:model-value', (event.target as HTMLInputElement).value)
}

function setJSONDraft(value: string | number): void {
  jsonDraft.value = String(value)
  try {
    JSON.parse(jsonDraft.value)
    emit('validity', true)
  } catch {
    emit('validity', false)
  }
}

function commitJSON(): void {
  try {
    const value: unknown = JSON.parse(jsonDraft.value)
    jsonError.value = false
    emit('validity', true)
    emit('update:model-value', value)
  } catch {
    jsonError.value = true
    emit('validity', false)
  }
}
</script>
