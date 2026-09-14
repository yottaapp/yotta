package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/authoringcontext"
)

func (s *registrar) registerObservation(protocol *server.MCPServer) {
	protocol.AddTool(mcp.NewTool("authoring_context", mcp.WithDescription("Read the active editor, saved Workflow Source (including defaults), unsaved-state indicator and configured automation targets. Omit workflowId to use the active editor."), mcp.WithString("workflowId")),
		func(ctx context.Context, call mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var request struct {
				WorkflowID string `json:"workflowId"`
			}
			if call.BindArguments(&request) != nil {
				return toolProblemResult(apperr.From(apperr.New("mcp.invalid_arguments", nil))), nil
			}
			result, err := s.observation.Inspect(request.WorkflowID)
			if err != nil {
				return toolProblemResult(apperr.From(err)), nil
			}
			return mcp.NewToolResultStructuredOnly(result), nil
		})
	protocol.AddTool(mcp.NewTool("automation_capture", mcp.WithDescription("Capture the Workflow default automation target, a configured slot, or the entire virtual desktop when screen=true. Returns an actual image and pixel coordinate metadata. Does not execute the Workflow."), mcp.WithString("workflowId"), mcp.WithString("slot"), mcp.WithBoolean("screen")),
		func(ctx context.Context, call mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var request authoringcontext.CaptureRequest
			if call.BindArguments(&request) != nil {
				return toolProblemResult(apperr.From(apperr.New("mcp.invalid_arguments", nil))), nil
			}
			result, err := s.observation.Capture(ctx, request)
			if err != nil {
				return toolProblemResult(apperr.From(err)), nil
			}
			raw, _ := json.Marshal(result.Info)
			return &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: string(raw)}, mcp.ImageContent{Type: "image", Data: base64.StdEncoding.EncodeToString(result.Data), MIMEType: result.MediaType}}, StructuredContent: result.Info}, nil
		})
	protocol.AddTool(mcp.NewTool("automation_target", mcp.WithDescription("Resolve a configured automation target to its current display name and pixel dimensions without changing focus."), mcp.WithString("slot", mcp.Required()), mcp.WithString("workflowId")),
		func(ctx context.Context, call mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result, err := s.observation.DescribeWorkflow(ctx, call.GetString("workflowId", ""), call.GetString("slot", ""))
			if err != nil {
				return toolProblemResult(apperr.From(err)), nil
			}
			return mcp.NewToolResultStructuredOnly(result), nil
		})
}
