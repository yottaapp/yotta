package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/yottaapp/yotta/internal/admission"
	run "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/runprepare"
	"github.com/yottaapp/yotta/internal/targetruntime"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

type leasedAdmission struct {
	record    run.Record
	providers map[string]run.InstalledProvider
	targets   targetruntime.Snapshot
	release   func()
}

func (a *Application) replaceAdmissionEnvironment(
	profile admission.HostProfile,
	policy admission.Policy,
	providers map[string]run.InstalledProvider,
	targets func() (targetruntime.Snapshot, func(), error),
) error {
	if !profile.Valid() || policy == nil {
		return errors.New("replacement execution environment is invalid")
	}
	next, err := cloneProviderSnapshot(providers)
	if err != nil {
		return err
	}
	a.admissionMu.Lock()
	defer a.admissionMu.Unlock()
	if err := a.admitter.ReplaceEnvironment(profile, policy); err != nil {
		return err
	}
	a.providers = next
	a.targetSnapshot = targets
	return nil
}

func (a *Application) admitRun(
	ctx context.Context,
	program compiler.ProgramSnapshot,
	principal string,
	selection admission.Selection,
	leased leasedAdmission,
) (_ leasedAdmission, resultErr error) {
	if !program.Valid() {
		return leasedAdmission{}, errors.New("run admission requires a valid Program")
	}
	if err := a.programs.Put(ctx, program); err != nil {
		return leasedAdmission{}, fmt.Errorf("persist Program before admission: %w", err)
	}

	admitted, err := a.admitter.Admit(ctx, admission.Request{
		Program: program, Principal: principal, Selection: selection,
	})
	if err != nil {
		return leasedAdmission{record: admitted.Record}, err
	}
	leased.record = admitted.Record
	return leased, nil
}

func (a *Application) leaseRunTargets() (leasedAdmission, error) {
	a.admissionMu.RLock()
	defer a.admissionMu.RUnlock()
	targets, err := targetruntime.NewSnapshot(nil)
	if err != nil {
		return leasedAdmission{}, fmt.Errorf("create empty configured target snapshot: %w", err)
	}
	release := func() {}
	if a.targetSnapshot != nil {
		targets, release, err = a.targetSnapshot()
		if err != nil {
			return leasedAdmission{}, fmt.Errorf("snapshot configured targets: %w", err)
		}
		if !targets.Valid() || release == nil {
			return leasedAdmission{}, errors.New("execution environment returned an invalid configured target snapshot")
		}
	}
	return leasedAdmission{providers: a.providers, targets: targets, release: release}, nil
}

func (a *Application) prepareRunImages(ctx context.Context, sourceJSON []byte, targets targetruntime.Snapshot, program compiler.ProgramSnapshot) (runprepare.PreparedImages, error) {
	if a.runImagePlanner == nil {
		return runprepare.PreparedImages{}, nil
	}
	// Retain Source structure for the planner, but remove image usage from
	// disconnected nodes using the compiler's source locations (including calls).
	source, diagnostics := schema.ParseSource(sourceJSON)
	if schema.HasErrors(diagnostics) {
		return runprepare.PreparedImages{}, errors.New("invalid workflow source for image preparation")
	}
	active := activeSourceNodes(program)
	for gi := range source.Graphs {
		for ni := range source.Graphs[gi].Nodes {
			node := &source.Graphs[gi].Nodes[ni]
			if !active[source.Graphs[gi].ID][node.ID] {
				node.Bindings = map[string]schema.InputBinding{}
			}
		}
	}
	sourceJSON, err := json.Marshal(source)
	if err != nil {
		return runprepare.PreparedImages{}, err
	}
	slots, err := runprepare.ReferencedTargetSlots(sourceJSON)
	if err != nil {
		return runprepare.PreparedImages{}, err
	}
	resolutions := make(map[string][2]int, len(slots))
	for _, slot := range slots {
		description, err := targets.Describe(ctx, slot)
		if err != nil {
			return runprepare.PreparedImages{}, fmt.Errorf("describe configured target %q for Run images: %w", slot, err)
		}
		resolutions[slot] = [2]int{description.Width, description.Height}
	}
	return a.runImagePlanner.Prepare(ctx, sourceJSON, resolutions)
}

func cloneProviderSnapshot(source map[string]run.InstalledProvider) (map[string]run.InstalledProvider, error) {
	if source == nil {
		return nil, errors.New("execution environment providers are unavailable")
	}
	result := make(map[string]run.InstalledProvider, len(source))
	for id, provider := range source {
		if id == "" || !provider.ArtifactDigest.Valid() || provider.ABI == "" || provider.Provider == nil {
			return nil, errors.New("execution environment contains an invalid provider")
		}
		result[id] = provider
	}
	return result, nil
}
