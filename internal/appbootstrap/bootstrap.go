// Package appbootstrap constructs the production Yotta application with
// immutable contracts, durable stores, installed providers, admission, and a
// single Program worker.
package appbootstrap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/yottaapp/yotta/internal/admission"
	"github.com/yottaapp/yotta/internal/ai"
	"github.com/yottaapp/yotta/internal/appcontrol"
	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/artifact"
	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/hostapi"
	"github.com/yottaapp/yotta/internal/httpegress"
	"github.com/yottaapp/yotta/internal/nodeadapter"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/nodepackage"
	"github.com/yottaapp/yotta/internal/noderuntime"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/panel"
	"github.com/yottaapp/yotta/internal/pluginhost"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/runprepare"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/stream"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowbundle"
	"github.com/yottaapp/yotta/internal/workflowstore"
	"github.com/yottaapp/yotta/internal/workspacefs"
)

type Limits struct {
	MaxSources              int
	MaxPrograms             int
	MaxProgramCacheBytes    int64
	MaxRuns                 int
	MaxResourcePayloadBytes int
	BlobChunkBytes          int
	BlobQueueCapacity       int
	StreamCapacity          int
	StreamChunkBytes        int
}

type Config struct {
	Panels                   *panel.Service
	DataRoot                 string
	ProgramCacheRoot         string
	WorkflowRepository       *catalog.WorkflowRepository
	RunRepository            *catalog.RunRepository
	BlobStore                *blob.Store
	Limits                   Limits
	AIInstallations          ai.Installations
	HTTPInstallations        httpegress.Installations
	ApplicationInstallations appcontrol.Installations
	AutomationInstallations  automationinstalled.Installations
	ScriptRuntime            *scriptengine.Runtime
	NodePackageStore         *nodepackage.Store
	WasmRunnerExecutable     string
	PluginExecution          pluginhost.ProcessHostOptions
	LogEmitter               noderuntime.LogEmitter
	OwnerCloseTimeout        time.Duration
	Now                      func() time.Time
	OnRunEvent               func(appcore.RunEvent)
	OnDebugEvent             func(appcore.DebugEvent)
}

type Runtime struct {
	Application         *appcore.Application
	Builtins            nodes.Builtins
	BlobStore           *blob.Store
	Bundles             *workflowbundle.Manager
	AuthoringProjection nodeauthoring.Snapshot
	ai                  ai.Installations
	http                httpegress.Installations
	automationMu        sync.Mutex
	current             *sealedExecutionEnvironment
	retired             []automationinstalled.Generation
	authoring           automationinstalled.AuthoringTargets
	factory             executionEnvironmentFactory
	closed              bool
	closeErr            error
}

func Build(config Config) (*Runtime, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	if !config.AIInstallations.Valid() || !config.HTTPInstallations.Valid() || !config.ApplicationInstallations.Valid() || !config.AutomationInstallations.Valid() || config.BlobStore == nil || config.WorkflowRepository == nil || config.RunRepository == nil || config.ScriptRuntime == nil || config.LogEmitter == nil || config.OwnerCloseTimeout <= 0 {
		return nil, errors.New("app bootstrap requires configured installations and effect runtimes")
	}
	if err := validateLimits(config.Limits); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(config.DataRoot)
	if err != nil || config.DataRoot == "" {
		return nil, errors.New("app bootstrap requires a data root")
	}
	builtins, err := nodes.Build()
	if err != nil {
		return nil, fmt.Errorf("build Catalog: %w", err)
	}
	catalog := builtins.Catalog
	var runtimePackages []nodepackage.RuntimePackage
	var processPlugins *pluginhost.ProcessHost
	var wasmPlugins *pluginhost.WasmHost
	pluginFeatures := []string{}
	if config.NodePackageStore != nil {
		runtimePackages, err = config.NodePackageStore.RuntimePackages(context.Background(), nodepackage.RuntimeHost{
			APIGeneration: hostapi.Current, OperatingSystem: runtime.GOOS, Architecture: runtime.GOARCH,
		})
		if err != nil {
			return nil, fmt.Errorf("project enabled node packages: %w", err)
		}
		if len(runtimePackages) != 0 {
			projection, err := pluginhost.MergeCatalog(catalog, runtimePackages, nodes.GeneratorVersion)
			if err != nil {
				return nil, err
			}
			catalog = projection.Catalog
			hasProcess, hasWasm := false, false
			for _, runtimePackage := range runtimePackages {
				for _, node := range runtimePackage.Nodes {
					hasProcess = hasProcess || node.Implementation.ABI.Kind == nodecontract.ABIProcess
					hasWasm = hasWasm || node.Implementation.ABI.Kind == nodecontract.ABIWIT
				}
			}
			if hasProcess {
				processPlugins, err = pluginhost.NewProcessHost(catalog, config.PluginExecution)
				if err != nil {
					return nil, err
				}
				pluginFeatures = append(pluginFeatures, processPlugins.HostFeatures()...)
			}
			if hasWasm {
				wasmPlugins, err = pluginhost.NewWasmHost(catalog, pluginhost.WasmHostOptions{
					RunnerExecutable: config.WasmRunnerExecutable, Execution: config.PluginExecution,
				})
				if err != nil {
					return nil, err
				}
				pluginFeatures = append(pluginFeatures, wasmPlugins.HostFeatures()...)
			}
		}
	}
	slices.Sort(pluginFeatures)
	pluginFeatures = slices.Compact(pluginFeatures)
	config.AIInstallations, err = config.AIInstallations.ForEvaluationArtifacts(builtins.AIEvaluationArtifacts())
	if err != nil {
		return nil, err
	}
	bindings := catalog.Bindings()
	contracts := make([]nodecontract.Contract, 0, len(bindings))
	for _, binding := range bindings {
		contracts = append(contracts, binding.Contract)
	}
	packageDependencies := []schema.NodePackageDependency{}
	for _, p := range runtimePackages {
		dependency := schema.NodePackageDependency{PublisherNamespace: p.PublisherNamespace, PackageID: p.PackageID, PackageVersion: p.PackageVersion, ManifestDigest: p.ManifestDigest}
		for _, node := range p.Nodes {
			dependency.NodeRefs = append(dependency.NodeRefs, node.Contract.NodeRef())
		}
		packageDependencies = append(packageDependencies, dependency)
	}
	authoringProjection, err := nodeauthoring.Project(nodeauthoring.Input{
		Catalog: catalog, Types: catalog.Types(), Capabilities: catalog.Capabilities(),
		Contracts: contracts, GeneratorVersion: nodes.GeneratorVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("build Authoring Projection: %w", err)
	}
	build, err := compiler.BuildDigest()
	if err != nil {
		return nil, fmt.Errorf("build compiler identity: %w", err)
	}
	workspace, err := resolveWorkspaceRoot(root)
	if err != nil {
		return nil, err
	}
	parameters, err := workflowstore.OpenParameterStore(filepath.Join(root, "workflow-parameters"))
	if err != nil {
		return nil, err
	}
	sources, err := workflowstore.OpenSourceStore(config.WorkflowRepository, workflowstore.SourceStoreOptions{
		MaxSources: config.Limits.MaxSources, Now: config.Now, Parameters: parameters,
		MigrationHistoryRoot: filepath.Join(root, "workflow-source-migrations"),
	})
	if err != nil {
		return nil, err
	}
	programs, err := workflowstore.OpenProgramStore(
		config.ProgramCacheRoot, catalog, builtins.ConfigValidators, build,
		workflowstore.ProgramStoreOptions{
			MaxPrograms: config.Limits.MaxPrograms,
			MaxBytes:    config.Limits.MaxProgramCacheBytes,
			Now:         config.Now,
		},
	)
	if err != nil {
		return nil, err
	}
	runs, err := run.OpenStore(config.RunRepository, catalog, run.StoreOptions{MaxRecords: config.Limits.MaxRuns})
	if err != nil {
		return nil, err
	}
	blobStore := config.BlobStore
	blobProvider, err := blob.NewProvider(blobStore, blob.ProviderLimits{
		MaxChunkBytes: config.Limits.BlobChunkBytes, QueueCapacity: config.Limits.BlobQueueCapacity,
	})
	if err != nil {
		return nil, err
	}
	streamProvider, err := stream.NewProvider(stream.Limits{
		MaxCapacity: config.Limits.StreamCapacity, MaxChunkBytes: config.Limits.StreamChunkBytes,
	})
	if err != nil {
		return nil, err
	}
	workspaceFileProvider, err := workspacefs.NewProvider(filepath.Join(workspace, "files"), workspacefs.Limits{
		MaxReadBytes: nodes.DefaultImageFileBytes, MaxWriteBytes: nodes.DefaultImageFileBytes, MaxChunkBytes: 64 << 10,
	})
	if err != nil {
		return nil, err
	}
	blobDigest, err := blob.ProviderArtifactDigest()
	if err != nil {
		return nil, err
	}
	streamDigest, err := stream.ProviderArtifactDigest()
	if err != nil {
		return nil, err
	}
	workspaceFileDigest, err := workspacefs.ProviderArtifactDigest()
	if err != nil {
		return nil, err
	}
	environmentFactory, err := newExecutionEnvironmentFactory(executionEnvironmentFactory{
		builtins: builtins, blobDigest: blobDigest, streamDigest: streamDigest, workspaceFileDigest: workspaceFileDigest,
		scriptRuntime: config.ScriptRuntime, pluginFeatures: pluginFeatures,
		ai: config.AIInstallations, http: config.HTTPInstallations,
		baseProviders: map[string]run.InstalledProvider{
			blob.ProviderID:        {ArtifactDigest: blobDigest, ABI: blob.ProviderABI, Provider: blobProvider},
			stream.ProviderID:      {ArtifactDigest: streamDigest, ABI: stream.ProviderABI, Provider: streamProvider},
			workspacefs.ProviderID: {ArtifactDigest: workspaceFileDigest, ABI: workspacefs.ProviderABI, Provider: workspaceFileProvider},
		},
	})
	if err != nil {
		return nil, err
	}
	environment, err := environmentFactory.seal(config.ApplicationInstallations, config.AutomationInstallations)
	if err != nil {
		return nil, err
	}
	environmentOwned := false
	defer func() {
		if !environmentOwned {
			_ = environment.generation.Retire()
		}
	}()
	admitter, err := admission.New(catalog, environment.profile, runs, environment.policy, admission.Options{
		Now: config.Now,
	})
	if err != nil {
		return nil, err
	}
	adapters, err := noderuntime.Installed(builtins, noderuntime.Dependencies{Script: config.ScriptRuntime, Log: config.LogEmitter, Panels: config.Panels})
	if err != nil {
		return nil, err
	}
	mergePluginAdapters := func(installed map[string]nodeadapter.InstalledAdapter, err error) error {
		if err != nil {
			return err
		}
		for entrypoint, adapter := range installed {
			if _, conflict := adapters[entrypoint]; conflict {
				return fmt.Errorf("plugin adapter entrypoint %q conflicts with an installed adapter", entrypoint)
			}
			adapters[entrypoint] = adapter
		}
		return nil
	}
	if processPlugins != nil {
		if err := mergePluginAdapters(processPlugins.Adapters(runtimePackages)); err != nil {
			return nil, err
		}
	}
	if wasmPlugins != nil {
		if err := mergePluginAdapters(wasmPlugins.Adapters(runtimePackages)); err != nil {
			return nil, err
		}
	}
	executor := compiler.NewExecutor(catalog, adapters, compiler.ExecutorOptions{Now: config.Now})
	runImagePlanner, err := runprepare.New(blobStore)
	if err != nil {
		return nil, err
	}
	application, err := appcore.New(appcore.Config{
		Parameters:   parameters,
		NodePackages: packageDependencies,
		Catalog:      catalog, Authoring: authoringProjection, CompilerBuild: build, ConfigValidators: builtins.ConfigValidators,
		BlobVerifier:    blobStore,
		RunImagePlanner: runImagePlanner,
		Sources:         sources, Programs: programs, Runs: runs,
		Admitter: admitter, Executor: executor,
		Providers: environment.providers, TargetSnapshot: environment.acquire,
		ResourceOptions: resource.Options{
			MaxPayloadBytes: config.Limits.MaxResourcePayloadBytes,
		},
		OwnerCloseTimeout: config.OwnerCloseTimeout, Now: config.Now,
		OnRunEvent: config.OnRunEvent, OnDebugEvent: config.OnDebugEvent,
	})
	if err != nil {
		return nil, err
	}
	authoringTargets, err := automationinstalled.NewAuthoringTargets(environment.generation)
	if err != nil {
		return nil, err
	}
	bundles, err := workflowbundle.New(application, blobStore, config.Panels)
	if err != nil {
		return nil, err
	}
	environmentOwned = true
	return &Runtime{
		Application: application, Builtins: builtins, BlobStore: blobStore, Bundles: bundles,
		AuthoringProjection: authoringProjection,
		ai:                  config.AIInstallations, http: config.HTTPInstallations,
		current: environment, authoring: authoringTargets, factory: environmentFactory,
	}, nil
}

func validateLimits(limits Limits) error {
	if limits.MaxSources <= 0 || limits.MaxPrograms <= 0 || limits.MaxProgramCacheBytes <= 0 || limits.MaxRuns <= 0 ||
		limits.MaxResourcePayloadBytes < 2*nodes.DefaultFileReadBytes || limits.BlobChunkBytes <= 0 || limits.BlobChunkBytes > limits.MaxResourcePayloadBytes ||
		limits.BlobQueueCapacity <= 0 || limits.StreamCapacity <= 0 || limits.StreamChunkBytes <= 0 ||
		limits.StreamChunkBytes > limits.MaxResourcePayloadBytes {
		return errors.New("app bootstrap limits are invalid")
	}
	return nil
}

func buildHostProfile(builtins nodes.Builtins, blobDigest, streamDigest, workspaceFileDigest artifact.Digest, scriptRuntime *scriptengine.Runtime, pluginFeatures []string, aiInstallations ai.Installations) (admission.HostProfile, error) {
	lookup := func(id string) (capability.Ref, error) {
		definition, ok := builtins.Catalog.LookupCapability(id)
		if !ok {
			return capability.Ref{}, fmt.Errorf("built-in capability %q is missing", id)
		}
		return definition.Ref(), nil
	}
	blobRead, err := lookup(nodes.BlobReadCapabilityID)
	if err != nil {
		return admission.HostProfile{}, err
	}
	blobWrite, err := lookup(nodes.BlobWriteCapabilityID)
	if err != nil {
		return admission.HostProfile{}, err
	}
	streamSession, err := lookup(nodes.StreamCapabilityID)
	if err != nil {
		return admission.HostProfile{}, err
	}
	aiGeneration, err := lookup(nodes.AIGenerationCapabilityID)
	if err != nil {
		return admission.HostProfile{}, err
	}
	filesystem, err := lookup(nodes.FilesystemCapabilityID)
	if err != nil {
		return admission.HostProfile{}, err
	}
	draft := admission.HostProfileDraft{
		OS: runtime.GOOS, Architecture: runtime.GOARCH, HostAPIGeneration: hostapi.Current,
		Features: append(scriptRuntime.HostFeatures(), pluginFeatures...),
		Providers: []admission.ProviderDescriptor{
			{ID: blob.ProviderID, ArtifactDigest: blobDigest, ABI: blob.ProviderABI, PluginInstanceID: "builtin",
				OperatingSystems: []string{runtime.GOOS}, Architectures: []string{runtime.GOARCH}, HostAPIs: hostapi.Supported(),
				Capabilities: []admission.ProviderCapability{
					{Capability: blobRead, ResourceKind: blob.KindReader},
					{Capability: blobWrite, ResourceKind: blob.KindWriter},
				}},
			{ID: stream.ProviderID, ArtifactDigest: streamDigest, ABI: stream.ProviderABI, PluginInstanceID: "builtin",
				OperatingSystems: []string{runtime.GOOS}, Architectures: []string{runtime.GOARCH}, HostAPIs: hostapi.Supported(),
				Capabilities: []admission.ProviderCapability{{Capability: streamSession, ResourceKind: stream.Kind}}},
			{ID: workspacefs.ProviderID, ArtifactDigest: workspaceFileDigest, ABI: workspacefs.ProviderABI, PluginInstanceID: "builtin",
				OperatingSystems: []string{runtime.GOOS}, Architectures: []string{runtime.GOARCH}, HostAPIs: hostapi.Supported(),
				Capabilities: []admission.ProviderCapability{{Capability: filesystem, ResourceKind: workspacefs.Kind}}},
		},
		Targets: []admission.AutomationTarget{
			{ID: "workspace", Kind: "blob-store", ProviderID: blob.ProviderID},
			{ID: "memory", Kind: "stream-session", ProviderID: stream.ProviderID},
			{ID: workspacefs.TargetID, Kind: workspacefs.TargetKind, ProviderID: workspacefs.ProviderID},
		},
		TargetSlots: []admission.TargetSlotBinding{{Slot: "workspace-files", TargetID: workspacefs.TargetID}},
	}
	providerIDs := map[string]struct{}{blob.ProviderID: {}, stream.ProviderID: {}, workspacefs.ProviderID: {}}
	for _, installed := range aiInstallations.Entries() {
		if _, exists := providerIDs[installed.ProviderID]; !exists {
			draft.Providers = append(draft.Providers, admission.ProviderDescriptor{
				ID: installed.ProviderID, ArtifactDigest: installed.ProviderArtifact, ABI: ai.ProviderABI, PluginInstanceID: "builtin",
				OperatingSystems: []string{runtime.GOOS}, Architectures: []string{runtime.GOARCH}, HostAPIs: hostapi.Supported(),
				Capabilities: []admission.ProviderCapability{{Capability: aiGeneration, ResourceKind: ai.KindModelSession}},
			})
			providerIDs[installed.ProviderID] = struct{}{}
		}
		draft.Targets = append(draft.Targets, admission.AutomationTarget{
			ID: installed.TargetID, Kind: "ai-model", ProviderID: installed.ProviderID,
		})
		draft.Credentials = append(draft.Credentials, admission.CredentialBinding{
			ID: installed.CredentialBindingID, ProviderID: installed.ProviderID, Capability: aiGeneration,
		})
		draft.TargetSlots = append(draft.TargetSlots, admission.TargetSlotBinding{Slot: installed.Slot, TargetID: installed.TargetID})
		draft.CredentialSlots = append(draft.CredentialSlots, admission.CredentialSlotBinding{
			Slot: installed.Slot, CredentialID: installed.CredentialBindingID,
		})
	}
	return admission.SealHostProfile(draft)
}
