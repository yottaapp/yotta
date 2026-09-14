package appbootstrap

import (
	"errors"
	"fmt"
	"maps"

	"github.com/yottaapp/yotta/internal/admission"
	"github.com/yottaapp/yotta/internal/ai"
	"github.com/yottaapp/yotta/internal/appcontrol"
	"github.com/yottaapp/yotta/internal/artifact"
	automationinstalled "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/httpegress"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/resource"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/targetruntime"
)

type executionEnvironmentFactory struct {
	builtins            nodes.Builtins
	blobDigest          artifact.Digest
	streamDigest        artifact.Digest
	workspaceFileDigest artifact.Digest
	scriptRuntime       *scriptengine.Runtime
	pluginFeatures      []string
	ai                  ai.Installations
	http                httpegress.Installations
	baseProviders       map[string]run.InstalledProvider
}

// executionEnvironmentFactory is the single projection seam for installed
// target facts. Each call seals a profile, policy, provider snapshot, and
// automation generation that must be published together.
type sealedExecutionEnvironment struct {
	profile    admission.HostProfile
	policy     admission.Policy
	providers  map[string]run.InstalledProvider
	generation automationinstalled.Generation
	targets    targetruntime.Snapshot
}

func newExecutionEnvironmentFactory(config executionEnvironmentFactory) (executionEnvironmentFactory, error) {
	if !config.blobDigest.Valid() || !config.streamDigest.Valid() ||
		!config.workspaceFileDigest.Valid() || config.scriptRuntime == nil ||
		!config.ai.Valid() || !config.http.Valid() || config.baseProviders == nil {
		return executionEnvironmentFactory{}, errors.New("execution environment factory requires trusted fixed installations")
	}
	config.baseProviders = maps.Clone(config.baseProviders)
	providers := maps.Clone(config.baseProviders)
	if err := mergeEnvironmentInstallations(providers, config.ai.Entries(), ai.ProviderABI, "AI",
		func(installed ai.Installation) (string, artifact.Digest, resource.Provider) {
			return installed.ProviderID, installed.ProviderArtifact, installed.Provider
		}); err != nil {
		return executionEnvironmentFactory{}, err
	}
	config.pluginFeatures = append([]string(nil), config.pluginFeatures...)
	return config, nil
}

func (factory executionEnvironmentFactory) withInstallations(
	aiInstallations ai.Installations,
	httpInstallations httpegress.Installations,
) (executionEnvironmentFactory, error) {
	factory.ai = aiInstallations
	factory.http = httpInstallations
	return newExecutionEnvironmentFactory(factory)
}

func (factory executionEnvironmentFactory) seal(
	applications appcontrol.Installations,
	automation automationinstalled.Installations,
) (_ *sealedExecutionEnvironment, resultErr error) {
	config := factory
	if !applications.Valid() || !automation.Valid() || config.baseProviders == nil {
		return nil, errors.New("execution environment installations are unavailable")
	}
	generation, err := automationinstalled.NewGeneration(automation)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, generation.Retire())
		}
	}()
	profile, err := buildHostProfile(
		config.builtins,
		config.blobDigest,
		config.streamDigest,
		config.workspaceFileDigest,
		config.scriptRuntime,
		config.pluginFeatures,
		config.ai,
	)
	if err != nil {
		return nil, err
	}
	policy, err := NewBuiltinPolicy(config.ai)
	if err != nil {
		return nil, err
	}
	providers := maps.Clone(config.baseProviders)
	if err := mergeEnvironmentInstallations(providers, config.ai.Entries(), ai.ProviderABI, "AI",
		func(installed ai.Installation) (string, artifact.Digest, resource.Provider) {
			return installed.ProviderID, installed.ProviderArtifact, installed.Provider
		}); err != nil {
		return nil, err
	}
	targets := make([]targetruntime.Installation, 0, len(config.http.Entries())+len(applications.Entries())+len(automation.Entries()))
	for _, installed := range config.http.Entries() {
		targets = append(targets, targetruntime.Installation{Slot: installed.Slot, TargetID: installed.TargetID, Provider: installed.Provider, Configuration: targetruntime.Configuration{Kind: "http-target", Origin: installed.Profile.Machine().Origin}})
	}
	for _, installed := range applications.Entries() {
		profile := installed.Profile.Machine()
		targets = append(targets, targetruntime.Installation{Slot: installed.Slot, TargetID: installed.TargetID, Provider: installed.Provider, Configuration: targetruntime.Configuration{Kind: "configured-application", Executable: profile.Executable, Arguments: profile.Arguments}})
	}
	for _, installed := range automation.Entries() {
		targets = append(targets, targetruntime.Installation{Slot: installed.Slot, TargetID: installed.TargetID, Provider: installed.Provider, Configuration: targetruntime.Configuration{Kind: installed.Profile.TargetKind()}})
	}
	targetSnapshot, err := targetruntime.NewSnapshot(targets)
	if err != nil {
		return nil, err
	}
	return &sealedExecutionEnvironment{
		profile: profile, policy: policy, providers: providers, generation: generation, targets: targetSnapshot,
	}, nil
}

func mergeEnvironmentInstallations[T any](
	providers map[string]run.InstalledProvider,
	installations []T,
	abi, kind string,
	project func(T) (string, artifact.Digest, resource.Provider),
) error {
	for _, installed := range installations {
		id, digest, provider := project(installed)
		if id == "" || !digest.Valid() || abi == "" || provider == nil {
			return fmt.Errorf("invalid %s provider installation", kind)
		}
		if existing, ok := providers[id]; ok {
			if existing.ArtifactDigest != digest || existing.ABI != abi || existing.Provider != provider {
				return fmt.Errorf("conflicting %s provider installation", kind)
			}
			continue
		}
		providers[id] = run.InstalledProvider{ArtifactDigest: digest, ABI: abi, Provider: provider}
	}
	return nil
}

func (environment *sealedExecutionEnvironment) acquire() (targetruntime.Snapshot, func(), error) {
	if environment == nil {
		return targetruntime.Snapshot{}, nil, errors.New("execution environment is unavailable")
	}
	lease, err := environment.generation.Acquire()
	if err != nil {
		return targetruntime.Snapshot{}, nil, err
	}
	return environment.targets, lease.Release, nil
}
