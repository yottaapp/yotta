<template>
  <div class="space-y-4">
    <UFormField :label="t('workflow.market.pricing')">
      <USelect
        v-model="pricingMode"
        data-testid="publication-pricing"
        :items="[
          { label: t('workflow.market.pricing_free'), value: 'free' },
          { label: t('workflow.market.pricing_paid'), value: 'paid' },
        ]"
        :disabled="disabled"
      />
    </UFormField>
    <UFormField
      v-if="draft.paid"
      :label="t('workflow.market.price_cny')"
      required
      :error="
        draft.price && priceCents(draft.price) === null
          ? t('workflow.market.invalid_price')
          : undefined
      "
    >
      <UInput
        v-model="draft.price"
        data-testid="publication-price"
        inputmode="decimal"
        :disabled="disabled"
      />
    </UFormField>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { priceCents, type SalesDraft } from '@/lib/publication'
const draft = defineModel<SalesDraft>({ required: true })
defineProps<{ disabled: boolean }>()
const { t } = useI18n()
const pricingMode = computed({
  get: () => (draft.value.paid ? 'paid' : 'free'),
  set: (value: string) => {
    draft.value = { ...draft.value, paid: value === 'paid' }
  },
})
</script>
