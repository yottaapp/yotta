package authoring

import (
	"encoding/json"
	"errors"

	"github.com/invopop/jsonschema"
	"github.com/yottaapp/yotta/internal/datatype"
	workflowschema "github.com/yottaapp/yotta/internal/workflow/schema"
)

const patchSchemaID = "https://yottaapp.dev/contracts/workflow/" + workflowschema.SchemaPathVersion + "/authoring-patch.schema.json"

// GenerateSchema emits the tracked Workflow authoring command contract. The
// reflected Go shape is tightened into an exact oneOf tagged union so clients
// cannot combine payloads or rely on decoder-specific zero values.
func GenerateSchema() ([]byte, error) {
	reflector := &jsonschema.Reflector{Anonymous: true, ExpandedStruct: true}
	reflected := reflector.Reflect(&PatchRequest{})
	raw, err := json.Marshal(reflected)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	document["$id"] = patchSchemaID
	document["title"] = "Yotta Workflow Authoring Patch"
	definitions, ok := document["$defs"].(map[string]any)
	if !ok {
		return nil, errors.New("authoring patch schema omitted definitions")
	}
	datatype.TuneTypeExpressionDefinitions(definitions)
	definitions["JSONValue"] = map[string]any{"oneOf": []any{
		map[string]any{"type": "null"},
		map[string]any{"type": "boolean"},
		map[string]any{"type": "number"},
		map[string]any{"type": "string"},
		map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/JSONValue"}},
		map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/JSONValue"}},
	}}
	for definitionName, propertyName := range map[string]string{
		"AddStateVariableCommand": "default",
		"SetConfigCommand":        "value",
		"BindValueCommand":        "value",
	} {
		definition, ok := definitions[definitionName].(map[string]any)
		if !ok {
			return nil, errors.New("authoring patch schema omitted JSON value owner")
		}
		fields, ok := definition["properties"].(map[string]any)
		if !ok {
			return nil, errors.New("authoring patch JSON value owner omitted properties")
		}
		fields[propertyName] = map[string]any{"$ref": "#/$defs/JSONValue"}
	}
	command, ok := definitions["Command"].(map[string]any)
	if !ok {
		return nil, errors.New("authoring patch schema omitted Command definition")
	}
	properties, ok := command["properties"].(map[string]any)
	if !ok {
		return nil, errors.New("authoring patch Command omitted properties")
	}
	variants := []struct {
		kind    CommandKind
		payload string
	}{
		{CommandRenameWorkflow, "renameWorkflow"},
		{CommandUpdateWorkflowMetadata, "updateWorkflowMetadata"},
		{CommandSetTargetDefault, "setTargetDefault"},
		{CommandClearTargetDefault, "clearTargetDefault"},
		{CommandAddStateVariable, "addStateVariable"},
		{CommandUpdateStateVariable, "updateStateVariable"},
		{CommandSetParameterBlocks, "setParameterBlocks"},
		{CommandSetWorkflowTargets, "setWorkflowTargets"},
		{CommandRemoveStateVariable, "removeStateVariable"},
		{CommandAddNode, "addNode"},
		{CommandUpgradeNodeContract, "upgradeNodeContract"},
		{CommandRemoveNode, "removeNode"},
		{CommandMoveNode, "moveNode"},
		{CommandSetNodeLabel, "setNodeLabel"},
		{CommandSetNodeDisabled, "setNodeDisabled"},
		{CommandSetConfig, "setConfig"},
		{CommandClearConfig, "clearConfig"},
		{CommandBindValue, "bindValue"},
		{CommandBindDefault, "bindDefault"},
		{CommandBindBlob, "bindBlob"},
		{CommandBindResource, "bindResource"},
		{CommandAddResource, "addResource"},
		{CommandReplaceResource, "replaceResource"},
		{CommandUpdateResourceMetadata, "updateResourceMetadata"},
		{CommandRemoveResource, "removeResource"},
		{CommandClearBinding, "clearBinding"},
		{CommandConnect, "connect"},
		{CommandDisconnect, "disconnect"},
		{CommandAddGraph, "addGraph"},
		{CommandRenameGraph, "renameGraph"},
		{CommandRemoveGraph, "removeGraph"},
		{CommandUpdateGraphInterface, "updateGraphInterface"},
		{CommandAddGraphCall, "addGraphCall"},
		{CommandUpdateGraphCall, "updateGraphCall"},
		{CommandRemoveGraphCall, "removeGraphCall"},
		{CommandAddAnnotation, "addAnnotation"},
		{CommandUpdateAnnotation, "updateAnnotation"},
		{CommandRemoveAnnotation, "removeAnnotation"},
		{CommandSetEdgeReroutes, "setEdgeReroutes"},
		{CommandCollapseSelection, "collapseSelection"},
	}
	oneOf := make([]any, 0, len(variants))
	for _, variant := range variants {
		payload, exists := properties[variant.payload]
		if !exists {
			return nil, errors.New("authoring patch Command omitted variant payload")
		}
		oneOf = append(oneOf, map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"kind":          map[string]any{"const": string(variant.kind)},
				variant.payload: payload,
			},
			"required": []string{"kind", variant.payload},
		})
	}
	definitions["Command"] = map[string]any{"oneOf": oneOf}
	formatted, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(formatted, '\n'), nil
}
