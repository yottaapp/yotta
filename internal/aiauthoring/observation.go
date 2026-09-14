package aiauthoring

import (
	"context"
	"encoding/json"
	"github.com/yottaapp/yotta/internal/ai"
	"github.com/yottaapp/yotta/internal/authoringcontext"
)

func (s *proposalState) authoringContext(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	result, err := s.manager.observation.Inspect(s.workflowID)
	if err != nil {
		return nil, err
	}
	if result.Editor.WorkflowID != s.workflowID {
		result.Editor = authoringcontext.Editor{WorkflowID: s.workflowID}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"contextJson": string(raw)})
}
func (s *proposalState) automationTarget(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Slot string `json:"slot"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	result, err := s.manager.observation.DescribeWorkflow(ctx, s.workflowID, request.Slot)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"targetJson": string(encoded)})
}
func (s *proposalState) automationCapture(ctx context.Context, raw json.RawMessage) (json.RawMessage, *ai.ImageInput, error) {
	var request authoringcontext.CaptureRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, nil, err
	}
	request.WorkflowID = s.workflowID
	result, err := s.manager.observation.Capture(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	metadata, err := json.Marshal(result.Info)
	if err != nil {
		return nil, nil, err
	}
	value, err := json.Marshal(map[string]string{"captureJson": string(metadata)})
	s.addToolTrace("automation_capture", raw, metadata)
	return value, &ai.ImageInput{MediaType: result.MediaType, Data: result.Data}, err
}
