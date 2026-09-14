import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import {
  backend,
  type AIModelProfile,
  type HTTPOriginProfile,
  type InstalledApplicationProfile,
  type InstalledAutomationTargetProfile,
} from '@/lib/backend'
import { RPCError, toRPCError } from '@/lib/invoke'

// MouseProfile 命名鼠标校准档（跟 Go services.MouseProfile 对齐）。
// counts360 = 原地转 360° 鼠标硬件累积 |dx|；同机不同游戏内灵敏度不同 → 多 profile。
export interface MouseProfile {
  label: string
  counts360: number
}

// LauncherBlock 悬浮窗启动器的一个块（积木式编排，跟 Go services.LauncherBlock 对齐）。
// type: 'workflow'(Workflow 按钮) | 'label'(文字标题) | 'hsep'(水平分隔符) | 'vsep'(垂直分隔符)。
export interface LauncherBlock {
  id: string
  type: 'workflow' | 'label' | 'hsep' | 'vsep'
  workflowId?: string // type=workflow
  icon?: string // type=workflow 自定义图标（完整 tabler 名）
  label?: string // workflow 自定义名 / label 标题文字
}

// 跟 Go services.Settings 当前公开 DTO 对齐；retired 开发期字段不在前端保留兼容镜像。
export interface Settings {
  onlineServices?: { hubURL: string; registryURL: string }
  ui: {
    panelDisplay?: {
      width: number
      height: number
      columns: number
      size: string
      backgroundOpacity: number
    }
    logger: {
      enabled: boolean
      liveView: boolean
      level: 'debug' | 'info' | 'warn' | 'error'
      panelOpen: boolean
      autoScroll: boolean
      showTime: boolean
      showTag: boolean
      wrapText: boolean
      writeFile: boolean
      fileDir: string
    }
    window: {
      width: number
      height: number
    }
    autostart: boolean // 登录后通过最高权限计划任务启动
    minimizeToTray: boolean // 关闭按钮 → 隐藏到托盘
    actionStopHotkey: string // 全局强停热键（默认 "Ctrl+Shift+F9"），改完即时生效（热键中心 rebind）
    calibrateHotkey: string // DPI 校准启动/停止热键（默认 "F8"），改完即时生效
    recordingStartHotkey: string // 录制开始热键（默认 "F10"）
    recordingStopHotkey: string // 录制停止热键（默认 "F12"，LL hook 拦截不透传游戏）
    recordingPauseHotkey: string // 录制暂停/继续切换热键（默认 "F11"）
    recordingMouseMode: 'relative' | 'absolute' // 录制鼠标语义；改完需重启
    mouseProfiles: MouseProfile[] // 命名鼠标校准档列表（异环/原神…各一档）
    activeMouseProfile: string // 指向 mouseProfiles 里某个 label；空/失配 → activeMouseCounts360 兜底
    launcherItems: LauncherBlock[] // 悬浮窗启动器块序列（设置里编排，有序，积木式）
    launcherDisplay: string // 'both'(默认)|'icon'|'text'
    launcherSize: string // 'xsmall'|'small'|'medium'(默认)|'large'|'xlarge'
    launcherToggleHotkey: string // 呼出/隐藏悬浮窗的全局热键（空=未绑）
    launcherSlotHotkeyModifiers: string // 启动器可见时前九槽位共享的 Ctrl/Shift/Alt 组合
    launcherSlotHotkeysDisabled?: boolean // 关闭启动器可见期间的 1–9 槽位快捷键
    workflowHotkeys: Record<string, string> // 本机 Workflow 永久全局热键
    canvasAssist: {
      collapsed: boolean
      hidden: boolean
      display: 'icons' | 'labels'
      favoriteNodeTypeIds: string[]
    }
  }
  locale: 'zh' | 'en' // i18n 口子
  capture: {
    method: 'auto' | 'gdi' | 'wgc' | 'mock' // 截屏后端；改完需重启 exe 才生效
    dumpDebug: boolean // bot detect 落盘带框 PNG 到 debug/captures/
  }
  ai: {
    profiles: AIModelProfile[]
    roles: {
      diagnostics: string
    }
    authoring: {
      maxIterations: number
    }
  }
  mcp: {
    enabled: boolean
    port: number
  }
  network: {
    httpOrigins: HTTPOriginProfile[]
  }
  applications: {
    profiles: InstalledApplicationProfile[]
  }
  automation: {
    targets: InstalledAutomationTargetProfile[]
  }
}

export type SettingsSaveState = 'idle' | 'saving' | 'saved' | 'error'

export const useSettingsStore = defineStore('settings', () => {
  const data = ref<Settings | null>(null)
  const loaded = ref(false)
  const saveState = ref<SettingsSaveState>('idle')
  const pendingWrites = ref(0)
  const lastSavedAt = ref<number | null>(null)
  const lastFailedPatch = ref<object | null>(null)
  const lastError = ref<RPCError | null>(null)
  let patchTail: Promise<unknown> = Promise.resolve()
  let syncStarted = false

  // activeMouseProfile 命中 → 该档 counts；失配但只有一档 → 那一档；否则 0。
  // 跟 Go Settings.ActiveMouseCounts360() 逻辑一致。
  const mouseProfiles = computed<MouseProfile[]>(() => data.value?.ui.mouseProfiles ?? [])
  const activeMouseCounts360 = computed<number>(() => {
    const list = mouseProfiles.value
    const active = data.value?.ui.activeMouseProfile
    const hit = list.find((p) => p.label === active)
    if (hit) return hit.counts360
    if (list.length === 1) return list[0].counts360
    return 0
  })

  async function load() {
    const s = await backend.settings.get()
    if (s) {
      data.value = s as Settings
      loaded.value = true
    }
  }

  // patch 是 partial Settings（RFC7386 deep merge 语义）
  function patch(p: object): Promise<boolean> {
    pendingWrites.value++
    saveState.value = 'saving'

    const run = async () => {
      let ok = false
      try {
        await backend.settings.update(p)
        if (data.value) deepMerge(data.value, p)
        lastFailedPatch.value = null
        lastError.value = null
        lastSavedAt.value = Date.now()
        ok = true
      } catch (error) {
        lastFailedPatch.value = p
        lastError.value = error instanceof RPCError ? error : toRPCError(error, 'settings.update')
      } finally {
        pendingWrites.value--
        if (pendingWrites.value === 0) saveState.value = ok ? 'saved' : 'error'
      }
      return ok
    }

    // Serialize RFC7386 patches so rapid edits cannot finish out of order and
    // restore an older value in the local store.
    const result = patchTail.then(run, run)
    patchTail = result.then(
      () => undefined,
      () => undefined,
    )
    return result
  }

  async function patchAIProfiles(profiles: AIModelProfile[]) {
    const diagnostics = data.value?.ai.roles?.diagnostics ?? ''
    return patch({
      ai: {
        profiles,
        ...(diagnostics && !profiles.some((profile) => profile.slot === diagnostics)
          ? { roles: { diagnostics: '' } }
          : {}),
      },
    })
  }

  async function patchAIRoles(roles: { diagnostics: string }) {
    return patch({ ai: { roles } })
  }

  async function patchAIAuthoring(authoring: { maxIterations: number }) {
    return patch({ ai: { authoring } })
  }

  async function patchHTTPOrigins(httpOrigins: HTTPOriginProfile[]) {
    return patch({ network: { httpOrigins } })
  }

  async function patchApplicationProfiles(profiles: InstalledApplicationProfile[]) {
    return patch({ applications: { profiles } })
  }

  async function patchAutomationTargets(targets: InstalledAutomationTargetProfile[]) {
    return patch({ automation: { targets } })
  }

  function startSync() {
    if (syncStarted) return
    syncStarted = true
    backend.events.onSettingsChanged(() => {
      // Own writes also emit this event. Wait until the local write queue is
      // drained, then reconcile with the committed backend snapshot.
      void patchTail.then(() => load())
    })
  }

  async function retryLastPatch() {
    const failed = lastFailedPatch.value
    if (!failed) return true
    return patch(failed)
  }

  return {
    data,
    loaded,
    saveState,
    pendingWrites,
    lastSavedAt,
    lastError,
    load,
    patch,
    patchAIProfiles,
    patchAIRoles,
    patchAIAuthoring,
    patchHTTPOrigins,
    patchApplicationProfiles,
    patchAutomationTargets,
    retryLastPatch,
    startSync,
    mouseProfiles,
    activeMouseCounts360,
  }
})

function deepMerge(target: any, source: any) {
  for (const k of Object.keys(source)) {
    if (source[k] !== null && typeof source[k] === 'object' && !Array.isArray(source[k])) {
      if (!target[k]) target[k] = {}
      deepMerge(target[k], source[k])
    } else {
      target[k] = source[k]
    }
  }
}
