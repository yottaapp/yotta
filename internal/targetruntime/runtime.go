// Package targetruntime runs configured network, application, and automation
// targets directly. A Snapshot is an immutable view of the current device
// configuration; a Run owns the adapter objects opened while one workflow is
// executing.
package targetruntime

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/yottaapp/yotta/internal/resource"
)

type Installation struct {
	Slot          string
	TargetID      string
	Provider      resource.Provider
	Configuration Configuration
}

// Configuration is immutable preparation metadata, never consulted per operation.
type Configuration struct {
	Kind       string
	Origin     string
	Executable string
	Arguments  []string
}

func (snapshot Snapshot) Configuration(slot string) Configuration {
	if !snapshot.Valid() {
		return Configuration{}
	}
	c := snapshot.state.bySlot[slot].Configuration
	c.Arguments = append([]string(nil), c.Arguments...)
	return c
}

type Description struct {
	Width  int
	Height int
}

type Describer interface {
	DescribeTarget(context.Context, string) (Description, error)
}

var ErrDescriptionUnavailable = errors.New("configured target description is unavailable")

type snapshotState struct {
	bySlot map[string]Installation
}

type Snapshot struct {
	state *snapshotState
}

func NewSnapshot(installations []Installation) (Snapshot, error) {
	bySlot := make(map[string]Installation, len(installations))
	for _, installation := range installations {
		if installation.Slot == "" || installation.TargetID == "" || installation.Provider == nil {
			return Snapshot{}, errors.New("configured target installation is incomplete")
		}
		if _, exists := bySlot[installation.Slot]; exists {
			return Snapshot{}, fmt.Errorf("configured target slot %q is duplicated", installation.Slot)
		}
		installation.Configuration.Arguments = append([]string(nil), installation.Configuration.Arguments...)
		bySlot[installation.Slot] = installation
	}
	return Snapshot{state: &snapshotState{bySlot: bySlot}}, nil
}

func (snapshot Snapshot) Valid() bool {
	return snapshot.state != nil
}

func (snapshot Snapshot) Slots() []string {
	if !snapshot.Valid() {
		return nil
	}
	slots := make([]string, 0, len(snapshot.state.bySlot))
	for slot := range snapshot.state.bySlot {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	return slots
}

func (snapshot Snapshot) Describe(ctx context.Context, slot string) (Description, error) {
	if ctx == nil {
		return Description{}, errors.New("configured target description context is required")
	}
	if !snapshot.Valid() {
		return Description{}, errors.New("configured target snapshot is unavailable")
	}
	installation, ok := snapshot.state.bySlot[slot]
	if !ok {
		return Description{}, fmt.Errorf("configured target slot %q does not exist", slot)
	}
	describer, ok := installation.Provider.(Describer)
	if !ok {
		return Description{}, ErrDescriptionUnavailable
	}
	description, err := describer.DescribeTarget(ctx, installation.TargetID)
	if err != nil {
		return Description{}, err
	}
	if description.Width <= 0 || description.Height <= 0 {
		return Description{}, errors.New("configured target description has invalid dimensions")
	}
	return description, nil
}

func (snapshot Snapshot) NewRun() (*Run, error) {
	if !snapshot.Valid() {
		return nil, errors.New("configured target snapshot is unavailable")
	}
	return &Run{
		installations: snapshot.state.bySlot,
		random:        rand.Reader,
		opened:        make(map[string]openedTarget),
	}, nil
}

type OpenRequest struct {
	Slot       string
	Kind       string
	Operations []string
	Config     []byte
}

type openedTarget struct {
	provider resource.Provider
	object   any
	handle   resource.Handle
}

type Run struct {
	mu            sync.Mutex
	installations map[string]Installation
	random        io.Reader
	opened        map[string]openedTarget
	closed        bool
}

func (runtime *Run) Open(ctx context.Context, request OpenRequest) (resource.Handle, error) {
	if ctx == nil {
		return resource.Handle{}, errors.New("configured target context is required")
	}
	if request.Slot == "" || request.Kind == "" || len(request.Operations) == 0 {
		return resource.Handle{}, errors.New("configured target request is incomplete")
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return resource.Handle{}, errors.New("configured target Run is closed")
	}
	installation, exists := runtime.installations[request.Slot]
	runtime.mu.Unlock()
	if !exists {
		return resource.Handle{}, fmt.Errorf("configured target slot %q does not exist", request.Slot)
	}
	object, err := installation.Provider.Open(ctx, resource.ProviderOpenRequest{
		TargetID: installation.TargetID,
		Kind:     request.Kind, Operations: append([]string(nil), request.Operations...),
		Config: append([]byte(nil), request.Config...),
	})
	if err != nil {
		return resource.Handle{}, err
	}
	token := make([]byte, 32)
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return resource.Handle{}, errors.Join(
			errors.New("configured target Run is closed"),
			installation.Provider.Close(context.WithoutCancel(ctx), object),
		)
	}
	for {
		if _, err := io.ReadFull(runtime.random, token); err != nil {
			return resource.Handle{}, errors.Join(err, installation.Provider.Close(context.WithoutCancel(ctx), object))
		}
		encoded := base64.RawURLEncoding.EncodeToString(token)
		if _, collision := runtime.opened[encoded]; collision {
			continue
		}
		handle := resource.Handle{Token: encoded, Kind: request.Kind}
		runtime.opened[encoded] = openedTarget{provider: installation.Provider, object: object, handle: handle}
		return handle, nil
	}
}

func (runtime *Run) Invoke(ctx context.Context, handle resource.Handle, operation string, payload []byte) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("configured target context is required")
	}
	runtime.mu.Lock()
	opened, exists := runtime.opened[handle.Token]
	closed := runtime.closed
	runtime.mu.Unlock()
	if closed {
		return nil, errors.New("configured target Run is closed")
	}
	if !exists || opened.handle != handle {
		return nil, errors.New("configured target handle does not exist")
	}
	return opened.provider.Invoke(ctx, opened.object, operation, append([]byte(nil), payload...))
}

func (runtime *Run) Drop(ctx context.Context, handle resource.Handle) error {
	if ctx == nil {
		return errors.New("configured target context is required")
	}
	runtime.mu.Lock()
	opened, exists := runtime.opened[handle.Token]
	if exists && opened.handle == handle {
		delete(runtime.opened, handle.Token)
	} else {
		exists = false
	}
	runtime.mu.Unlock()
	if !exists {
		return nil
	}
	return opened.provider.Close(context.WithoutCancel(ctx), opened.object)
}

func (runtime *Run) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("configured target close context is required")
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return nil
	}
	runtime.closed = true
	opened := make([]openedTarget, 0, len(runtime.opened))
	for _, target := range runtime.opened {
		opened = append(opened, target)
	}
	runtime.opened = make(map[string]openedTarget)
	runtime.mu.Unlock()
	var closeErr error
	for _, target := range opened {
		closeErr = errors.Join(closeErr, target.provider.Close(context.WithoutCancel(ctx), target.object))
	}
	return closeErr
}
