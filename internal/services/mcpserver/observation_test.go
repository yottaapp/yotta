package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	appcore "github.com/yottaapp/yotta/internal/application"
	"github.com/yottaapp/yotta/internal/authoringcontext"
	"github.com/yottaapp/yotta/internal/automation/target"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowstore"
	"image"
	"image/png"
	"testing"
)

type observedParameters struct {
	*appcore.Application
	local *workflowstore.ParameterStore
}

func (a observedParameters) GetParameters(id string) (appcore.ParameterConfiguration, error) {
	result, err := a.Application.GetParameters(id)
	if err != nil {
		return result, err
	}
	result.Values, err = a.local.Load(id)
	return result, err
}

type observedTargets struct{ slot string }

func (t *observedTargets) ResolveTarget(_ context.Context, slot string) (target.Target, error) {
	t.slot = slot
	return target.Target{Kind: "desktop-window", DisplayName: "Machine"}, nil
}
func (t *observedTargets) CapturePNG(_ context.Context, slot string) ([]byte, error) {
	t.slot = slot
	var raw bytes.Buffer
	err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 100, 80)))
	return raw.Bytes(), err
}

func TestMCPActiveWorkflowAndImageContent(t *testing.T) {
	application := testApplication(t)
	created, err := application.CreateSource(context.Background(), "Observed workflow")
	if err != nil {
		t.Fatal(err)
	}
	local, err := workflowstore.OpenParameterStore("")
	if err != nil {
		t.Fatal(err)
	}
	observation := &authoringcontext.Service{Application: observedParameters{Application: application, local: local}, Screen: func(context.Context) (image.Image, image.Point, error) {
		return image.NewRGBA(image.Rect(0, 0, 640, 480)), image.Pt(-640, 0), nil
	}}
	observation.SetEditor(authoringcontext.Editor{WorkflowID: created.WorkflowID(), GraphID: "main", Dirty: true})
	protocol, err := BuildProtocol(application, observation)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewInProcessClient(protocol)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	initialize := mcp.InitializeRequest{}
	initialize.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initialize.Params.ClientInfo = mcp.Implementation{Name: "observation-test", Version: "1"}
	if _, err = c.Initialize(context.Background(), initialize); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, c, "authoring_context", map[string]any{})
	var inspected authoringcontext.Context
	decodeStructured(t, result, &inspected)
	if !inspected.Editor.Dirty || inspected.WorkflowID != created.WorkflowID() || len(inspected.Source) == 0 {
		t.Fatalf("context=%+v", inspected)
	}
	imageResult := callTool(t, c, "automation_capture", map[string]any{"screen": true})
	if len(imageResult.Content) != 2 {
		t.Fatalf("content=%+v", imageResult.Content)
	}
	img, ok := imageResult.Content[1].(mcp.ImageContent)
	if !ok || img.MIMEType != "image/jpeg" || img.Data == "" {
		t.Fatalf("image=%+v", imageResult.Content[1])
	}
	request := mcp.CallToolRequest{}
	request.Params.Name = "automation_capture"
	request.Params.Arguments = map[string]any{}
	failed, err := c.CallTool(context.Background(), request)
	if err != nil || !failed.IsError {
		t.Fatal("dirty default capture was not rejected", err)
	}
	callTool(t, c, "workflow_apply_patch", map[string]any{"workflowId": created.WorkflowID(), "baseRevision": 0, "commands": []any{map[string]any{"kind": "set-target-default", "setTargetDefault": map[string]any{"target": "target", "slot": schema.DefaultWorkflowTargetID}}}})
	if err := local.Save(created.WorkflowID(), map[string]json.RawMessage{schema.TargetParameterPrefix + schema.DefaultWorkflowTargetID: json.RawMessage(`"game"`)}); err != nil {
		t.Fatal(err)
	}
	observation.SetEditor(authoringcontext.Editor{WorkflowID: created.WorkflowID(), GraphID: "main"})
	targets := &observedTargets{}
	observation.Targets = targets
	captured := callTool(t, c, "automation_capture", map[string]any{})
	var metadata authoringcontext.CaptureInfo
	decodeStructured(t, captured, &metadata)
	if metadata.Slot != schema.DefaultWorkflowTargetID {
		t.Fatalf("physical slot leaked: %+v", metadata)
	}
	observation.SetEditor(authoringcontext.Editor{})
	described := callTool(t, c, "automation_target", map[string]any{"workflowId": created.WorkflowID(), "slot": schema.DefaultWorkflowTargetID})
	var targetInfo authoringcontext.ResolvedTarget
	decodeStructured(t, described, &targetInfo)
	if targetInfo.Slot != schema.DefaultWorkflowTargetID || observation.Editor().WorkflowID != "" {
		t.Fatalf("background describe=%+v", targetInfo)
	}
	if targets.slot != "game" {
		t.Fatalf("wrong default slot: %s", targets.slot)
	}
}
