// Package desktopapp composes the Wails presentation adapter around the
// constructor-complete Workflow Application runtime.
package desktopapp

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/yottaapp/yotta/internal/aiauthoring"
	"github.com/yottaapp/yotta/internal/apperr"
	yottaapplication "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/appruntime"
	"github.com/yottaapp/yotta/internal/authoringcontext"
	"github.com/yottaapp/yotta/internal/communityclient"
	"github.com/yottaapp/yotta/internal/hotkey"
	"github.com/yottaapp/yotta/internal/localruntime"
	"github.com/yottaapp/yotta/internal/nativeoidc"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/registryclient"
	"github.com/yottaapp/yotta/internal/securestore"
	"github.com/yottaapp/yotta/internal/serviceconfig"
	"github.com/yottaapp/yotta/internal/services"
	"github.com/yottaapp/yotta/internal/services/asset"
	"github.com/yottaapp/yotta/internal/services/calibration"
	"github.com/yottaapp/yotta/internal/services/mcpserver"
	"github.com/yottaapp/yotta/internal/services/panels"
	"github.com/yottaapp/yotta/internal/services/pathrecording"
	"github.com/yottaapp/yotta/internal/services/plugins"
	"github.com/yottaapp/yotta/internal/services/recording"
	"github.com/yottaapp/yotta/internal/services/resourceauthoring"
	"github.com/yottaapp/yotta/internal/services/schedule"
	"github.com/yottaapp/yotta/internal/services/snippet"
	"github.com/yottaapp/yotta/internal/services/tools"
	"github.com/yottaapp/yotta/internal/services/workflow"
	"github.com/yottaapp/yotta/internal/storage"
	storagemigrate "github.com/yottaapp/yotta/internal/storage/migrate"
	"github.com/yottaapp/yotta/pkg/locale"
	"github.com/yottaapp/yotta/pkg/screenshot"
	"github.com/yottaapp/yotta/pkg/version"
)

type Config struct {
	HubURL                    string
	HubAllowLoopbackHTTP      bool
	Assets                    embed.FS
	TrayIcon                  []byte
	StorageRoot               string
	RegistryURL               string
	RegistryAllowLoopbackHTTP bool
	RegistryTokens            registryclient.TokenSource
	OIDCAuthorizationEndpoint string
	OIDCTokenEndpoint         string
	OIDCClientID              string
	OIDCCallbackAddress       string
	OIDCAudience              string
	OIDCUserinfoEndpoint      string
	AccountURL                string
}

func Run(config Config) error {
	if _, err := storagemigrate.Ensure(context.Background(), storagemigrate.Options{
		Root: config.StorageRoot, MaxRuns: 65536,
	}); err != nil {
		return runStorageRecovery(config, err)
	}
	instanceID, err := singleInstanceID(config.StorageRoot)
	if err != nil {
		return fmt.Errorf("resolve desktop instance identity: %w", err)
	}
	mainActivator := &mainWindowActivator{}
	profileRoots, err := storage.Resolve(config.StorageRoot)
	if err != nil {
		return err
	}
	windowOptions := wailsWindowsOptions()
	if windowOptions.WebviewUserDataPath == "" {
		windowOptions.WebviewUserDataPath = filepath.Join(profileRoots.Cache, "webview2")
	}
	wailsApp := application.New(application.Options{
		Name:         "Yotta",
		Description:  "节点编排，自动执行",
		MarshalError: apperr.Marshal,
		Windows:      windowOptions,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: instanceID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				mainActivator.request()
			},
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(config.Assets),
		},
	})
	// 日志栈：zerolog process/Workflow diagnostics → LogSink → 单一 log:batch 事件 + 可选 JSONL.
	logSink := services.NewLogSink(nil) // emit 在 wailsApp 构造后装配
	rootLog := zerolog.New(logSink).With().Timestamp().Logger()
	restoreProblemObserver := apperr.SetObserver(func(problem apperr.Envelope, cause error) {
		rootLog.Error().
			Str("tag", "SYSTEM").
			Str("problemId", problem.ID).
			Str("operationId", problem.OperationID).
			Str("errorType", reflect.TypeOf(cause).String()).
			Err(cause).
			Msg("RPC problem")
	})
	defer restoreProblemObserver()
	aiSecrets := services.NewAISecrets(securestore.New())
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve local runtime executable: %w", err)
	}
	var app *services.App
	local, err := localruntime.Open(context.Background(), localruntime.Config{
		StorageRoot:       config.StorageRoot,
		Executable:        executable,
		LogSink:           logSink,
		RootLog:           rootLog,
		ConfigureFileLogs: true,
		AISecrets:         aiSecrets,
		WorkflowLog:       newWorkflowLogEmitter(rootLog),
		Now:               time.Now,
		OnRunEvent: func(event yottaapplication.RunEvent) {
			payload := map[string]any{
				"runId": event.RunID, "workflowId": event.WorkflowID, "status": event.Status,
				"generation": event.Generation, "recordDigest": event.Digest,
			}
			if event.Err != nil {
				payload["failed"] = true
				rootLog.Warn().Err(event.Err).Str("tag", "RUN").Str("runId", event.RunID).Msg("workflow Run completed with error")
			}
			app.Emit("run:changed", payload)
		},
		OnDebugEvent: func(event yottaapplication.DebugEvent) {
			app.Emit("debug:changed", map[string]any{
				"runId": event.RunID, "snapshot": event.Snapshot,
			})
		},
	})
	if err != nil {
		return fmt.Errorf("open local runtime: %w", err)
	}
	app = local.Settings
	config = withServiceSettings(config, app.Settings().OnlineServices)
	roots := local.Roots
	workflowRuntime := local.Workflow
	sharedBlobStore := workflowRuntime.BlobStore
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if shutdownErr := local.Close(shutdownCtx); shutdownErr != nil {
			fmt.Fprintf(os.Stderr, "close local runtime: %v\n", shutdownErr)
		}
	}()

	// Screenshot diagnostics belong to the active storage profile and never to
	// the executable or process working directory.
	screenshot.InitDefault(roots.Captures, 16, app.Settings().Capture.DumpDebug)
	defer screenshot.CloseDefault()

	// 按 settings.locale 加载每个 bot 的配置 + 视觉模板。
	// 失败分两种：
	//   - ErrLocaleNotImplemented：合法的"未实装"状态（manifest implemented:false），
	//     info 级别日志，前端把这个 bot 标灰；不当成错误。
	//   - 其它 error：真配置错（yaml 解析挂了、ROI 越界等），error 级别日志。
	loc := locale.Locale(app.Settings().Locale)
	if !locale.Valid(string(loc)) {
		rootLog.Warn().Str("tag", "SYSTEM").Msgf("settings.locale=%q 无效，回退 zh", loc)
		loc = locale.Zh
	}
	_ = loc // locale 保留给后续 Locale 设置项使用

	wailsServices := make([]application.Service, 0, 16)

	// 共享 HotkeyManager。Win32 RegisterHotKey 是 process-wide unique（hWnd=NULL 时
	// 跟线程绑定），全 app 必须共享同一个实例 —— action / recorder 都注册到这里。
	// 两个 manager 就两个 hotkey 线程互相覆盖反注册，热键全丢。
	sharedHotkeys := hotkey.NewHotkeyManager()

	settingsSvc := services.NewSettingsService(app, aiSecrets)
	observation := &authoringcontext.Service{Application: workflowRuntime.Application, Targets: workflowRuntime.AuthoringTargets(), Screen: authoringcontext.CaptureScreen, ListTargets: func() []authoringcontext.TargetInfo {
		items := []authoringcontext.TargetInfo{}
		for _, configured := range app.Settings().Automation.Targets {
			items = append(items, authoringcontext.TargetInfo{Slot: configured.Slot, Label: configured.Label, Kind: configured.TargetKind, Adapter: configured.AdapterKind})
		}
		return items
	}}
	mcpRuntime, err := mcpserver.NewRuntime(workflowRuntime.Application, observation)
	if err != nil {
		return fmt.Errorf("initialize MCP runtime: %w", err)
	}

	assetStore, err := asset.NewStore(
		local.Assets,
		local.Objects,
		sharedBlobStore,
	)
	if err != nil {
		return fmt.Errorf("initialize asset store: %w", err)
	}
	resourceCreator, err := resourceauthoring.NewCreator(sharedBlobStore, assetStore)
	if err != nil {
		return fmt.Errorf("initialize Workflow Resource creator: %w", err)
	}
	resourceAuthoringSvc := resourceauthoring.NewService(resourceCreator, app.Emit)
	snippetStore, err := snippet.NewStore(filepath.Join(roots.Data, "snippets"))
	if err != nil {
		return fmt.Errorf("initialize snippet store: %w", err)
	}
	authoringTargets := workflowRuntime.AuthoringTargets()
	if err := app.AttachSettingsActivator(func(before, after *services.Settings) (*services.SettingsActivationPlan, error) {
		installationsChanged := !reflect.DeepEqual(before.AI, after.AI) ||
			!reflect.DeepEqual(before.Network, after.Network) ||
			!reflect.DeepEqual(before.Applications, after.Applications) ||
			!reflect.DeepEqual(before.Automation, after.Automation) ||
			before.ActiveMouseCounts360() != after.ActiveMouseCounts360()
		mcpChanged := !reflect.DeepEqual(before.MCP, after.MCP)
		if !installationsChanged && !mcpChanged {
			return nil, nil
		}
		var installationCommit func() error
		var installationAbort func()
		if installationsChanged {
			drafts, err := after.Automation.InstallationDrafts(after.Applications, after.ActiveMouseCounts360())
			if err != nil {
				return nil, fmt.Errorf("prepare installed automation: %w", err)
			}
			prepared, err := workflowRuntime.PrepareInstallations(
				after.AI.InstallationDrafts(),
				aiSecrets,
				after.Network.InstallationDrafts(),
				after.Applications.InstallationDrafts(),
				drafts,
			)
			if err != nil {
				return nil, err
			}
			installationCommit = prepared.Commit
			installationAbort = prepared.Abort
		}
		var mcpCommit func() error
		var mcpAbort func()
		if mcpChanged {
			var err error
			mcpCommit, mcpAbort, err = mcpRuntime.Prepare(mcpserver.RuntimeConfig{
				Enabled: after.MCP.Enabled,
				Port:    after.MCP.Port,
			})
			if err != nil {
				if installationAbort != nil {
					installationAbort()
				}
				return nil, err
			}
		}
		return &services.SettingsActivationPlan{
			Commit: func() error {
				if installationCommit != nil {
					if err := installationCommit(); err != nil {
						if mcpAbort != nil {
							mcpAbort()
						}
						return err
					}
				}
				if mcpCommit != nil {
					return mcpCommit()
				}
				return nil
			},
			Abort: func() {
				if installationAbort != nil {
					installationAbort()
				}
				if mcpAbort != nil {
					mcpAbort()
				}
			},
		}, nil
	}); err != nil {
		return fmt.Errorf("attach live installation settings: %w", err)
	}
	var scheduleSvc *schedule.Service
	var registry *registryclient.Client
	workflowOptions := []workflow.Option{
		workflow.WithBundleManager(workflowRuntime.Bundles),
		workflow.WithReferenceResolver(func(workflowID string) []workflow.SourceReference {
			references := make([]workflow.SourceReference, 0)
			for _, block := range app.Settings().UI.LauncherItems {
				if block.Type == "workflow" && block.WorkflowID == workflowID {
					label := block.Label
					if label == "" {
						label = block.ID
					}
					references = append(references, workflow.SourceReference{Kind: "launcher", ID: block.ID, Label: label})
				}
			}
			return references
		}),
	}
	if strings.TrimRight(config.RegistryURL, "/") == serviceconfig.DefaultRegistryURL {
		workflowOptions = append(workflowOptions, workflow.WithWalletPage(wailsApp.Browser, serviceconfig.DefaultWalletURL))
	}
	if strings.TrimSpace(config.RegistryURL) != "" {
		tokens := config.RegistryTokens
		if tokens == nil && strings.TrimSpace(config.OIDCClientID) != "" {
			credentialScope, scopeErr := storage.CredentialScope(roots)
			if scopeErr != nil {
				return fmt.Errorf("resolve profile account identity: %w", scopeErr)
			}
			credentialScope = serviceCredentialScope(credentialScope, config)
			session, sessionErr := nativeoidc.New(nativeoidc.Config{
				Credentials: securestore.New(), CredentialScope: credentialScope, AccountURL: config.AccountURL,
				AuthorizationEndpoint: config.OIDCAuthorizationEndpoint,
				TokenEndpoint:         config.OIDCTokenEndpoint, ClientID: config.OIDCClientID,
				Audience: config.OIDCAudience, Scopes: []string{"openid", "profile", "offline_access"},
				CallbackAddress: config.OIDCCallbackAddress, Browser: wailsApp.Browser,
				UserinfoEndpoint: config.OIDCUserinfoEndpoint,
			})
			if sessionErr != nil {
				return fmt.Errorf("initialize native OIDC session: %w", sessionErr)
			}
			tokens = session
			workflowOptions = append(workflowOptions, workflow.WithRegistryAccount(session), workflow.WithWallet(securestore.New(), credentialScope+"\x00"+config.OIDCTokenEndpoint+"\x00"+config.RegistryURL, wailsApp.Browser))
		}
		registryClient, registryErr := registryclient.New(registryclient.Options{
			BaseURL: config.RegistryURL, Tokens: tokens,
			AllowLoopbackHTTP: config.RegistryAllowLoopbackHTTP,
		})
		if registryErr != nil {
			return fmt.Errorf("initialize Registry client: %w", registryErr)
		}
		registry = registryClient
		workflowOptions = append(workflowOptions, workflow.WithRegistryClient(registryClient))
		if config.HubURL != "" {
			community, err := communityclient.New(config.HubURL, tokens, config.HubAllowLoopbackHTTP)
			if err != nil {
				return err
			}
			workflowOptions = append(workflowOptions, workflow.WithCommunity(community))
		}
		workflowOptions = append(workflowOptions, workflow.WithRegistryState(filepath.Join(roots.Data, "registry-installations.json")))
	}
	workflowOptions = append(workflowOptions, workflow.WithAuthoringContext(observation))
	workflowSvc, err := workflow.NewService(workflowRuntime.Application, workflowOptions...)
	if err != nil {
		return fmt.Errorf("initialize workflow service: %w", err)
	}
	aiAuthoring, err := aiauthoring.NewManager(workflowRuntime.Application, workflowRuntime.Builtins, time.Now, observation)
	if err != nil {
		return fmt.Errorf("initialize AI authoring: %w", err)
	}
	aiConversations, err := aiauthoring.NewConversationStore(filepath.Join(roots.Data, "ai-conversations"), time.Now)
	if err != nil {
		return fmt.Errorf("initialize AI conversation store: %w", err)
	}
	if err := aiAuthoring.AttachConversationStore(aiConversations); err != nil {
		return fmt.Errorf("attach AI conversation store: %w", err)
	}

	// ---- HotkeyRegistry：所有热键的中央 manifest ----
	// 系统、录制、Schedule 与 editor 热键全部走这条路。
	// 用户可在 Settings → 快捷键 tab 改任意一条，hot reload 立即生效。
	var recordingSvc *recording.Service
	hotkeyRegistry := hotkey.NewHotkeyRegistryWithCallbacks(sharedHotkeys, hotkey.Callbacks{
		OnActionChange: func(workflowID, newStr string) error {
			_, _, err := app.MutateSettings(func(cur *services.Settings) error {
				if cur.UI.WorkflowHotkeys == nil {
					cur.UI.WorkflowHotkeys = map[string]string{}
				}
				if strings.TrimSpace(newStr) == "" {
					delete(cur.UI.WorkflowHotkeys, workflowID)
				} else {
					cur.UI.WorkflowHotkeys[workflowID] = newStr
				}
				return nil
			})
			return err
		},
		// OnSystemChange 写回 settings.UI 的 exact binding。
		OnSystemChange: func(key, newStr string) error {
			switch key {
			case "system.execution-stop", "recording.start", "recording.stop", "recording.pause", "recording.cancel",
				"system.calibrate-toggle", "system.launcher-toggle", "tools.window-capture", "paths.mark":
			default:
				return nil
			}
			_, _, err := app.MutateSettings(func(cur *services.Settings) error {
				switch key {
				case "system.execution-stop":
					cur.UI.ActionStopHotkey = newStr
				case "recording.stop":
					cur.UI.RecordingStopHotkey = newStr
				case "recording.start":
					cur.UI.RecordingStartHotkey = newStr
				case "recording.pause":
					cur.UI.RecordingPauseHotkey = newStr
				case "recording.cancel":
					cur.UI.RecordingCancelHotkey = newStr
				case "system.calibrate-toggle":
					cur.UI.CalibrateHotkey = newStr
				case "system.launcher-toggle":
					cur.UI.LauncherToggleHotkey = newStr
				case "tools.window-capture":
					cur.UI.WindowCaptureHotkey = newStr
				case "paths.mark":
					cur.UI.PathMarkHotkey = newStr
				}
				return nil
			})
			var committed interface{ Committed() bool }
			refreshRecordingHotkeys := func() {
				if recordingSvc != nil && strings.HasPrefix(key, "recording.") {
					go recording.RefreshHotkeys(recordingSvc)
				}
			}
			if err != nil && errors.As(err, &committed) && committed.Committed() {
				refreshRecordingHotkeys()
				rootLog.Warn().Err(err).Str("tag", "SETTINGS").Str("hotkey", key).
					Msg("hotkey settings committed but durability sync failed")
				return nil
			}
			if err == nil {
				refreshRecordingHotkeys()
			}
			return err
		},
		EmitChanged: func() { app.Emit("hotkey:changed", map[string]any{}) },
	})

	// 暴露 HotkeyService RPC 给前端
	// 「重置默认」用的内置热键出厂默认 (跟 services.defaultSettings 一致, 也是下方各 Register 的 fallback)。
	hotkeyDefaults := map[string]string{
		"system.execution-stop":   "Ctrl+Shift+F9",
		"system.calibrate-toggle": "F8",
		"system.launcher-toggle":  "",
		"tools.window-capture":    "F9",
		"recording.start":         "F10",
		"recording.stop":          "F12",
		"recording.pause":         "F11",
		"recording.cancel":        "F7",
		"paths.mark":              "F6",
	}
	runWorkflowHotkey := func(workflowID string) {
		go func() {
			if active := workflowRuntime.Application.ActiveSourceRuns(workflowID); len(active) != 0 {
				for _, runID := range active {
					if _, err := workflowRuntime.Application.CancelRun(context.Background(), runID); err != nil {
						rootLog.Warn().Err(err).Str("tag", "HOTKEY").Str("workflowId", workflowID).Str("runId", runID).
							Msg("workflow hotkey failed to cancel Run")
					}
				}
				return
			}
			result, err := workflowRuntime.Application.StartRun(context.Background(), yottaapplication.StartRunRequest{
				WorkflowID: workflowID, Principal: "local-user",
			})
			if err != nil {
				rootLog.Warn().Err(err).Str("tag", "HOTKEY").Str("workflowId", workflowID).
					Msg("workflow hotkey failed to start Run")
				return
			}
			rootLog.Debug().Str("tag", "HOTKEY").Str("workflowId", workflowID).Str("runId", result.Record.Admission().RunID).
				Msg("workflow hotkey started Run")
		}()
	}
	launcherHotkeys := &launcherHotkeyController{
		registry: hotkeyRegistry,
		settings: app.Settings,
		list:     workflowSvc.ListSources,
		run:      runWorkflowHotkey,
	}
	syncWorkflowEntries := func() {
		views, listErr := workflowSvc.ListSources()
		if listErr != nil {
			rootLog.Warn().Err(listErr).Str("tag", "HOTKEY").Msg("reconcile workflow hotkeys")
			return
		}
		syncWorkflowHotkeys(hotkeyRegistry, app.Settings(), views, runWorkflowHotkey)
	}
	hotkeySvc := hotkey.NewHotkeyServiceWithListHook(hotkeyRegistry, hotkeyDefaults, syncWorkflowEntries)
	syncWorkflowEntries()

	// Asset authoring captures exact installed targets; no Workflow
	// document can inject a native window selector.
	assetSvc := asset.NewService(assetStore, authoringTargets, workflowRuntime.Application, app.Emit)

	scheduleStore, err := schedule.NewStore(filepath.Join(roots.Data, "schedules"))
	if err != nil {
		return fmt.Errorf("initialize schedule store: %w", err)
	}
	// Schedule triggers enter the same durable Workflow Run command as GUI.
	scheduleHotkeyAdapter := &scheduleHotkeyRegistrar{reg: hotkeyRegistry}
	scheduleDaemon := schedule.NewDaemon(
		scheduleStore,
		&workflowRunStarter{application: workflowRuntime.Application},
		scheduleHotkeyAdapter,
	)
	scheduleSvc = schedule.NewService(
		scheduleStore,
		schedule.WithChangeListener(scheduleDaemon.Reload),
		schedule.WithManualFire(scheduleDaemon.FireManual),
	)
	// InputClip remains an authoring asset service; playback reads the
	// exposed nominal BlobRef through explicit blob-read and playback grants.
	clipSvc := newClipService(assetStore, app.Emit)
	macroSvc := newMacroService(assetStore, app.Emit)
	pathRecordingSvc := pathrecording.NewDesktopService(hotkeyRegistry, app.Emit, local.Plugins.PrepareAuthoringEndpoint)

	snippetSvc := snippet.NewServiceWithAuthoring(snippetStore, workflowRuntime.AuthoringProjection, app.Emit)

	// 全局强停热键取消唯一 Application worker 的 queued/running Runs。
	// 设置面板里 UI.ActionStopHotkey 改这一条；空 → 默认 Ctrl+Shift+F9。
	stopAllHk := hotkeyOrDefault(app.Settings().UI.ActionStopHotkey, "Ctrl+Shift+F9")
	if err := hotkeyRegistry.Register("system.execution-stop", hotkey.HotkeySourceSystem,
		"hotkeys.label.system.execution_stop", nil, stopAllHk, "",
		func() {
			stopAllForHotkey(func() error { return workflowRuntime.Application.CancelAll(context.Background()) }, rootLog)
		}); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Str("hotkey", stopAllHk).Msg("注册全局强停热键失败")
	}

	// 校准 F8 走自治 LL hook (calibrationSvc 持有), 不走 OS RegisterHotKey (游戏 reserve).
	// emit 把 'calibration:toggle' 广播给前端推进状态机; vkGetter 读热键中心当前 F8 绑定。
	calibrationSvc := calibration.NewService(
		func(name string, data any) { app.Emit(name, data) },
		func() uint32 {
			_, vk := registryHotkey(hotkeyRegistry, "system.calibrate-toggle", calibration.VKF8)
			return vk
		},
	)

	// 简易录制落原子 Macro；精准录制落 InputClip。两条产品路径共享原生采集器，
	// 但不再共享持久化模型或“保存后加到画布”副作用。
	recordingSvc = newRecordingService(
		app, clipSvc, macroSvc, resourceCreator, hotkeyRegistry, authoringTargets, app.Emit,
	)

	// tools 杂项工具服务：MousePos / 鼠标 HUD / ScreenPicker 等。
	// Wails app 尚未创建；先把可延迟 attach 的 presentation adapter 注入 tools core。
	toolsPresenter := &wailsToolsPresenter{}
	templatePreview, err := newTemplatePreview(sharedBlobStore, authoringTargets)
	if err != nil {
		return fmt.Errorf("initialize template preview: %w", err)
	}
	toolsSvc := tools.NewServiceWithOptions(authoringTargets, toolsPresenter, tools.Options{
		OnPathEditorClose: pathRecordingSvc.StopCurrent,
		TemplateMatcher:   templatePreview,
		OnCalibratorClose: func() {
			calibrationSvc.StopHotkeyWatch()
			_, _ = calibrationSvc.Stop()
		},
		CaptureHotkey: func() (uint32, uint32) {
			return registryHotkey(hotkeyRegistry, "tools.window-capture", 0x78)
		},
		OnLauncherShown:  launcherHotkeys.refresh,
		OnLauncherHidden: launcherHotkeys.clear,
	})

	local.Panels.SetPresenter(func(id string) error {
		if err := toolsSvc.OpenPanels(); err != nil {
			return err
		}
		toolsPresenter.Emit("panels:selected", id)
		return nil
	})

	// 悬浮窗启动器 呼出/隐藏 热键：默认未绑（空），从 settings.UI 读，rebind 经 onSystemHotkeyChange 写回。
	// os-global 机制（跟 execution-stop 一致）。按键 → toggle 启动器悬浮窗显隐。
	launcherToggleHk := strings.TrimSpace(app.Settings().UI.LauncherToggleHotkey)
	if err := hotkeyRegistry.Register("system.launcher-toggle", hotkey.HotkeySourceSystem,
		"hotkeys.label.system.launcher_toggle", nil, launcherToggleHk, "",
		func() { _ = toolsSvc.ToggleLauncher() }); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Str("hotkey", launcherToggleHk).Msg("注册启动器 toggle 热键失败")
	}

	// DPI 校准 toggle 热键：默认 F8，从 settings.UI 读（rebind 经 onSystemHotkeyChange 写回）。
	// LL-hook 机制 (值持有条目, 不占 OS RegisterHotKey — 游戏会 reserve, 切游戏后失效)。
	// 真正装钩由 calibrationSvc 在校准窗开关时做 (StartHotkeyWatch 读上面的 vkGetter);
	// 命中 emit 'calibration:toggle' 推进前端状态机。热键中心仍可见 + rebind + 冲突检测。
	calibHk := hotkeyOrDefault(app.Settings().UI.CalibrateHotkey, "F8")
	if err := hotkeyRegistry.RegisterLLHook("system.calibrate-toggle", hotkey.HotkeySourceSystem,
		"hotkeys.label.system.calibrate_toggle", calibHk, ""); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Str("hotkey", calibHk).Msg("注册 DPI 校准热键失败")
	}

	// 录制热键 (LL-hook 全局拦截, 不占 OS RegisterHotKey — 游戏会 reserve)。
	// 默认从 settings.UI 读; registry 是编辑权威, rebind 经 onSystemHotkeyChange 写回 settings.UI。
	recStartHk := hotkeyOrDefault(app.Settings().UI.RecordingStartHotkey, "F10")
	if err := hotkeyRegistry.RegisterLLHook("recording.start", hotkey.HotkeySourceRecording,
		"hotkeys.label.recording.start", recStartHk, ""); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Str("hotkey", recStartHk).Msg("注册开始录制热键失败")
	}
	recStopHk := hotkeyOrDefault(app.Settings().UI.RecordingStopHotkey, "F12")
	if err := hotkeyRegistry.RegisterLLHook("recording.stop", hotkey.HotkeySourceRecording,
		"hotkeys.label.recording.stop", recStopHk, ""); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Str("hotkey", recStopHk).Msg("注册停录热键失败")
	}
	recPauseHk := hotkeyOrDefault(app.Settings().UI.RecordingPauseHotkey, "F11")
	if err := hotkeyRegistry.RegisterLLHook("recording.pause", hotkey.HotkeySourceRecording,
		"hotkeys.label.recording.pause", recPauseHk, ""); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Str("hotkey", recPauseHk).Msg("注册暂停录制热键失败")
	}
	recCancelHk := hotkeyOrDefault(app.Settings().UI.RecordingCancelHotkey, "F7")
	if err := hotkeyRegistry.RegisterLLHook("recording.cancel", hotkey.HotkeySourceRecording,
		"hotkeys.label.recording.cancel", recCancelHk, ""); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Str("hotkey", recCancelHk).Msg("注册取消录制热键失败")
	}

	// 窗口捕获键 (NodeInspector「捕获目标窗口」按下它抓前台游戏窗口)。
	// 值持有者条目 (mechanism=ll-hook, 不持久占 OS) — 进热键中心可见 + 可 rebind + 冲突检测；
	// 真正监听由 toolsSvc 捕获时临时读取 constructor-pinned getter。默认 F9。
	winCapHk := hotkeyOrDefault(app.Settings().UI.WindowCaptureHotkey, "F9")
	if err := hotkeyRegistry.RegisterLLHook("tools.window-capture", hotkey.HotkeySourceSystem,
		"hotkeys.label.system.window_capture", winCapHk, ""); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Str("hotkey", winCapHk).Msg("注册窗口捕获热键失败")
	}

	if err := hotkeyRegistry.RegisterRecording("paths.mark",
		"hotkeys.label.recording.path_mark", app.Settings().UI.PathMarkHotkey,
		func() { app.Emit("path:mark", nil) }); err != nil {
		rootLog.Warn().Err(err).Str("tag", "HOTKEY").Msg("path mark hotkey registration failed")
	}

	// Runtime resources are declared in dependency order and close in reverse.
	// Triggers stop before the single Workflow worker and its Run Owners.
	applicationRuntime := appruntime.New(
		appruntime.Resource{
			Name:  "local-runtime",
			Start: local.Workflow.Application.Start,
			Close: local.Close,
		},
		appruntime.Resource{
			Name: "mcp-server",
			Start: func(context.Context) error {
				configured := app.Settings().MCP
				return mcpRuntime.Start(mcpserver.RuntimeConfig{Enabled: configured.Enabled, Port: configured.Port})
			},
			Close: mcpRuntime.Close,
		},
		appruntime.Resource{
			Name:  "hotkey-registry",
			Start: func(context.Context) error { return nil },
			Close: hotkeyRegistry.Shutdown,
		},
		appruntime.Resource{
			Name:  "schedule-daemon",
			Start: func(context.Context) error { scheduleDaemon.Start(); return nil },
			Close: scheduleDaemon.StopContext,
		},
		appruntime.Resource{Name: "path-recording", Start: func(context.Context) error { return nil }, Close: func(ctx context.Context) error { return pathrecording.Shutdown(ctx, pathRecordingSvc) }},
		appruntime.Resource{
			Name:  "recording",
			Start: func(context.Context) error { return nil },
			Close: func(ctx context.Context) error { return recording.Shutdown(ctx, recordingSvc) },
		},
		appruntime.Resource{
			Name:  "calibration",
			Start: func(context.Context) error { return nil },
			Close: func(ctx context.Context) error { return calibration.Shutdown(ctx, calibrationSvc) },
		},
		appruntime.Resource{
			Name:  "tools-presentation",
			Start: func(context.Context) error { return nil },
			Close: func(ctx context.Context) error {
				err := tools.Shutdown(ctx, toolsSvc)
				toolsPresenter.Detach()
				return err
			},
		},
	)

	serviceErrors := application.ServiceOptions{MarshalError: apperr.Marshal}
	pluginService := plugins.NewService(local.Plugins, local.Roots)
	if registry != nil {
		pluginService = plugins.NewService(local.Plugins, local.Roots, registry)
	}
	wailsServices = append(wailsServices,
		application.NewServiceWithOptions(settingsSvc, serviceErrors),
		application.NewServiceWithOptions(pluginService, serviceErrors),
		application.NewServiceWithOptions(panels.NewService(local.Panels), serviceErrors),
		application.NewServiceWithOptions(services.NewMCPService(), serviceErrors),
		application.NewServiceWithOptions(services.NewAppInfoService(), serviceErrors),
		application.NewServiceWithOptions(workflowSvc, serviceErrors),
		application.NewServiceWithOptions(hotkeySvc, serviceErrors),
		application.NewServiceWithOptions(assetSvc, serviceErrors),
		application.NewServiceWithOptions(pathRecordingSvc, serviceErrors),
		application.NewServiceWithOptions(scheduleSvc, serviceErrors),
		application.NewServiceWithOptions(calibrationSvc, serviceErrors),
		application.NewServiceWithOptions(recordingSvc, serviceErrors),
		application.NewServiceWithOptions(resourceAuthoringSvc, serviceErrors),
		application.NewServiceWithOptions(toolsSvc, serviceErrors),
		application.NewServiceWithOptions(clipSvc, serviceErrors),
		application.NewServiceWithOptions(macroSvc, serviceErrors),
		application.NewServiceWithOptions(snippetSvc, serviceErrors),
		application.NewServiceWithOptions(services.NewAIService(app, aiSecrets, aiAuthoring), serviceErrors),
		application.NewServiceWithOptions(services.NewAutomationService(app), serviceErrors),
	)
	for _, service := range wailsServices {
		wailsApp.RegisterService(service)
	}
	if err := app.AttachEmitter(func(name string, data any) { wailsApp.Event.Emit(name, data) }); err != nil {
		return fmt.Errorf("attach presentation emitter: %w", err)
	}

	// tools secondary windows/event delivery become ready with the GUI runtime.
	toolsPresenter.Attach(wailsApp)

	// 装配统一日志 transport（system/runtime/dump/trace 都经 log:batch）。
	logSink.SetEmit(func(e services.LogBatchEvent) {
		if app.LogStreamingEnabled() {
			wailsApp.Event.Emit(services.EventLogBatch, e)
		}
	})

	// 注册事件 payload 类型（让 bindings 生成器产 TS 类型）
	application.RegisterEvent[services.LogBatchEvent](services.EventLogBatch)

	// 主窗口尺寸读 settings（用户上次拖到的尺寸），frameless 让前端自己画 title bar
	winCfg := app.Settings().UI.Window
	mainWin := wailsApp.Window.NewWithOptions(mainWindowOptions(winCfg.Width, winCfg.Height))
	toolsPresenter.AttachMain(mainWin)
	mainActivator.attach(func() {
		mainWin.Show()
		mainWin.Restore()
		mainWin.Focus()
	})
	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		mainActivator.markReady()
	})

	// 用户拖完才落盘（WindowDidResize 拖动期间会狂刷，没必要每帧写 IO）。
	// settings.UI.Window 不走 SettingsService.Update 的 patch 流程 —— 这只是 UI 状态，
	// 不需要前端订阅或校验，直接绕过 swap 直接写。
	mainWin.OnWindowEvent(events.Windows.WindowEndResize, func(_ *application.WindowEvent) {
		w, h := mainWin.Size()
		if w < 100 || h < 100 {
			return
		}
		app.UpdateWindowSize(w, h)
	})

	// 系统托盘：始终常驻。左键单击 toggle 窗口显隐；右键弹菜单可强制退出。
	// 即使 MinimizeToTray=false 也保留托盘 —— 用户万一拿不到主窗口时还有个出口。
	//
	// 故意不用 tray.AttachWindow()：它内部走 PositionWindow，会把窗口拽到托盘附近
	// （桌面右下角），还会让部分内容溢出屏幕。我们要的是"回到上次位置"，
	// 所以手动 OnClick → Show().Focus()（操作系统会把它还原到 Hide() 前的位置）。
	tray := wailsApp.SystemTray.New()
	tray.SetIcon(config.TrayIcon).SetTooltip("Yotta " + version.Version)
	tray.OnClick(func() {
		if mainWin.IsVisible() && !mainWin.IsMinimised() {
			mainWin.Hide()
		} else {
			mainActivator.request()
		}
	})
	trayMenu := application.NewMenu()
	trayMenu.Add("显示窗口").OnClick(func(*application.Context) { mainActivator.request() })
	trayMenu.AddSeparator()
	trayMenu.Add("退出 Yotta").OnClick(func(*application.Context) { wailsApp.Quit() })
	tray.SetMenu(trayMenu)

	// 关闭按钮（X）行为：MinimizeToTray=true 时 cancel 事件 + 隐藏到托盘；
	// false 时明确退出整个 app，不能依赖“最后一个窗口关闭”：启动器等隐藏
	// secondary window 仍然存活时，默认行为只会销毁主窗并继续占用 profile。
	mainWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		// Wails closes every window after cancelling the application context.
		// Do not intercept that unconditional shutdown pass.
		if wailsApp.Context().Err() != nil {
			return
		}
		handleMainWindowClosing(
			app.Settings().UI.MinimizeToTray,
			e.Cancel,
			func() { mainWin.Hide() },
			wailsApp.Quit,
		)
	})

	// 每次启动都刷新计划任务中的绝对路径，避免 exe 移动后自启动指向旧位置。
	if err := services.ApplyAutostart(app.Settings().UI.Autostart); err != nil {
		rootLog.Warn().Err(err).Str("tag", "SYSTEM").Msg("启动期自启计划任务同步失败")
	}

	if err := applicationRuntime.Start(context.Background()); err != nil {
		rootLog.Error().Err(err).Str("tag", "STARTUP").Msg("application runtime start")
		return fmt.Errorf("start application runtime: %w", err)
	}
	rootLog.Info().Str("tag", "SYSTEM").Str("version", version.Version).Msg("Yotta started")

	// Wails releases its single-instance mutex during shutdown. Close the
	// storage-owning runtime first so an immediate restart cannot acquire the
	// instance mutex and then collide with the still-held writer lease.
	var closeRuntimeOnce sync.Once
	closeApplicationRuntime := func() {
		closeRuntimeOnce.Do(func() {
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelShutdown()
			if err := applicationRuntime.Close(shutdownCtx); err != nil {
				rootLog.Warn().Err(err).Str("tag", "SHUTDOWN").Msg("application runtime close")
			}
		})
	}
	wailsApp.OnShutdown(closeApplicationRuntime)

	// 阻塞直到关窗口
	runErr := wailsApp.Run()
	// Run can fail before Wails enters its normal cleanup path.
	closeApplicationRuntime()
	if runErr != nil {
		return fmt.Errorf("run Wails application: %w", runErr)
	}
	return nil
}

func stopAllForHotkey(stopAll func() error, log zerolog.Logger) {
	if err := stopAll(); err != nil {
		log.Warn().Err(err).Str("tag", "SYSTEM").Msg("全局强停失败")
	}
}

func hotkeyOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func registryHotkey(registry *hotkey.HotkeyRegistry, key string, fallback uint32) (uint32, uint32) {
	entry, ok := registry.Get(key)
	if !ok || entry.HotkeyStr == "" {
		return 0, fallback
	}
	mods, vk, err := hotkey.ParseHotkey(entry.HotkeyStr)
	if err != nil || vk == 0 {
		return 0, fallback
	}
	return mods, vk
}

func newWorkflowLogEmitter(log zerolog.Logger) noderuntime.LogEmitter {
	return noderuntime.LogEmitterFunc(func(ctx context.Context, entry noderuntime.LogEntry) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event *zerolog.Event
		switch entry.Level {
		case "debug":
			event = log.Debug()
		case "warn":
			event = log.Warn()
		case "error":
			event = log.Error()
		default:
			event = log.Info()
		}
		event.Str("tag", "WORKFLOW").Str("graphId", entry.GraphID).Str("nodeId", entry.NodeID).
			Str("invocationId", entry.InvocationID).Int("attempt", entry.Attempt)
		if entry.Failure != nil {
			event.Interface("failure", entry.Failure)
		}
		event.Msg(entry.Message)
		return nil
	})
}
