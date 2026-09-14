<template>
  <SettingsSection
    :title="t('settingsServices.title')"
    :description="t('settingsServices.description')"
    icon="i-tabler-world-www"
  >
    <template #badge><SettingsRestartBadge /></template>
    <SettingsRow v-for="field in fields" :key="field" :label="t(`settingsServices.${field}`)">
      <div class="w-full space-y-2">
        <UInput
          v-model="draft[field]"
          class="w-full"
          :placeholder="DEFAULT_SERVICE_ADDRESSES[field]"
          :aria-label="t(`settingsServices.${field}`)"
          :aria-invalid="!validServiceEndpoint(draft[field])"
          :aria-describedby="`service-address-${field}-hint`"
          :disabled="saving"
          :maxlength="2048"
          spellcheck="false"
          @keydown.enter.prevent="save"
        />
        <p
          :id="`service-address-${field}-hint`"
          class="text-xs leading-5"
          :class="validServiceEndpoint(draft[field]) ? 'text-muted' : 'text-error'"
        >
          {{
            validServiceEndpoint(draft[field])
              ? t('settingsServices.default_hint')
              : t('settingsServices.invalid')
          }}
        </p>
      </div>
    </SettingsRow>
    <div class="flex flex-wrap items-center justify-end gap-2 px-4 py-3">
      <UButton color="neutral" variant="soft" :disabled="saving" @click="restore">{{
        t('settingsServices.restore')
      }}</UButton>
      <UButton
        icon="i-tabler-device-floppy"
        :loading="saving"
        :disabled="!valid || !dirty"
        @click="save"
        >{{ t('settingsServices.save') }}</UButton
      >
    </div>
    <p v-if="saved && !dirty" class="px-4 pb-4 text-xs text-muted" role="status">
      {{ t('settingsServices.saved') }}
    </p>
  </SettingsSection>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSettingsStore } from '@/stores/settings'
import { DEFAULT_SERVICE_ADDRESSES, validServiceEndpoint } from '@/settings/serviceEndpoint'
import SettingsSection from './SettingsSection.vue'
import SettingsRow from './SettingsRow.vue'
import SettingsRestartBadge from './SettingsRestartBadge.vue'

const { t } = useI18n()
const store = useSettingsStore()
const fields = ['hubURL', 'registryURL'] as const
const draft = reactive({ hubURL: '', registryURL: '' })
const saving = ref(false)
const saved = ref(false)
watch(
  () => store.data?.onlineServices,
  (value) => {
    draft.hubURL = value?.hubURL ?? ''
    draft.registryURL = value?.registryURL ?? ''
  },
  { immediate: true, deep: true },
)
const valid = computed(() => fields.every((field) => validServiceEndpoint(draft[field])))
const dirty = computed(() =>
  fields.some((field) => draft[field] !== (store.data?.onlineServices?.[field] ?? '')),
)

async function save() {
  if (saving.value || !valid.value || !dirty.value) return
  await persist({
    hubURL: draft.hubURL.trim().replace(/\/+$/, ''),
    registryURL: draft.registryURL.trim().replace(/\/+$/, ''),
  })
}
async function restore() {
  if (saving.value) return
  await persist({ hubURL: '', registryURL: '' })
}
async function persist(onlineServices: typeof draft) {
  saving.value = true
  saved.value = false
  try {
    if (await store.patch({ onlineServices })) {
      Object.assign(draft, onlineServices)
      saved.value = true
    }
  } finally {
    saving.value = false
  }
}
</script>
