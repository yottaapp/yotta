package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/yottaapp/yotta/internal/ai"
	"github.com/yottaapp/yotta/internal/aiauthoring"
	"github.com/yottaapp/yotta/internal/appcontrol"
	"github.com/yottaapp/yotta/internal/artifact"
	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/httpegress"
	"github.com/yottaapp/yotta/internal/serviceconfig"
	"github.com/yottaapp/yotta/pkg/locale"
)

// Settings 是持久化层 schema。
type Settings struct {
	OnlineServices OnlineServiceSettings `json:"onlineServices"`
	UI             UISettings            `json:"ui"`
	Locale         string                `json:"locale"`  // "zh" | "en"；i18n 口子，目前默认且仅 zh
	Capture        CaptureSettings       `json:"capture"` // 截屏后端选择
	AI             AISettings            `json:"ai"`
	MCP            MCPSettings           `json:"mcp"`
	Network        NetworkSettings       `json:"network"`
	Applications   ApplicationSettings   `json:"applications"`
	Automation     AutomationSettings    `json:"automation"`
}

// OnlineServiceSettings overrides build defaults on the next desktop startup.
// Empty addresses retain the configured defaults; these are local settings.
type OnlineServiceSettings struct {
	HubURL      string `json:"hubURL"`
	RegistryURL string `json:"registryURL"`
}

type AISettings struct {
	Profiles  []AIModelSettings   `json:"profiles"`
	Roles     AIRoleSettings      `json:"roles"`
	Authoring AIAuthoringSettings `json:"authoring"`
}

type AIAuthoringSettings struct {
	MaxIterations int `json:"maxIterations"`
}

type AIRoleSettings struct {
	Diagnostics string `json:"diagnostics"`
}

// MCPSettings controls the local, loopback-only Streamable HTTP endpoint.
// The desktop composition owns the listener lifecycle; settings contain no
// bearer tokens or remote bind addresses.
type MCPSettings struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

type NetworkSettings struct {
	HTTPOrigins []HTTPOriginSettings `json:"httpOrigins"`
}

type ApplicationSettings struct {
	Profiles []InstalledApplicationSettings `json:"profiles"`
}

type AutomationSettings struct {
	Targets []InstalledAutomationTargetSettings `json:"targets"`
}

// InstalledAutomationTargetSettings is the stable persistence envelope for an
// adapter-owned profile intent. Workflows receive only Slot; native identity
// and backend details never enter graph data.
type InstalledAutomationTargetSettings struct {
	Slot           string          `json:"slot"`
	Label          string          `json:"label"`
	TargetKind     string          `json:"targetKind"`
	AdapterKind    string          `json:"adapterKind"`
	ProfileVersion string          `json:"profileVersion"`
	Profile        json.RawMessage `json:"profile"`
}

func (configured InstalledAutomationTargetSettings) profileDraft(application InstalledApplicationSettings) (automationinstalled.ProfileDraft, error) {
	return automationinstalled.ProfileDraftFromIntent(
		configured.TargetKind, configured.AdapterKind, configured.ProfileVersion, configured.Profile,
		func(slot string) (appcontrol.ProfileDraft, bool) {
			if application.Slot != slot {
				return appcontrol.ProfileDraft{}, false
			}
			return application.profileDraft(), true
		},
	)
}

func (configured InstalledAutomationTargetSettings) requiresApplication() bool {
	return automationinstalled.RequiresApplication(configured.TargetKind, configured.AdapterKind)
}

func (configured InstalledAutomationTargetSettings) applicationSlot() string {
	slot, _ := automationinstalled.ApplicationSlotFromIntent(
		configured.TargetKind, configured.AdapterKind, configured.ProfileVersion, configured.Profile,
	)
	return slot
}

func (settings AutomationSettings) InstallationDrafts(applications ApplicationSettings, activeMouseCounts360 ...int) ([]automationinstalled.InstallationDraft, error) {
	runtimeMouseCounts360 := 0
	if len(activeMouseCounts360) > 0 && activeMouseCounts360[0] > 0 {
		runtimeMouseCounts360 = activeMouseCounts360[0]
	}
	bySlot := make(map[string]InstalledApplicationSettings, len(applications.Profiles))
	for _, application := range applications.Profiles {
		bySlot[application.Slot] = application
	}
	result := make([]automationinstalled.InstallationDraft, 0, len(settings.Targets))
	for _, configured := range settings.Targets {
		var application InstalledApplicationSettings
		if configured.requiresApplication() {
			var ok bool
			applicationSlot := configured.applicationSlot()
			application, ok = bySlot[applicationSlot]
			if !ok {
				return nil, fmt.Errorf("automation target %q references unknown application slot %q", configured.Slot, applicationSlot)
			}
		}
		profile, err := configured.profileDraft(application)
		if err != nil {
			return nil, fmt.Errorf("automation target %q profile: %w", configured.Slot, err)
		}
		result = append(result, automationinstalled.InstallationDraft{
			Slot: configured.Slot, Label: configured.Label, Profile: profile,
			RuntimeMouseCounts360: int64(runtimeMouseCounts360),
		})
	}
	return result, nil
}

// InstalledApplicationSettings is user configuration for one application
// target. Workflows bind its Slot and the runtime uses the configured path and
// arguments directly.
type InstalledApplicationSettings struct {
	Slot       string   `json:"slot"`
	Label      string   `json:"label"`
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
}

func (configured InstalledApplicationSettings) profileDraft() appcontrol.ProfileDraft {
	return appcontrol.ProfileDraft{Executable: configured.Executable, Arguments: append([]string(nil), configured.Arguments...)}
}
func (configured InstalledApplicationSettings) installationDraft() appcontrol.InstallationDraft {
	return appcontrol.InstallationDraft{Slot: configured.Slot, Profile: configured.profileDraft()}
}
func (settings ApplicationSettings) InstallationDrafts() []appcontrol.InstallationDraft {
	result := make([]appcontrol.InstallationDraft, 0, len(settings.Profiles))
	for _, configured := range settings.Profiles {
		result = append(result, configured.installationDraft())
	}
	return result
}

// HTTPOriginSettings is one configured HTTP target. Workflows select it by
// logical slot and resolve request paths against its base URL.
type HTTPOriginSettings struct {
	Slot                string `json:"slot"`
	Label               string `json:"label"`
	Origin              string `json:"origin"`
	ResponseByteLimit   int64  `json:"responseByteLimit"`
	TimeoutMilliseconds int64  `json:"timeoutMilliseconds"`
}

func (origin HTTPOriginSettings) profileDraft() httpegress.ProfileDraft {
	return httpegress.ProfileDraft{Origin: origin.Origin, ResponseByteLimit: origin.ResponseByteLimit, TimeoutMilliseconds: origin.TimeoutMilliseconds}
}

func (origin HTTPOriginSettings) installationDraft() httpegress.InstallationDraft {
	return httpegress.InstallationDraft{Slot: origin.Slot, Profile: origin.profileDraft()}
}

func (settings NetworkSettings) InstallationDrafts() []httpegress.InstallationDraft {
	result := make([]httpegress.InstallationDraft, 0, len(settings.HTTPOrigins))
	for _, origin := range settings.HTTPOrigins {
		result = append(result, origin.installationDraft())
	}
	return result
}

// AIModelSettings is installation metadata only. Credential material lives in
// AISecrets; model calls bind this logical slot through the trusted Host
// Profile atomically published for each installation-settings generation.
type AIModelSettings struct {
	Slot             string                 `json:"slot"`
	Label            string                 `json:"label"`
	Provider         ai.ProviderKind        `json:"provider"`
	Endpoint         string                 `json:"endpoint"`
	AllowLocalHTTP   bool                   `json:"allowLocalHttp"`
	Model            string                 `json:"model"`
	MaxOutputTokens  int64                  `json:"maxOutputTokens"`
	Capabilities     ai.ProfileCapabilities `json:"capabilities"`
	Pricing          ai.TokenPricing        `json:"pricing"`
	Evaluation       ai.EvaluationStatus    `json:"evaluation"`
	EvaluationSuite  artifact.Digest        `json:"evaluationSuite,omitempty"`
	EvaluationReport ai.EvalReportArtifact  `json:"evaluationReport,omitempty"`
}

func (p AIModelSettings) profileDraft() ai.ModelProfileDraft {
	return ai.ModelProfileDraft{
		Provider: p.Provider, Endpoint: p.Endpoint, AllowLocalHTTP: p.AllowLocalHTTP, Model: p.Model, Capabilities: p.Capabilities,
		MaxOutputTokens: p.MaxOutputTokens, Pricing: p.Pricing, Evaluation: p.Evaluation, EvaluationSuite: p.EvaluationSuite, EvaluationReport: p.EvaluationReport.Digest,
		ProviderMetadata: json.RawMessage(`{}`),
	}
}

func (p AIModelSettings) installationDraft() ai.InstallationDraft {
	return ai.InstallationDraft{Slot: p.Slot, Profile: p.profileDraft(), Evaluation: p.EvaluationReport}
}

func (s AISettings) InstallationDrafts() []ai.InstallationDraft {
	result := make([]ai.InstallationDraft, 0, len(s.Profiles))
	for _, profile := range s.Profiles {
		result = append(result, profile.installationDraft())
	}
	return result
}

// CaptureSettings 选哪个截屏后端。Method 改这个要重启 exe 才生效；DumpDebug 即时生效。
//   - "auto": 启动期根据 OS build 自动选——Win11/Server 2022 (build>=20348) 走 WGC，
//     老系统走 GDI。新装默认就是这个。
//   - "gdi": PrintWindow + GDI，兼容性最好，但 D3D 游戏后台容易拿冻结帧
//   - "wgc": Windows Graphics Capture（Win10 1903+），后台抓帧稳定，
//     依赖 bin/capture_wgc.dll；Win10 上有黄框关不掉
//   - "mock": 从磁盘 PNG 序列回放（调试用），需把帧放进 bin/mock-frames/
//     或设置环境变量 YOTTA_MOCK_DIR 指向目录
type CaptureSettings struct {
	Method    string `json:"method"`    // "auto" | "gdi" | "wgc" | "mock"
	DumpDebug bool   `json:"dumpDebug"` // bot detect 关键路径 dump 带框 PNG 到 bin/captures/，调试用
}

type UISettings struct {
	PanelDisplay   *PanelDisplaySettings `json:"panelDisplay,omitempty"`
	Logger         LoggerSettings        `json:"logger"`
	Window         WindowSettings        `json:"window"`
	Autostart      bool                  `json:"autostart"`      // 登录后通过最高权限计划任务启动
	MinimizeToTray bool                  `json:"minimizeToTray"` // 关闭按钮 → 隐藏到托盘
	// ActionStopHotkey 全局打断动作热键（默认 "Ctrl+Shift+F9"）。
	// 改动需重启生效（main.go 启动期注册一次，不监听 settings 变化）。
	ActionStopHotkey string `json:"actionStopHotkey"`
	// CalibrateHotkey DPI 校准启动/停止 toggle 热键（默认 "F8"）。
	// 「快捷键」页 rebind 经 onSystemHotkeyChange 写回, 启动期 main.go 读这字段注册。
	CalibrateHotkey string `json:"calibrateHotkey"`
	// WindowCaptureHotkey 窗口捕获键（默认 "F9"）—— NodeInspector「捕获目标窗口」按下它抓前台游戏窗口。
	// registry 值持有者条目 (tools.window-capture)，「快捷键」页可 rebind；捕获时临时 OS 注册它。
	WindowCaptureHotkey string `json:"windowCaptureHotkey"`
	// RecordingStopHotkey 录制停止热键 (LL hook 拦截, 不透传游戏). 默认 "F12".
	RecordingStopHotkey string `json:"recordingStopHotkey"`
	// RecordingStartHotkey 进入录制准备态后的启动热键. 默认 "F10".
	RecordingStartHotkey string `json:"recordingStartHotkey"`
	// RecordingPauseHotkey 录制暂停/继续切换热键 (LL hook 拦截, 不透传游戏也不进 clip). 默认 "F11".
	// 录制中按 → 暂停; 暂停中按 → 走 3s 倒计时后继续. 走热键避免点 HUD 按钮的点击被录进 clip.
	RecordingPauseHotkey string `json:"recordingPauseHotkey"`
	// RecordingCancelHotkey 丢弃当前录制的热键 (LL hook 拦截, 不透传游戏也不进 clip). 默认 "F7".
	RecordingCancelHotkey string `json:"recordingCancelHotkey"`
	// PathMarkHotkey is the persistent shortcut managed by the hotkey center.
	PathMarkHotkey string `json:"pathMarkHotkey"`
	// RecordingMouseMode 录制时鼠标语义. "relative" (FPS 相机 RawDelta, 默认) / "absolute" (UI 点击 MouseMove screen px).
	// 决定 recorder drainLoop 是否窗口过滤. 改动需重启生效.
	RecordingMouseMode string `json:"recordingMouseMode"`
	// MouseProfiles 命名鼠标校准 profile 列表。同一台机器玩不同游戏 (异环 vs 原神) 内灵敏度不同 →
	// 360° counts 不同, 装不进单个全局值。每个 profile 存一个 {Label, Counts360}。
	// 跨电脑共享脚本时, counts360 是 (本机 × 游戏内灵敏度) 的函数, 本就 per-machine, 不跨机同步。
	MouseProfiles []MouseProfile `json:"mouseProfiles"`
	// ActiveMouseProfile 指向 MouseProfiles 里某个 Label, 标记"当前默认"。
	// 录制兜底 getter (无 MouseCalibration 节点时) + 新建节点默认值 + "同步所有容器" 用它。
	// 空 / 指向不存在的 label → ActiveMouseCounts360() 兜底 (见该方法)。
	ActiveMouseProfile string `json:"activeMouseProfile"`
	// LauncherItems 悬浮窗启动器的块序列（用户在设置里编排，有序，积木式）。空 = 空启动器。
	LauncherItems []LauncherBlock `json:"launcherItems"`
	// LauncherDisplay 按钮显示：""/"both"=图标+文字 | "icon"=仅图标 | "text"=仅文字。
	LauncherDisplay string `json:"launcherDisplay"`
	// LauncherSize 启动器内容尺寸："xsmall" | "small" | "medium" | "large" | "xlarge"。
	LauncherSize string `json:"launcherSize"`
	// LauncherToggleHotkey 呼出/隐藏悬浮窗的全局热键（空 = 未绑）。
	LauncherToggleHotkey string `json:"launcherToggleHotkey"`
	// LauncherSlotHotkeyModifiers 是启动器可见期间前九个槽位共享的修饰键。
	// 仅允许 Ctrl/Shift/Alt 的非空组合；隐藏启动器后对应 OS 热键立即注销。
	LauncherSlotHotkeyModifiers string `json:"launcherSlotHotkeyModifiers"`
	// LauncherSlotHotkeysDisabled 关闭启动器可见期间的 1–9 槽位快捷键；默认 false。
	LauncherSlotHotkeysDisabled bool `json:"launcherSlotHotkeysDisabled,omitempty"`
	// WorkflowHotkeys 是本机安装级 Workflow 全局热键，不进入可移植 Workflow Source。
	WorkflowHotkeys map[string]string    `json:"workflowHotkeys,omitempty"`
	CanvasAssist    CanvasAssistSettings `json:"canvasAssist"`
}

type PanelDisplaySettings struct {
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	Columns           int    `json:"columns"`
	Size              string `json:"size"`
	BackgroundOpacity int    `json:"backgroundOpacity"`
}

type CanvasAssistSettings struct {
	Collapsed           bool     `json:"collapsed"`
	Hidden              bool     `json:"hidden"`
	Display             string   `json:"display"`
	FavoriteNodeTypeIDs []string `json:"favoriteNodeTypeIds"`
}

// MouseProfile 一个命名校准档。Counts360 = 原地转身 360° 鼠标硬件累积的 |dx| (用户在游戏里实测)。
type MouseProfile struct {
	Label     string `json:"label"`
	Counts360 int    `json:"counts360"`
}

// LauncherBlock 悬浮窗启动器的一个块（积木式编排）。Type 决定形态：
//   - "workflow": 一个 Workflow 按钮（WorkflowID + 可选 Icon/Label）
//   - "label":     文字标题（Label = 标题文字），占整行
//   - "hsep":      水平分隔符（占整行的横线，把后面的块挤到下一排）
//   - "vsep":      垂直分隔符（同排按钮之间的竖线）
//
// 只存本机编排数据；Workflow 名称和运行状态由 Application 投影。
type LauncherBlock struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	WorkflowID string `json:"workflowId,omitempty"` // type=workflow
	Icon       string `json:"icon,omitempty"`       // type=workflow 自定义图标（完整 tabler 名）
	Label      string `json:"label,omitempty"`      // workflow 自定义名 / label 标题文字
}

// ActiveMouseCounts360 返回当前生效 profile 的 counts360。
//   - ActiveMouseProfile 命中某个 profile.Label → 返该值
//   - 没命中但恰好只有一个 profile → 返那个 (单 profile 场景免设 active 的便利, 非兼容垫片)
//   - 否则 (空列表 / 多 profile 没选 active 没命中) → 0 (= 未校准, 回放不缩放)
func (s *Settings) ActiveMouseCounts360() int {
	for _, p := range s.UI.MouseProfiles {
		if p.Label == s.UI.ActiveMouseProfile {
			return p.Counts360
		}
	}
	if len(s.UI.MouseProfiles) == 1 {
		return s.UI.MouseProfiles[0].Counts360
	}
	return 0
}

// WindowSettings 记录用户调到的窗口尺寸。main.go 启动时读取，
// WindowEndResize 事件触发时写回 settings.json。
// 不存 x/y：多屏拔插容易把窗口扔到屏幕外，用户自己点 Center() 更省事。
type WindowSettings struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type LoggerSettings struct {
	Enabled    bool   `json:"enabled"`   // master diagnostic switch; false stops production at source
	LiveView   bool   `json:"liveView"`  // stream normalized batches to the main webview
	Level      string `json:"level"`     // debug | info | warn | error
	PanelOpen  bool   `json:"panelOpen"` // 面板折叠态 (false=折叠)
	AutoScroll bool   `json:"autoScroll"`
	ShowTime   bool   `json:"showTime"`
	ShowTag    bool   `json:"showTag"`
	WrapText   bool   `json:"wrapText"`
	WriteFile  bool   `json:"writeFile"`
	FileDir    string `json:"fileDir"` // 写文件目录；空 = 当前 RootSet 的 diagnostics/logs
}

// defaultSettings 返回内置默认值。
func defaultSettings() *Settings {
	return &Settings{
		UI: UISettings{
			Logger: LoggerSettings{
				Enabled: true, LiveView: true, Level: "info",
				PanelOpen: true, AutoScroll: true, ShowTime: true, ShowTag: true,
				WrapText: false, WriteFile: true, FileDir: "",
			},
			Window:                      WindowSettings{Width: 1100, Height: 720},
			ActionStopHotkey:            "Ctrl+Shift+F9",
			CalibrateHotkey:             "F8",
			WindowCaptureHotkey:         "F9",
			RecordingStopHotkey:         "F12",
			RecordingStartHotkey:        "F10",
			RecordingPauseHotkey:        "F11",
			RecordingCancelHotkey:       "F7",
			PathMarkHotkey:              "F6",
			RecordingMouseMode:          "relative",
			LauncherSize:                "medium",
			LauncherSlotHotkeyModifiers: "Ctrl+Shift",
			CanvasAssist: CanvasAssistSettings{
				Display: "labels",
				FavoriteNodeTypeIDs: []string{
					"https://schemas.yotta.dev/nodes/automation/press-keys",
					"https://schemas.yotta.dev/nodes/control/delay",
					"https://schemas.yotta.dev/nodes/automation/click-pointer",
					"https://schemas.yotta.dev/nodes/automation/click-template",
					"https://schemas.yotta.dev/nodes/control/branch",
				},
			},
		},
		Locale: "zh",
		// 默认 auto：启动期 main.go 看 OS build 决定 Win10 走 GDI / Win11+ 走 WGC。
		// 用户嫌弃自动选的可以去设置硬切 gdi/wgc/mock。
		Capture: CaptureSettings{Method: "auto", DumpDebug: false},
		AI: AISettings{
			Profiles: []AIModelSettings{}, Roles: AIRoleSettings{},
			Authoring: AIAuthoringSettings{MaxIterations: aiauthoring.DefaultMaxIterations},
		},
		MCP:          MCPSettings{Enabled: false, Port: 39271},
		Network:      NetworkSettings{HTTPOrigins: []HTTPOriginSettings{}},
		Applications: ApplicationSettings{Profiles: []InstalledApplicationSettings{}},
		Automation:   AutomationSettings{Targets: []InstalledAutomationTargetSettings{}},
	}
}

type settingsCommittedError struct{ err error }

func (e *settingsCommittedError) Error() string   { return e.err.Error() }
func (e *settingsCommittedError) Unwrap() error   { return e.err }
func (e *settingsCommittedError) Committed() bool { return true }

func settingsSaveCommitted(err error) bool {
	var committed *settingsCommittedError
	return errors.As(err, &committed)
}

// Clone deep copy。SettingsService.Update 的 clone→merge→Validate→swap→save 流程用。
func (s *Settings) Clone() *Settings {
	data, _ := json.Marshal(s)
	var out Settings
	_ = json.Unmarshal(data, &out)
	return &out
}

// Validate 字段范围检查。malformed patch 应用后失败 → SettingsService.Update 拒绝 swap。
func (s *Settings) Validate() error {
	if !locale.Valid(s.Locale) {
		return fmt.Errorf("locale 不在支持列表内: got %q", s.Locale)
	}
	switch s.Capture.Method {
	case "auto", "gdi", "wgc", "mock":
		// ok
	default:
		return fmt.Errorf("capture.method 必须是 \"auto\" / \"gdi\" / \"wgc\" / \"mock\"，got %q", s.Capture.Method)
	}
	// 窗口尺寸不强制下限（main.go 用 SetMinSize 兜底），但拦住明显无效值
	if s.UI.Window.Width < 100 || s.UI.Window.Height < 100 {
		return fmt.Errorf("ui.window 尺寸过小: %dx%d", s.UI.Window.Width, s.UI.Window.Height)
	}
	switch s.UI.Logger.Level {
	case "debug", "info", "warn", "error":
		// ok
	default:
		return fmt.Errorf("ui.logger.level 必须是 debug/info/warn/error，got %q", s.UI.Logger.Level)
	}
	switch s.UI.LauncherSize {
	case "", "xsmall", "small", "medium", "large", "xlarge":
		// 空值按 medium 解释，兼容显式删除该偏好的旧 patch。
	default:
		return fmt.Errorf("ui.launcherSize 必须是 xsmall/small/medium/large/xlarge，got %q", s.UI.LauncherSize)
	}
	if p := s.UI.PanelDisplay; p != nil {
		if p.Width < 240 || p.Width > 3840 || p.Height < 160 || p.Height > 2160 || p.Columns < 1 || p.Columns > 4 || p.BackgroundOpacity < 0 || p.BackgroundOpacity > 100 {
			return errors.New("ui.panelDisplay contains an out-of-range value")
		}
		if p.Size != "small" && p.Size != "medium" && p.Size != "large" {
			return errors.New("ui.panelDisplay.size must be small, medium or large")
		}
	}
	if !validLauncherSlotModifiers(s.UI.LauncherSlotHotkeyModifiers) {
		return fmt.Errorf("ui.launcherSlotHotkeyModifiers 必须是 Ctrl/Shift/Alt 的非空组合，got %q", s.UI.LauncherSlotHotkeyModifiers)
	}
	if s.UI.CanvasAssist.Display != "icons" && s.UI.CanvasAssist.Display != "labels" {
		return fmt.Errorf("ui.canvasAssist.display must be icons or labels, got %q", s.UI.CanvasAssist.Display)
	}
	if len(s.UI.CanvasAssist.FavoriteNodeTypeIDs) > 5 {
		return errors.New("ui.canvasAssist.favoriteNodeTypeIds exceeds five entries")
	}
	seenCanvasFavorites := make(map[string]bool, len(s.UI.CanvasAssist.FavoriteNodeTypeIDs))
	for _, nodeTypeID := range s.UI.CanvasAssist.FavoriteNodeTypeIDs {
		if len(nodeTypeID) == 0 || len(nodeTypeID) > 512 || seenCanvasFavorites[nodeTypeID] {
			return fmt.Errorf("ui.canvasAssist.favoriteNodeTypeIds contains invalid or duplicate ID %q", nodeTypeID)
		}
		seenCanvasFavorites[nodeTypeID] = true
	}
	if err := s.AI.validate(); err != nil {
		return err
	}
	if s.MCP.Port < 1024 || s.MCP.Port > 65535 {
		return fmt.Errorf("mcp.port must be between 1024 and 65535, got %d", s.MCP.Port)
	}
	if err := s.Network.validate(); err != nil {
		return err
	}
	for _, field := range []*string{&s.OnlineServices.HubURL, &s.OnlineServices.RegistryURL} {
		value, err := serviceconfig.NormalizeEndpoint(*field)
		if err != nil {
			return fmt.Errorf("onlineServices: %w", err)
		}
		*field = value
	}
	if err := s.Applications.validate(); err != nil {
		return err
	}
	if err := s.Automation.validate(s.Applications); err != nil {
		return err
	}
	installedSlots := make(map[string]string, len(s.AI.Profiles)+len(s.Network.HTTPOrigins)+len(s.Applications.Profiles)+len(s.Automation.Targets))
	for _, profile := range s.AI.Profiles {
		installedSlots[profile.Slot] = "ai.profiles"
	}
	for _, origin := range s.Network.HTTPOrigins {
		if owner, exists := installedSlots[origin.Slot]; exists {
			return fmt.Errorf("installation slot %q is shared by %s and network.httpOrigins", origin.Slot, owner)
		}
		installedSlots[origin.Slot] = "network.httpOrigins"
	}
	for _, configured := range s.Applications.Profiles {
		if owner, exists := installedSlots[configured.Slot]; exists {
			return fmt.Errorf("installation slot %q is shared by %s and applications.profiles", configured.Slot, owner)
		}
		installedSlots[configured.Slot] = "applications.profiles"
	}
	for _, configured := range s.Automation.Targets {
		if owner, exists := installedSlots[configured.Slot]; exists {
			return fmt.Errorf("installation slot %q is shared by %s and automation.targets", configured.Slot, owner)
		}
		installedSlots[configured.Slot] = "automation.targets"
	}
	return nil
}

func validLauncherSlotModifiers(value string) bool {
	if value == "" {
		return true
	}
	parts := strings.Split(value, "+")
	if len(parts) == 0 || len(parts) > 3 {
		return false
	}
	seen := map[string]bool{}
	for _, part := range parts {
		if part != "Ctrl" && part != "Shift" && part != "Alt" || seen[part] {
			return false
		}
		seen[part] = true
	}
	return true
}

func (settings *AISettings) validate() error {
	if settings.Authoring.MaxIterations < aiauthoring.MinMaxIterations || settings.Authoring.MaxIterations > aiauthoring.MaxMaxIterations {
		return fmt.Errorf("ai.authoring.maxIterations must be between %d and %d", aiauthoring.MinMaxIterations, aiauthoring.MaxMaxIterations)
	}
	if len(settings.Profiles) > 64 {
		return errors.New("ai.profiles exceeds installation budget")
	}
	seenSlot := make(map[string]bool, len(settings.Profiles))
	seenLabel := make(map[string]bool, len(settings.Profiles))
	for _, configured := range settings.Profiles {
		if len(configured.Label) == 0 || len(configured.Label) > 128 || seenLabel[configured.Label] {
			return fmt.Errorf("ai.profiles has an invalid or duplicate label %q", configured.Label)
		}
		seenLabel[configured.Label] = true
		if err := ai.ValidateInstallationSlot(configured.Slot); err != nil || seenSlot[configured.Slot] {
			return fmt.Errorf("ai.profiles has an invalid or duplicate slot %q", configured.Slot)
		}
		seenSlot[configured.Slot] = true
		profile, err := ai.SealModelProfile(configured.profileDraft())
		if err != nil {
			return fmt.Errorf("ai.profiles[%s]: %w", configured.Slot, err)
		}
		evaluationErr := ai.ValidateEvaluation(profile, configured.EvaluationReport)
		if evaluationErr != nil && !errors.Is(evaluationErr, ai.ErrEvaluationNotApproved) {
			return fmt.Errorf("ai.profiles[%s]: %w", configured.Slot, evaluationErr)
		}
	}
	if settings.Roles.Diagnostics != "" {
		var selected *AIModelSettings
		for index := range settings.Profiles {
			if settings.Profiles[index].Slot == settings.Roles.Diagnostics {
				selected = &settings.Profiles[index]
				break
			}
		}
		if selected == nil || !selected.Capabilities.ToolCalling {
			return errors.New("ai.roles.diagnostics must reference a tool-calling AI profile")
		}
	}
	return nil
}

func (settings *NetworkSettings) validate() error {
	if len(settings.HTTPOrigins) > 64 {
		return errors.New("network.httpOrigins exceeds installation budget")
	}
	seenSlots, seenLabels := map[string]bool{}, map[string]bool{}
	for _, configured := range settings.HTTPOrigins {
		if configured.Label == "" || len(configured.Label) > 128 || seenLabels[configured.Label] {
			return fmt.Errorf("network.httpOrigins has an invalid or duplicate label %q", configured.Label)
		}
		seenLabels[configured.Label] = true
		if err := httpegress.ValidateInstallationSlot(configured.Slot); err != nil || seenSlots[configured.Slot] {
			return fmt.Errorf("network.httpOrigins has an invalid or duplicate slot %q", configured.Slot)
		}
		seenSlots[configured.Slot] = true
		if _, err := httpegress.SealProfile(configured.profileDraft()); err != nil {
			return fmt.Errorf("network.httpOrigins[%s]: %w", configured.Slot, err)
		}
	}
	return nil
}

func (settings *ApplicationSettings) validate() error {
	if len(settings.Profiles) > 64 {
		return errors.New("applications.profiles exceeds installation budget")
	}
	seenSlots, seenLabels := map[string]bool{}, map[string]bool{}
	for _, configured := range settings.Profiles {
		if configured.Label == "" || len(configured.Label) > 128 || seenLabels[configured.Label] {
			return fmt.Errorf("applications.profiles has an invalid or duplicate label %q", configured.Label)
		}
		seenLabels[configured.Label] = true
		if err := appcontrol.ValidateInstallationSlot(configured.Slot); err != nil || seenSlots[configured.Slot] {
			return fmt.Errorf("applications.profiles has an invalid or duplicate slot %q", configured.Slot)
		}
		seenSlots[configured.Slot] = true
		if _, err := appcontrol.SealProfile(configured.profileDraft()); err != nil {
			return fmt.Errorf("applications.profiles[%s]: %w", configured.Slot, err)
		}
	}
	return nil
}

func (settings *AutomationSettings) validate(applications ApplicationSettings) error {
	if len(settings.Targets) > 64 {
		return errors.New("automation.targets exceeds installation budget")
	}
	applicationsBySlot := make(map[string]InstalledApplicationSettings, len(applications.Profiles))
	for _, configured := range applications.Profiles {
		applicationsBySlot[configured.Slot] = configured
	}
	seenSlots, seenLabels := map[string]bool{}, map[string]bool{}
	for _, configured := range settings.Targets {
		if configured.Label == "" || len(configured.Label) > 128 || seenLabels[configured.Label] {
			return fmt.Errorf("automation.targets has an invalid or duplicate label %q", configured.Label)
		}
		seenLabels[configured.Label] = true
		if err := automationinstalled.ValidateInstallationSlot(configured.Slot); err != nil || seenSlots[configured.Slot] {
			return fmt.Errorf("automation.targets has an invalid or duplicate slot %q", configured.Slot)
		}
		seenSlots[configured.Slot] = true
		_, supported := automationinstalled.TargetType(configured.TargetKind, configured.AdapterKind)
		if !supported {
			return fmt.Errorf("automation.targets[%s] has unsupported target/adapter %q/%q", configured.Slot, configured.TargetKind, configured.AdapterKind)
		}
		var application InstalledApplicationSettings
		if configured.requiresApplication() {
			var ok bool
			applicationSlot := configured.applicationSlot()
			application, ok = applicationsBySlot[applicationSlot]
			if !ok {
				return fmt.Errorf("automation.targets[%s] references unknown application slot %q", configured.Slot, applicationSlot)
			}
		}
		draft, err := configured.profileDraft(application)
		if err != nil {
			return fmt.Errorf("automation.targets[%s]: %w", configured.Slot, err)
		}
		if _, err := automationinstalled.SealProfile(draft); err != nil {
			return fmt.Errorf("automation.targets[%s]: %w", configured.Slot, err)
		}
	}
	return nil
}

// ApplyMergePatch 把 RFC7386 patch 应用到 s 上。语义：
//   - object 字段递归 merge
//   - array 整体替换
//   - scalar 覆盖
//   - null 删除字段（RFC7386 标准）
//
// 流程：把 s 序列化 → MergePatch(jsonBytes, patch) → 反序列化回 s。
// 用 DisallowUnknownFields 防 patch 写错字段名。
func ApplyMergePatch(s *Settings, patch json.RawMessage) error {
	cur, err := json.Marshal(s)
	if err != nil {
		return err
	}
	merged, err := jsonpatch.MergePatch(cur, patch)
	if err != nil {
		return fmt.Errorf("RFC7386 merge failed: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(merged))
	dec.DisallowUnknownFields()
	*s = Settings{}
	if err := dec.Decode(s); err != nil {
		return fmt.Errorf("merged settings unmarshal failed: %w", err)
	}
	return nil
}
