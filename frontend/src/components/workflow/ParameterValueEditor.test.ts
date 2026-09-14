import { createApp, defineComponent, h, reactive, nextTick } from 'vue'
import { createI18n } from 'vue-i18n'
import { afterEach, expect, it, vi } from 'vitest'
import authoring from '../../../../contracts/node/current/builtin-authoring'
import type { TypeProjection } from '../../../../contracts/node/current/authoring-projection'
import type { Variable } from '../../../../contracts/workflow/current/workflow-source'
import ParameterValueEditor from './ParameterValueEditor.vue'

vi.mock('@/components/common/AdaptiveSelect.vue', () => ({
  default: defineComponent({
    props: ['modelValue', 'items'],
    emits: ['update:modelValue'],
    setup:
      (props, { emit }) =>
      () =>
        h(
          'select',
          {
            value: props.modelValue,
            onChange: (event: Event) =>
              emit('update:modelValue', Number((event.target as HTMLSelectElement).value)),
          },
          props.items.map((item: { value: number; label: string }) =>
            h('option', { value: item.value }, item.label),
          ),
        ),
  }),
}))
const applications: ReturnType<typeof createApp>[] = []
afterEach(() => {
  applications.splice(0).forEach((app) => app.unmount())
  document.body.replaceChildren()
})

it('reports invalid JSON drafts before they can be mistaken for saved values', async () => {
  const types = reactive(authoring.body.types as unknown as TypeProjection[])
  const type = types.find((item) => item.typeRef.typeId.includes('/core/json/'))!
  const variable: Variable = {
    name: 'options',
    type: { kind: 'ref', ref: type.typeRef },
    default: {},
    parameter: { id: 'options-id', label: 'Options', control: 'auto' },
  }
  const validity = vi.fn(),
    changed = vi.fn()
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () =>
      h(ParameterValueEditor, {
        variable,
        types,
        modelValue: {},
        onValidity: validity,
        'onUpdate:modelValue': changed,
      }),
  })
  app.component(
    'UTextarea',
    defineComponent({
      props: ['modelValue'],
      emits: ['update:modelValue', 'blur'],
      setup:
        (props, { emit }) =>
        () =>
          h('textarea', {
            value: props.modelValue,
            onInput: (event: Event) =>
              emit('update:modelValue', (event.target as HTMLTextAreaElement).value),
            onBlur: () => emit('blur'),
          }),
    }),
  )
  app.use(
    createI18n({
      legacy: false,
      locale: 'en',
      messages: { en: { workflow: { state_panel: { invalid_initial_json: 'Invalid JSON' } } } },
    }),
  )
  applications.push(app)
  app.mount(host)
  const textarea = host.querySelector('textarea')!
  textarea.value = '{'
  textarea.dispatchEvent(new Event('input'))
  await nextTick()
  expect(validity).toHaveBeenLastCalledWith(false)
  textarea.dispatchEvent(new Event('blur'))
  expect(changed).not.toHaveBeenCalled()
  textarea.value = '{"count":3}'
  textarea.dispatchEvent(new Event('input'))
  textarea.dispatchEvent(new Event('blur'))
  await nextTick()
  expect(validity).toHaveBeenLastCalledWith(true)
  expect(changed).toHaveBeenLastCalledWith({ count: 3 })
})

it('uses stable stored values for labeled choices and accepts reactive type projections', () => {
  const types = reactive(authoring.body.types as unknown as TypeProjection[])
  const type = types.find((item) => item.typeRef.typeId.includes('/core/string/'))!
  const variable: Variable = {
    name: 'person',
    type: { kind: 'ref', ref: type.typeRef },
    default: 'a',
    parameter: {
      id: 'person-id',
      label: 'Person',
      control: 'select',
      options: [
        { label: 'Alice', value: 'a' },
        { label: 'Bob', value: 'b' },
      ],
    },
  }
  const changed = vi.fn()
  const host = document.createElement('div')
  document.body.append(host)
  const app = createApp({
    render: () =>
      h(ParameterValueEditor, { variable, types, modelValue: 'a', 'onUpdate:modelValue': changed }),
  })
  app.use(createI18n({ legacy: false, locale: 'en', messages: { en: {} } }))
  applications.push(app)
  app.mount(host)
  const select = host.querySelector('select')!
  expect(select.options[1]?.textContent).toBe('Bob')
  select.value = '1'
  select.dispatchEvent(new Event('change'))
  expect(changed).toHaveBeenCalledWith('b')
})
