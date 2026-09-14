<template>
  <USwitch
    v-if="parameter?.control === 'auto' && typeof variable.default === 'boolean'"
    :model-value="Boolean(modelValue)"
    :aria-label="parameter.label"
    @update:model-value="emit('update:model-value', $event)"
  />
  <AdaptiveSelect
    v-else-if="parameter?.control === 'select'"
    :model-value="selectedIndex"
    :items="options"
    width-mode="fill"
    @update:model-value="selectOne(Number($event))"
  />
  <div v-else-if="parameter?.control === 'multiselect'" class="space-y-2">
    <UCheckbox
      v-for="(option, index) in parameter.options"
      :key="index"
      :label="option.label"
      :model-value="isSelected(option.value)"
      @update:model-value="toggle(option.value, Boolean($event))"
    />
  </div>
  <StateDefaultValueEditor
    v-else
    :model-value="modelValue"
    :type="choice?.projection"
    :editor-adapter="choice?.editorAdapter"
    @update:model-value="emit('update:model-value', $event)"
    @validity="emit('validity', $event)"
  />
</template>
<script setup lang="ts">
import { computed, toRaw } from 'vue'
import type { Variable } from '../../../../contracts/workflow/current/workflow-source'
import type { TypeProjection } from '../../../../contracts/node/current/authoring-projection'
import AdaptiveSelect from '@/components/common/AdaptiveSelect.vue'
import StateDefaultValueEditor from '@/app/editor/StateDefaultValueEditor.vue'
import {
  buildStateTypeChoices,
  stateTypeChoiceForExpression,
} from '@/app/editor/stateVariableTypes'
const props = defineProps<{ variable: Variable; modelValue: unknown; types: TypeProjection[] }>()
const emit = defineEmits<{ 'update:model-value': [value: unknown]; validity: [valid: boolean] }>()
const parameter = computed(() => props.variable.parameter)
const choice = computed(() =>
  stateTypeChoiceForExpression(
    buildStateTypeChoices(props.types.map((type) => toRaw(type))),
    props.variable.type,
  ),
)
const options = computed(() =>
  (parameter.value?.options ?? []).map((option, value) => ({ label: option.label, value })),
)
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)
const selectedIndex = computed(
  () => parameter.value?.options?.findIndex((option) => same(option.value, props.modelValue)) ?? -1,
)
function selectOne(index: number) {
  if (parameter.value?.options?.[index])
    emit('update:model-value', parameter.value.options[index].value)
}
function isSelected(value: unknown) {
  return Array.isArray(props.modelValue) && props.modelValue.some((item) => same(item, value))
}
function toggle(value: unknown, checked: boolean) {
  const items = Array.isArray(props.modelValue)
    ? props.modelValue.filter((item) => !same(item, value))
    : []
  emit('update:model-value', checked ? [...items, value] : items)
}
</script>
