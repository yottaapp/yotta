<template>
  <section class="settings-page" data-testid="settings-plugins" :aria-busy="loading || !!busy">
    <SettingsSection
      :title="t('settingsPlugins.title')"
      :description="t('settingsPlugins.description')"
      icon="i-tabler-puzzle"
    >
      <template #actions>
        <div class="flex gap-2">
          <UButton
            size="sm"
            color="neutral"
            variant="ghost"
            icon="i-tabler-refresh"
            :disabled="loading || !!busy"
            @click="refresh(true)"
            >{{ t('common.refresh') }}</UButton
          >
          <UButton
            size="sm"
            icon="i-tabler-plus"
            data-testid="plugin-import"
            :loading="busy === 'import'"
            :disabled="!!busy"
            @click="importPlugin"
            >{{ t('settingsPlugins.import') }}</UButton
          >
        </div>
      </template>
      <div class="space-y-4">
        <UAlert
          v-if="failure"
          color="error"
          variant="soft"
          :title="t('settingsPlugins.failed')"
          :description="failure"
          role="alert"
        />
        <UAlert
          v-if="restartRequired"
          color="warning"
          variant="soft"
          icon="i-tabler-refresh"
          :title="t('settingsPlugins.restart_title')"
          :description="t('settingsPlugins.restart_hint')"
        />

        <div class="flex flex-wrap items-center justify-between gap-3">
          <div
            class="flex flex-wrap gap-1"
            role="group"
            :aria-label="t('settingsPlugins.filter_label')"
          >
            <UButton
              v-for="option in filters"
              :key="option.key"
              size="sm"
              :color="filter === option.key ? 'primary' : 'neutral'"
              :variant="filter === option.key ? 'soft' : 'ghost'"
              :aria-pressed="filter === option.key"
              @click="filter = option.key"
            >
              {{ t(option.label) }} <span class="ml-1 tabular-nums">{{ option.count }}</span>
            </UButton>
          </div>
          <UInput
            v-model="query"
            icon="i-tabler-search"
            class="w-full sm:w-64"
            :placeholder="t('settingsPlugins.search')"
            :aria-label="t('settingsPlugins.search')"
            data-testid="plugin-search"
          />
        </div>

        <div
          v-if="selectedIds.length"
          class="flex flex-wrap items-center gap-2 rounded-lg border border-primary/25 bg-primary/5 px-3 py-2"
          data-testid="plugin-bulk-toolbar"
        >
          <div class="mr-auto min-w-0 text-xs">
            <span class="font-medium text-highlighted">{{
              t('settingsPlugins.selected', { count: selectedIds.length })
            }}</span>
            <span v-if="hiddenSelected" class="ml-2 text-muted">{{
              t('settingsPlugins.hidden_selected', { count: hiddenSelected })
            }}</span>
          </div>
          <UButton
            size="sm"
            color="neutral"
            variant="soft"
            data-testid="plugin-bulk-enable"
            :loading="busy === 'batch:enable'"
            :disabled="!!busy"
            @click="runBatch('enable')"
            >{{ t('settingsPlugins.enable') }}</UButton
          >
          <UButton
            size="sm"
            color="neutral"
            variant="soft"
            data-testid="plugin-bulk-disable"
            :loading="busy === 'batch:disable'"
            :disabled="!!busy"
            @click="runBatch('disable')"
            >{{ t('settingsPlugins.disable') }}</UButton
          >
          <UButton
            size="sm"
            color="error"
            variant="soft"
            data-testid="plugin-bulk-uninstall"
            :loading="busy === 'batch:uninstall'"
            :disabled="!!busy"
            @click="runBatch('uninstall')"
            >{{ t('settingsPlugins.uninstall') }}</UButton
          >
          <UButton
            size="sm"
            color="neutral"
            variant="ghost"
            :disabled="!!busy"
            @click="selectedIds = []"
            >{{ t('settingsPlugins.clear_selection') }}</UButton
          >
        </div>

        <div
          v-if="batchResults.length"
          class="space-y-2 rounded-lg border border-default px-3 py-3"
          data-testid="plugin-batch-results"
          role="status"
        >
          <div class="flex flex-wrap items-center justify-between gap-2">
            <p
              class="text-xs font-medium"
              :class="failedResults.length ? 'text-warning' : 'text-primary'"
            >
              {{
                t('settingsPlugins.batch_summary', {
                  succeeded: batchResults.length - failedResults.length,
                  failed: failedResults.length,
                })
              }}
            </p>
            <div class="flex gap-2">
              <UButton
                v-if="failedResults.length"
                size="xs"
                color="neutral"
                variant="soft"
                :disabled="!!busy"
                @click="
                  runBatch(
                    lastAction,
                    failedResults.map((result) => result.id),
                  )
                "
                >{{ t('settingsPlugins.retry_failed') }}</UButton
              >
              <UButton size="xs" color="neutral" variant="ghost" @click="batchResults = []">{{
                t('common.close')
              }}</UButton>
            </div>
          </div>
          <ul v-if="failedResults.length" class="space-y-2 text-xs">
            <li v-for="result in failedResults" :key="result.id" class="space-y-1">
              <span class="font-medium text-highlighted">{{
                resultNames[result.id] || result.id
              }}</span>
              <p class="break-words text-muted">{{ batchError(result) }}</p>
            </li>
          </ul>
        </div>

        <p v-if="loading" class="py-8 text-sm text-muted" role="status">
          {{ t('common.loading') }}
        </p>
        <div
          v-else-if="!items.length"
          class="space-y-2 rounded-lg border border-dashed border-default py-12 text-center"
        >
          <UIcon name="i-tabler-puzzle" class="size-8 text-muted" />
          <h3 class="text-sm font-medium text-highlighted">{{ t('settingsPlugins.empty') }}</h3>
          <p class="text-xs text-muted">{{ t('settingsPlugins.empty_hint') }}</p>
        </div>
        <div v-else class="overflow-x-auto rounded-lg border border-default">
          <table class="w-full text-left text-xs" :aria-label="t('settingsPlugins.table_label')">
            <thead class="border-b border-default bg-elevated/60 text-muted">
              <tr>
                <th scope="col" class="w-10 px-3 py-3">
                  <UCheckbox
                    :model-value="visibleSelection"
                    :disabled="!!busy || !visibleItems.length"
                    :aria-label="t('settingsPlugins.select_visible')"
                    data-testid="plugin-select-all"
                    @update:model-value="selectVisible($event === true)"
                  />
                </th>
                <th scope="col" class="px-2 py-3 font-medium">
                  {{ t('settingsPlugins.column_plugin') }}
                </th>
                <th scope="col" class="w-32 px-3 py-3 font-medium">
                  {{ t('settingsPlugins.column_status') }}
                </th>
                <th scope="col" class="w-44 px-3 py-3 text-right font-medium">
                  {{ t('settingsPlugins.column_actions') }}
                </th>
              </tr>
            </thead>
            <tbody class="divide-y divide-default">
              <template v-for="item in visibleItems" :key="item.id">
                <tr
                  data-testid="plugin-item"
                  :data-plugin-id="item.id"
                  :aria-selected="selected.has(item.id)"
                  :class="selected.has(item.id) ? 'bg-primary/5' : 'hover:bg-elevated/30'"
                >
                  <td class="px-3 py-4 align-top">
                    <UCheckbox
                      :model-value="selected.has(item.id)"
                      :disabled="!!busy"
                      :aria-label="t('settingsPlugins.select_plugin', { name: item.name })"
                      @update:model-value="toggleSelection(item.id, $event === true)"
                    />
                  </td>
                  <td class="min-w-44 max-w-md px-2 py-4">
                    <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                      <button
                        type="button"
                        class="break-words text-left text-sm font-semibold text-highlighted hover:text-primary focus-visible:outline-2 focus-visible:outline-primary"
                        :aria-expanded="expanded.has(item.id)"
                        @click="toggleDetails(item.id)"
                      >
                        {{ item.name }}
                      </button>
                      <span class="text-muted">v{{ item.version }}</span>
                    </div>
                    <p class="mt-1 line-clamp-2 leading-5 text-muted">{{ item.description }}</p>
                    <p class="mt-1 text-muted">
                      {{
                        t('settingsPlugins.contributions', {
                          nodes: item.nodes.length,
                          workflows: item.workflows.length,
                        })
                      }}
                    </p>
                  </td>
                  <td class="px-3 py-4 align-top">
                    <UBadge
                      size="sm"
                      :color="item.enabled ? 'primary' : 'neutral'"
                      variant="soft"
                      >{{
                        t(item.enabled ? 'settingsPlugins.enabled' : 'settingsPlugins.disabled')
                      }}</UBadge
                    >
                    <p
                      class="mt-2 leading-5"
                      :class="item.restartRequired ? 'text-warning' : 'text-muted'"
                    >
                      {{
                        t(
                          item.restartRequired
                            ? 'settingsPlugins.filter_pending'
                            : item.loaded
                              ? 'settingsPlugins.loaded'
                              : 'settingsPlugins.not_loaded',
                        )
                      }}
                    </p>
                    <span v-if="inUse(item)" class="text-warning">{{
                      t('settingsPlugins.active_run')
                    }}</span>
                  </td>
                  <td class="px-3 py-4 align-top">
                    <div class="flex flex-wrap justify-end gap-1">
                      <UButton
                        size="xs"
                        color="neutral"
                        variant="soft"
                        :disabled="!!busy || inUse(item)"
                        @click="
                          perform(item.id, () => pluginBackend.setEnabled(item.id, !item.enabled))
                        "
                        >{{
                          t(item.enabled ? 'settingsPlugins.disable' : 'settingsPlugins.enable')
                        }}</UButton
                      >
                      <UButton
                        size="xs"
                        color="neutral"
                        variant="ghost"
                        :aria-expanded="expanded.has(item.id)"
                        @click="toggleDetails(item.id)"
                        >{{
                          t(
                            expanded.has(item.id)
                              ? 'settingsPlugins.collapse'
                              : 'settingsPlugins.details',
                          )
                        }}</UButton
                      >
                      <UButton
                        size="xs"
                        color="error"
                        variant="ghost"
                        :disabled="!!busy || inUse(item)"
                        @click="uninstall(item)"
                        >{{ t('settingsPlugins.uninstall') }}</UButton
                      >
                    </div>
                  </td>
                </tr>
                <tr
                  v-if="expanded.has(item.id)"
                  class="bg-elevated/25"
                  data-testid="plugin-details"
                >
                  <td colspan="4" class="px-5 py-4">
                    <PluginDetails
                      :item="item"
                      :busy="!!busy"
                      @rollback="perform(item.id, () => pluginBackend.rollback(item.id))"
                      @control="
                        (id, start) =>
                          perform(item.id, () => pluginBackend.control(item.id, id, start))
                      "
                    />
                  </td>
                </tr>
              </template>
              <tr v-if="!visibleItems.length">
                <td colspan="4" class="space-y-3 py-10 text-center text-muted">
                  <p>{{ t('settingsPlugins.no_matches') }}</p>
                  <UButton size="xs" color="neutral" variant="soft" @click="resetFilters">{{
                    t('settingsPlugins.reset_filters')
                  }}</UButton>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </SettingsSection>
  </section>
</template>

<script setup lang="ts">
import { computed, onActivated, onDeactivated, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  pluginBackend,
  loadPluginMessages,
  type PluginView,
  type PluginBatchAction,
  type PluginBatchResult,
} from '@/lib/plugins'
import { errorMessage, RPCError } from '@/lib/invoke'
import { useConfirm } from '@/composables/useConfirm'
import { useSettingsStore } from '@/stores/settings'
import PluginDetails from './PluginDetails.vue'
import SettingsSection from '@/components/settings/SettingsSection.vue'

const { t } = useI18n()
const { confirm } = useConfirm()
const settings = useSettingsStore()
const items = ref<PluginView[]>([])
const loading = ref(true)
const busy = ref('')
const failure = ref('')
const restartRequired = ref(false)
const query = ref('')
const filter = ref<'all' | 'enabled' | 'disabled' | 'pending'>('all')
const selectedIds = ref<string[]>([])
const selected = computed(() => new Set(selectedIds.value))
const expandedIds = ref<string[]>([])
const expanded = computed(() => new Set(expandedIds.value))
const filters = computed(() => [
  { key: 'all' as const, label: 'settingsPlugins.filter_all', count: items.value.length },
  {
    key: 'enabled' as const,
    label: 'settingsPlugins.enabled',
    count: items.value.filter((item) => item.enabled).length,
  },
  {
    key: 'disabled' as const,
    label: 'settingsPlugins.disabled',
    count: items.value.filter((item) => !item.enabled).length,
  },
  {
    key: 'pending' as const,
    label: 'settingsPlugins.filter_pending',
    count: items.value.filter((item) => item.restartRequired).length,
  },
])
const visibleItems = computed(() => {
  const needle = query.value.trim().toLocaleLowerCase()
  return items.value.filter(
    (item) =>
      (filter.value === 'all' ||
        (filter.value === 'enabled' && item.enabled) ||
        (filter.value === 'disabled' && !item.enabled) ||
        (filter.value === 'pending' && item.restartRequired)) &&
      (!needle ||
        [item.name, item.description, item.id].some((text) =>
          text.toLocaleLowerCase().includes(needle),
        )),
  )
})
const visibleSelection = computed(() => {
  const count = visibleItems.value.filter((item) => selected.value.has(item.id)).length
  return count === 0 ? false : count === visibleItems.value.length ? true : 'indeterminate'
})
const hiddenSelected = computed(
  () => selectedIds.value.filter((id) => !visibleItems.value.some((item) => item.id === id)).length,
)
const batchResults = ref<PluginBatchResult[]>([])
const failedResults = computed(() => batchResults.value.filter((result) => !result.succeeded))
const resultNames = ref<Record<string, string>>({})
const lastAction = ref<PluginBatchAction>('enable')
const inUse = (item: PluginView) =>
  item.inUse || item.workflows.some((workflow) => workflow.running)
let disposed = false
let refreshGeneration = 0
let timer: ReturnType<typeof setTimeout> | undefined
function resetFilters() {
  query.value = ''
  filter.value = 'all'
}
function toggleSelection(id: string, checked: boolean) {
  selectedIds.value = checked
    ? [...new Set([...selectedIds.value, id])]
    : selectedIds.value.filter((value) => value !== id)
}
function selectVisible(checked: boolean) {
  const visible = new Set(visibleItems.value.map((item) => item.id))
  selectedIds.value = checked
    ? [...new Set([...selectedIds.value, ...visible])]
    : selectedIds.value.filter((id) => !visible.has(id))
}
function toggleDetails(id: string) {
  expandedIds.value = expanded.value.has(id)
    ? expandedIds.value.filter((value) => value !== id)
    : [...expandedIds.value, id]
}
function batchError(result: PluginBatchResult) {
  return errorMessage(
    new RPCError(
      result.problem ?? { id: 'plugins.change_failed' },
      'plugins.batch',
      result.problem?.operationId ?? '',
      null,
    ),
  )
}
async function refresh(clearError = false) {
  const generation = ++refreshGeneration
  try {
    await loadPluginMessages()
    const [next, pending] = await Promise.all([pluginBackend.list(), pluginBackend.needsRestart()])
    if (!disposed && generation === refreshGeneration) {
      items.value = next
      restartRequired.value = pending
      const current = new Set(next.map((item) => item.id))
      selectedIds.value = selectedIds.value.filter((id) => current.has(id))
      expandedIds.value = expandedIds.value.filter((id) => current.has(id))
      if (clearError) failure.value = ''
    }
  } catch (error) {
    if (!disposed && generation === refreshGeneration) failure.value = errorMessage(error)
  } finally {
    if (!disposed) loading.value = false
  }
}
async function perform(key: string, operation: () => Promise<unknown>) {
  if (busy.value) return
  busy.value = key
  refreshGeneration++
  failure.value = ''
  try {
    await operation()
    await settings.load()
    await refresh()
  } catch (error) {
    if (!disposed) failure.value = errorMessage(error)
  } finally {
    busy.value = ''
  }
}
async function importPlugin() {
  await perform('import', async () => {
    const path = await pluginBackend.pick(t('settingsPlugins.import'))
    if (path) await pluginBackend.import(path)
  })
}
async function runBatch(action: PluginBatchAction, ids = [...selectedIds.value]) {
  if (!ids.length || busy.value) return
  if (
    action === 'uninstall' &&
    (await confirm({
      title: t('settingsPlugins.batch_uninstall_title', { count: ids.length }),
      description: t('settingsPlugins.batch_uninstall_hint'),
      confirmText: t('settingsPlugins.uninstall'),
      color: 'error',
    })) !== true
  )
    return
  lastAction.value = action
  resultNames.value = Object.fromEntries(items.value.map((item) => [item.id, item.name]))
  await perform(`batch:${action}`, async () => {
    batchResults.value = await pluginBackend.batch(action, ids)
  })
}
async function uninstall(item: PluginView) {
  if (
    (await confirm({
      title: t('settingsPlugins.uninstall_title', { name: item.name }),
      description: t('settingsPlugins.uninstall_hint'),
      confirmText: t('settingsPlugins.uninstall'),
      color: 'error',
    })) !== true
  )
    return
  await perform(item.id, () => pluginBackend.uninstall(item.id))
}
let pageActive = false
let pollGeneration = 0
async function poll(generation: number) {
  if (!pageActive || disposed || generation !== pollGeneration) return
  if (!busy.value) await refresh()
  if (pageActive && !disposed && generation === pollGeneration) {
    timer = setTimeout(() => void poll(generation), 2500)
  }
}
function activate() {
  if (pageActive || disposed) return
  pageActive = true
  void poll(++pollGeneration)
}
function deactivate() {
  pageActive = false
  pollGeneration++
  refreshGeneration++
  clearTimeout(timer)
}
onMounted(activate)
onActivated(activate)
onDeactivated(deactivate)
onUnmounted(() => {
  disposed = true
  deactivate()
})
</script>
