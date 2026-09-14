import { createApp, h, nextTick, ref } from 'vue'
import ui from '@nuxt/ui/vue-plugin'
import { describe, it, expect } from 'vitest'
import { i18n } from '@/i18n'
import type { SalesDraft } from '@/lib/publication'
import WorkflowPublicationPricing from './WorkflowPublicationPricing.vue'
import WorkflowPublicationStatus from './WorkflowPublicationStatus.vue'

describe('workflow publication UI', () => {
  it('accepts paid pricing without a terms input', async () => {
    const draft = ref<SalesDraft>({
      paid: false,
      price: '',
      revision: 0,
      available: true,
      configured: false,
    })
    const host = document.createElement('div')
    const app = createApp({
      render: () =>
        h(WorkflowPublicationPricing, {
          modelValue: draft.value,
          'onUpdate:modelValue': (value) => {
            draft.value = value
          },
          disabled: false,
        }),
    })
    document.body.append(host)
    app.use(ui).use(i18n).mount(host)
    expect(host.querySelector('[data-testid="publication-price"]')).toBeNull()
    draft.value.paid = true
    await nextTick()
    for (const [testId, value] of [['publication-price', '12.34']]) {
      const input = host.querySelector<HTMLInputElement>(`[data-testid="${testId}"]`)!
      input.value = value!
      input.dispatchEvent(new Event('input'))
    }
    await nextTick()
    expect(draft.value).toMatchObject({
      paid: true,
      price: '12.34',
    })
    expect(host.querySelector('[data-testid="publication-license"]')).toBeNull()
    app.unmount()
    host.remove()
  })

  it.each([
    ['pending_review', '已提交，待审核'],
    ['approved', '最近投稿已通过审核'],
    ['rejected', '最近投稿未通过审核'],
    ['published', '已发布到市场'],
    ['future_state', '投稿已接收'],
  ])('displays %s without claiming it is published', (status, label) => {
    const host = document.createElement('div')
    const app = createApp({
      render: () =>
        h(WorkflowPublicationStatus, {
          status,
          reason: status === 'rejected' ? '请补充使用说明' : undefined,
        }),
    })
    app.use(i18n).mount(host)
    expect(host.textContent).toContain(label)
    if (status === 'rejected') expect(host.textContent).toContain('请补充使用说明')
    if (status === 'pending_review') expect(host.textContent).not.toContain('已发布到市场')
    app.unmount()
  })
})
