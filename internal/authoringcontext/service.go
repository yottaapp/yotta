// Package authoringcontext shares desktop observation between MCP and AI proposals.
package authoringcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	_ "image/png"
	"sync"
	"time"

	"github.com/yottaapp/yotta/internal/apperr"
	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/automation/target"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
	"golang.org/x/image/draw"
)

type Editor struct {
	WorkflowID string `json:"workflowId"`
	GraphID    string `json:"graphId"`
	Dirty      bool   `json:"dirty"`
}
type TargetInfo struct {
	Slot    string `json:"slot"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Adapter string `json:"adapter"`
}
type Targets interface {
	ResolveTarget(context.Context, string) (target.Target, error)
	CapturePNG(context.Context, string) ([]byte, error)
}
type WorkflowApplication interface {
	GetSource(string) (workflowstore.SourceSnapshot, error)
	GetParameters(string) (appcore.ParameterConfiguration, error)
}

type Service struct {
	Application WorkflowApplication
	Targets     Targets
	ListTargets func() []TargetInfo
	Screen      func(context.Context) (image.Image, image.Point, error)
	mu          sync.RWMutex
	editor      Editor
}

func (s *Service) SetEditor(editor Editor) { s.mu.Lock(); defer s.mu.Unlock(); s.editor = editor }
func (s *Service) Editor() Editor          { s.mu.RLock(); defer s.mu.RUnlock(); return s.editor }

type Context struct {
	Editor     Editor          `json:"editor"`
	WorkflowID string          `json:"workflowId"`
	Revision   int64           `json:"revision"`
	Source     json.RawMessage `json:"source"`
	Targets    []TargetInfo    `json:"targets"`
}

// observation freezes one workflow revision and its local bindings for a single
// observation. Machine slots never enter the authoring-facing target list.
type observation struct {
	context  Context
	source   schema.WorkflowSource
	bindings map[string]string
}

func (s *Service) loadObservation(workflowID string) (observation, error) {
	if s == nil {
		return observation{}, problem("unavailable")
	}
	result := observation{context: Context{Editor: s.Editor(), Targets: []TargetInfo{}}, bindings: map[string]string{}}
	var installed []TargetInfo
	if s.ListTargets != nil {
		installed = s.ListTargets()
	}
	if workflowID == "" {
		workflowID = result.context.Editor.WorkflowID
	}
	if workflowID == "" {
		result.context.Targets = append(result.context.Targets, installed...)
		return result, nil
	}
	if s.Application == nil {
		return observation{}, problem("unavailable")
	}
	snapshot, err := s.Application.GetSource(workflowID)
	if err != nil {
		return observation{}, problem("workflow_not_found")
	}
	source, diagnostics := schema.ParseSource(snapshot.Artifact())
	if schema.HasErrors(diagnostics) {
		return observation{}, problem("workflow_not_found")
	}
	result.context.WorkflowID, result.context.Revision, result.context.Source = workflowID, snapshot.Revision(), snapshot.Artifact()
	result.source = source
	if len(source.Targets) == 0 {
		result.context.Targets = append(result.context.Targets, installed...)
		return result, nil
	}
	parameters, err := s.Application.GetParameters(workflowID)
	if err != nil {
		return observation{}, problem("unavailable")
	}
	if parameters.Revision != snapshot.Revision() {
		return observation{}, problem("save_first")
	}
	for _, role := range source.Targets {
		var slot string
		if json.Unmarshal(parameters.Values[schema.TargetParameterPrefix+role.ID], &slot) == nil {
			result.bindings[role.ID] = slot
		}
		info := TargetInfo{Slot: role.ID, Label: role.Name, Kind: role.Kind}
		if slot != "" {
			for _, machine := range installed {
				if machine.Slot == slot {
					info.Kind, info.Adapter = machine.Kind, machine.Adapter
					break
				}
			}
		}
		result.context.Targets = append(result.context.Targets, info)
	}
	return result, nil
}
func (s *Service) Inspect(workflowID string) (Context, error) {
	observation, err := s.loadObservation(workflowID)
	return observation.context, err
}

func (s *Service) observationSlot(workflowID, slot string) (roleID, machineSlot, name string, err error) {
	observation, err := s.loadObservation(workflowID)
	if err != nil {
		return "", "", "", err
	}
	isDefault := slot == ""
	if isDefault {
		if observation.context.Editor.WorkflowID == observation.context.WorkflowID && observation.context.Editor.Dirty {
			return "", "", "", problem("save_first")
		}
		slot, _ = schema.TargetDefaultSlot(observation.source, "target")
		if slot == "" {
			return "", "", "", problem("default_target_missing")
		}
	}
	if len(observation.source.Targets) == 0 {
		return slot, slot, "", nil
	}
	for _, role := range observation.source.Targets {
		if role.ID != slot {
			continue
		}
		machine := observation.bindings[role.ID]
		if machine == "" {
			code := "target_unavailable"
			if isDefault {
				code = "default_target_missing"
			}
			return "", "", "", problem(code)
		}
		return role.ID, machine, role.Name, nil
	}
	return "", "", "", problem("target_unavailable")
}

type CaptureRequest struct {
	WorkflowID string `json:"workflowId,omitempty"`
	Slot       string `json:"slot,omitempty"`
	Screen     bool   `json:"screen,omitempty"`
}
type CaptureInfo struct {
	Slot            string `json:"slot"`
	CoordinateSpace string `json:"coordinateSpace"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	SourceWidth     int    `json:"sourceWidth"`
	SourceHeight    int    `json:"sourceHeight"`
	OriginX         int    `json:"originX"`
	OriginY         int    `json:"originY"`
	CapturedAt      string `json:"capturedAt"`
}
type Capture struct {
	Info      CaptureInfo
	Data      []byte
	MediaType string
}
type ResolvedTarget struct {
	Slot   string `json:"slot"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (s *Service) Describe(ctx context.Context, slot string) (ResolvedTarget, error) {
	return s.DescribeWorkflow(ctx, "", slot)
}
func (s *Service) DescribeWorkflow(ctx context.Context, workflowID, slot string) (ResolvedTarget, error) {
	if s == nil || s.Targets == nil {
		return ResolvedTarget{}, problem("unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	role, machine, name, err := s.observationSlot(workflowID, slot)
	if err != nil {
		return ResolvedTarget{}, err
	}
	resolved, err := s.Targets.ResolveTarget(ctx, machine)
	if err != nil {
		return ResolvedTarget{}, problem("target_unavailable")
	}
	if name == "" {
		name = resolved.DisplayName
	}
	return ResolvedTarget{Slot: role, Name: name, Kind: resolved.Kind, Width: resolved.Resolution.W, Height: resolved.Resolution.H}, nil
}
func (s *Service) Capture(ctx context.Context, request CaptureRequest) (Capture, error) {
	if s == nil {
		return Capture{}, problem("unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var frame image.Image
	var origin image.Point
	var err error
	space := "target"
	if request.Screen {
		if request.Slot != "" {
			return Capture{}, problem("invalid_capture")
		}
		if s.Screen == nil {
			return Capture{}, problem("screen_unavailable")
		}
		frame, origin, err = s.Screen(ctx)
		space = "screen"
	} else {
		role, machine, _, resolveErr := s.observationSlot(request.WorkflowID, request.Slot)
		if resolveErr != nil {
			return Capture{}, resolveErr
		}
		request.Slot = role

		if s.Targets == nil {
			return Capture{}, problem("unavailable")
		}
		var raw []byte
		raw, err = s.Targets.CapturePNG(ctx, machine)
		if err == nil {
			config, _, decodeErr := image.DecodeConfig(bytes.NewReader(raw))
			if decodeErr != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 64_000_000 {
				return Capture{}, problem("invalid_image")
			}
			frame, _, err = image.Decode(bytes.NewReader(raw))
		}
	}
	if err != nil {
		return Capture{}, problem("capture_failed")
	}
	if ctx.Err() != nil {
		return Capture{}, problem("capture_failed")
	}
	if frame == nil {
		return Capture{}, problem("invalid_image")
	}
	bounds := frame.Bounds()
	info := CaptureInfo{Slot: request.Slot, CoordinateSpace: space, SourceWidth: bounds.Dx(), SourceHeight: bounds.Dy(), OriginX: origin.X, OriginY: origin.Y, CapturedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	// Keep images readable while staying within every provider's image budget.
	for limit := 1920; limit >= 480; limit /= 2 {
		w, h := bounds.Dx(), bounds.Dy()
		if max(w, h) > limit {
			scale := float64(limit) / float64(max(w, h))
			w = max(1, int(float64(w)*scale))
			h = max(1, int(float64(h)*scale))
		}
		resized := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(resized, resized.Bounds(), frame, bounds, draw.Src, nil)
		var encoded bytes.Buffer
		if jpeg.Encode(&encoded, resized, &jpeg.Options{Quality: 85}) != nil {
			return Capture{}, problem("invalid_image")
		}
		if encoded.Len() <= 1536<<10 {
			info.Width, info.Height = w, h
			return Capture{Info: info, Data: encoded.Bytes(), MediaType: "image/jpeg"}, nil
		}
	}
	return Capture{}, problem("invalid_image")
}
func problem(code string) error { return apperr.New("authoring.observation."+code, nil) }
