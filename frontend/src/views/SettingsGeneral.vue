<template>
  <div class="settings-page">
    <SettingsSection
      :title="t('settings.general.appearance_title')"
      icon="i-tabler-layout-dashboard"
    >
      <SettingsRow :label="t('settings.language')" :hint="t('settings.language_restart_hint')">
        <template #meta><SettingsRestartBadge /></template>
        <AdaptiveSelect
          :model-value="currentLocale"
          :items="localeItems"
          :aria-label="t('settings.language')"
          @update:model-value="onLocaleChange"
        />
      </SettingsRow>
    </SettingsSection>

    <SettingsSection :title="t('settings.startup.section_title')" icon="i-tabler-power">
      <SettingsRow :label="t('settings.startup.autostart_label')">
        <USwitch
          :model-value="autostart"
          :aria-label="t('settings.startup.autostart_label')"
          @update:model-value="onToggleAutostart"
        />
      </SettingsRow>

      <div class="border-t border-default/60" />

      <SettingsRow :label="t('settings.startup.tray_label')">
        <USwitch
          :model-value="minimizeToTray"
          :aria-label="t('settings.startup.tray_label')"
          @update:model-value="onToggleMinimizeToTray"
        />
      </SettingsRow>
    </SettingsSection>

    <SettingsSection
      :title="t('settings.general.capture_diagnostics_title')"
      icon="i-tabler-activity-heartbeat"
    >
      <SettingsRow :label="t('settings.capture.section_title')" :hint="captureMethodHint">
        <template #meta><SettingsRestartBadge /></template>
        <AdaptiveSelect
          :model-value="currentCapture"
          :items="captureItems"
          :aria-label="t('settings.capture.section_title')"
          @update:model-value="onCaptureChange"
        />
      </SettingsRow>

      <div class="border-t border-default/60" />

      <SettingsRow
        :label="t('settings.capture.dump_debug_label')"
        :hint="t('settings.capture.dump_debug_hint')"
      >
        <USwitch
          :model-value="dumpDebug"
          :aria-label="t('settings.capture.dump_debug_label')"
          @update:model-value="onDumpDebugChange"
        />
      </SettingsRow>

      <div class="border-t border-default/60" />

      <SettingsRow :label="t('settings.log.enabled_label')">
        <USwitch
          :model-value="loggerEnabled"
          :aria-label="t('settings.log.enabled_label')"
          @update:model-value="(value: boolean) => patchLogger('enabled', value)"
        />
      </SettingsRow>

      <div class="border-t border-default/60" />

      <SettingsRow :label="t('settings.log.level_label')" :hint="t('settings.log.level_hint')">
        <AdaptiveSelect
          :model-value="loggerLevel"
          :items="logLevelItems"
          :aria-label="t('settings.log.level_label')"
          @update:model-value="(value: string) => patchLogger('level', value)"
        />
      </SettingsRow>

      <div class="border-t border-default/60" />

      <SettingsRow :label="t('settings.log.live_label')">
        <USwitch
          :model-value="loggerLiveView"
          :aria-label="t('settings.log.live_label')"
          @update:model-value="(value: boolean) => patchLogger('liveView', value)"
        />
      </SettingsRow>
    </SettingsSection>

    <SettingsSection :title="t('settings.general.data_title')" icon="i-tabler-database">
      <SettingsRow :label="t('settings.general.app_data')">
        <template #meta>
          <UPopover
            mode="hover"
            :content="{ align: 'start', side: 'top' }"
            :ui="{ content: 'w-96 max-w-[calc(100vw-2rem)] max-h-[50vh] overflow-y-auto p-4' }"
          >
            <UButton
              size="xs"
              color="neutral"
              variant="ghost"
              icon="i-tabler-help-circle"
              :aria-label="t('settings.general.data_help')"
              data-testid="app-data-help"
            />
            <template #content>
              <dl class="space-y-3 text-xs" data-testid="app-data-folders">
                <div v-for="folder in dataFolders" :key="folder.name" class="space-y-1">
                  <dt class="font-medium text-highlighted">{{ folder.name }}</dt>
                  <dd class="leading-5 text-muted">{{ t(folder.hint) }}</dd>
                </div>
              </dl>
            </template>
          </UPopover>
        </template>
        <UButton
          color="neutral"
          variant="soft"
          icon="i-tabler-folder"
          :loading="openingDataFolder"
          data-testid="open-app-data-folder"
          @click="openDataFolder"
          >{{ t('settingsPlugins.open_folder') }}</UButton
        >
      </SettingsRow>
    </SettingsSection>
    <SettingsServiceAddresses />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { pluginBackend } from '@/lib/plugins'
import { errorMessage } from '@/lib/invoke'
import { useToast } from '@/composables/useAppToast'
import { useSettingsStore } from '@/stores/settings'
import { setLocale, type Locale } from '@/i18n'
import SettingsRestartBadge from '@/components/settings/SettingsRestartBadge.vue'
import SettingsRow from '@/components/settings/SettingsRow.vue'
import SettingsSection from '@/components/settings/SettingsSection.vue'
import SettingsServiceAddresses from '@/components/settings/SettingsServiceAddresses.vue'
import AdaptiveSelect from '@/components/common/AdaptiveSelect.vue'

const { t } = useI18n()
const settingsStore = useSettingsStore()
const toast = useToast()
const dataFolders = [
  { name: 'config', hint: 'settings.general.folder_config' },
  { name: 'data', hint: 'settings.general.folder_data' },
  { name: 'catalog', hint: 'settings.general.folder_catalog' },
  { name: 'objects', hint: 'settings.general.folder_objects' },
  { name: 'packages', hint: 'settings.general.folder_packages' },
  { name: 'state', hint: 'settings.general.folder_state' },
  { name: 'documents', hint: 'settings.general.folder_documents' },
  { name: 'diagnostics', hint: 'settings.general.folder_diagnostics' },
  { name: 'backups', hint: 'settings.general.folder_backups' },
  { name: 'cache', hint: 'settings.general.folder_cache' },
  { name: 'runtime', hint: 'settings.general.folder_runtime' },
  { name: 'tmp', hint: 'settings.general.folder_tmp' },
  { name: 'webview-event-runtime', hint: 'settings.general.folder_webview' },
]
const openingDataFolder = ref(false)
async function openDataFolder() {
  if (openingDataFolder.value) return
  openingDataFolder.value = true
  try {
    await pluginBackend.openLocation('root')
  } catch (error) {
    toast.add({ description: errorMessage(error), color: 'error' })
  } finally {
    openingDataFolder.value = false
  }
}

const currentLocale = computed(() => (settingsStore.data?.locale ?? 'zh') as Locale)
const localeItems = computed(() => [
  { label: t('settings.language_zh'), value: 'zh' },
  { label: t('settings.language_en'), value: 'en' },
])

async function onLocaleChange(value: string) {
  const ok = await settingsStore.patch({ locale: value })
  if (!ok) return
  if (!(await setLocale(value as Locale))) {
    toast.add({ title: t('settings.language_load_failed'), color: 'error' })
    return
  }
  if (value === 'en') {
    toast.add({
      title: t('toast.lang_en_warn_title'),
      description: t('toast.lang_en_warn_desc'),
      icon: 'i-tabler-alert-triangle',
      color: 'warning',
    })
  }
}

const currentCapture = computed(() => settingsStore.data?.capture?.method ?? 'auto')
const captureItems = computed(() => [
  { label: t('settings.capture.method.auto'), value: 'auto' },
  { label: t('settings.capture.method.gdi'), value: 'gdi' },
  { label: t('settings.capture.method.wgc'), value: 'wgc' },
  { label: t('settings.capture.method.mock'), value: 'mock' },
])
const captureMethodHint = computed(() => t(`settings.capture.method_hint.${currentCapture.value}`))

function onCaptureChange(value: string) {
  void settingsStore.patch({ capture: { method: value } })
}

const dumpDebug = computed(() => settingsStore.data?.capture?.dumpDebug ?? false)
function onDumpDebugChange(value: boolean) {
  void settingsStore.patch({ capture: { dumpDebug: value } })
}

const autostart = computed(() => settingsStore.data?.ui.autostart ?? false)
const minimizeToTray = computed(() => settingsStore.data?.ui.minimizeToTray ?? false)
function onToggleAutostart(value: boolean) {
  void settingsStore.patch({ ui: { autostart: value } })
}
function onToggleMinimizeToTray(value: boolean) {
  void settingsStore.patch({ ui: { minimizeToTray: value } })
}

const loggerEnabled = computed(() => settingsStore.data?.ui.logger.enabled ?? true)
const loggerLiveView = computed(() => settingsStore.data?.ui.logger.liveView ?? true)
const loggerLevel = computed(() => settingsStore.data?.ui.logger.level ?? 'info')
const logLevelItems = computed(() => [
  { label: 'DEBUG', value: 'debug' },
  { label: 'INFO', value: 'info' },
  { label: 'WARN', value: 'warn' },
  { label: 'ERROR', value: 'error' },
])
function patchLogger(field: string, value: string | boolean) {
  void settingsStore.patch({ ui: { logger: { [field]: value } } })
}
</script>
