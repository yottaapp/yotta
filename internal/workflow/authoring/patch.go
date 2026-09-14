// Package authoring applies the only mutable Workflow authoring protocol.
// Callers submit small domain commands against an exact source revision; they
// never replace a complete Workflow Source document.
package authoring

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/invopop/jsonschema"
	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

const (
	MaxCommands        = 256
	graphCallInputPort = "in"
)

var handlePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

type PatchRequest struct {
	WorkflowID   string    `json:"workflowId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	BaseRevision int64     `json:"baseRevision" jsonschema:"required,minimum=0,maximum=9007199254740991"`
	Commands     []Command `json:"commands" jsonschema:"required,minItems=1,maxItems=256"`
}

type CommandKind string

const (
	CommandRenameWorkflow         CommandKind = "rename-workflow"
	CommandUpdateWorkflowMetadata CommandKind = "update-workflow-metadata"
	CommandSetTargetDefault       CommandKind = "set-target-default"
	CommandClearTargetDefault     CommandKind = "clear-target-default"
	CommandAddStateVariable       CommandKind = "add-state-variable"
	CommandSetWorkflowTargets     CommandKind = "set-workflow-targets"
	CommandSetParameterBlocks     CommandKind = "set-parameter-blocks"
	CommandUpdateStateVariable    CommandKind = "update-state-variable"
	CommandRemoveStateVariable    CommandKind = "remove-state-variable"
	CommandAddNode                CommandKind = "add-node"
	CommandUpgradeNodeContract    CommandKind = "upgrade-node-contract"
	CommandRemoveNode             CommandKind = "remove-node"
	CommandMoveNode               CommandKind = "move-node"
	CommandSetNodeLabel           CommandKind = "set-node-label"
	CommandSetNodeDisabled        CommandKind = "set-node-disabled"
	CommandSetConfig              CommandKind = "set-config"
	CommandClearConfig            CommandKind = "clear-config"
	CommandBindValue              CommandKind = "bind-value"
	CommandBindDefault            CommandKind = "bind-default"
	CommandBindBlob               CommandKind = "bind-blob"
	CommandBindResource           CommandKind = "bind-resource"
	CommandAddResource            CommandKind = "add-resource"
	CommandReplaceResource        CommandKind = "replace-resource"
	CommandUpdateResourceMetadata CommandKind = "update-resource-metadata"
	CommandRemoveResource         CommandKind = "remove-resource"
	CommandClearBinding           CommandKind = "clear-binding"
	CommandConnect                CommandKind = "connect"
	CommandDisconnect             CommandKind = "disconnect"
	CommandAddGraph               CommandKind = "add-graph"
	CommandRenameGraph            CommandKind = "rename-graph"
	CommandRemoveGraph            CommandKind = "remove-graph"
	CommandUpdateGraphInterface   CommandKind = "update-graph-interface"
	CommandAddGraphCall           CommandKind = "add-graph-call"
	CommandUpdateGraphCall        CommandKind = "update-graph-call"
	CommandRemoveGraphCall        CommandKind = "remove-graph-call"
	CommandAddAnnotation          CommandKind = "add-annotation"
	CommandUpdateAnnotation       CommandKind = "update-annotation"
	CommandRemoveAnnotation       CommandKind = "remove-annotation"
	CommandSetEdgeReroutes        CommandKind = "set-edge-reroutes"
	CommandCollapseSelection      CommandKind = "collapse-selection"
)

// Command is an explicit tagged union. Exactly one payload must be present and
// it must match Kind; this is checked again inside Engine.Apply for every
// caller, including Wails and MCP decoders.
type Command struct {
	SetWorkflowTargets     *SetWorkflowTargetsCommand     `json:"setWorkflowTargets,omitempty"`
	Kind                   CommandKind                    `json:"kind"`
	RenameWorkflow         *RenameWorkflowCommand         `json:"renameWorkflow,omitempty"`
	UpdateWorkflowMetadata *UpdateWorkflowMetadataCommand `json:"updateWorkflowMetadata,omitempty"`
	SetTargetDefault       *SetTargetDefaultCommand       `json:"setTargetDefault,omitempty"`
	ClearTargetDefault     *ClearTargetDefaultCommand     `json:"clearTargetDefault,omitempty"`
	AddStateVariable       *AddStateVariableCommand       `json:"addStateVariable,omitempty"`
	SetParameterBlocks     *SetParameterBlocksCommand     `json:"setParameterBlocks,omitempty"`
	UpdateStateVariable    *UpdateStateVariableCommand    `json:"updateStateVariable,omitempty"`
	RemoveStateVariable    *RemoveStateVariableCommand    `json:"removeStateVariable,omitempty"`
	AddNode                *AddNodeCommand                `json:"addNode,omitempty"`
	UpgradeNodeContract    *NodeCommand                   `json:"upgradeNodeContract,omitempty"`
	RemoveNode             *NodeCommand                   `json:"removeNode,omitempty"`
	MoveNode               *MoveNodeCommand               `json:"moveNode,omitempty"`
	SetNodeLabel           *SetNodeLabelCommand           `json:"setNodeLabel,omitempty"`
	SetNodeDisabled        *SetNodeDisabledCommand        `json:"setNodeDisabled,omitempty"`
	SetConfig              *SetConfigCommand              `json:"setConfig,omitempty"`
	ClearConfig            *FieldCommand                  `json:"clearConfig,omitempty"`
	BindValue              *BindValueCommand              `json:"bindValue,omitempty"`
	BindDefault            *PortCommand                   `json:"bindDefault,omitempty"`
	BindBlob               *BindBlobCommand               `json:"bindBlob,omitempty"`
	BindResource           *BindResourceCommand           `json:"bindResource,omitempty"`
	AddResource            *AddResourceCommand            `json:"addResource,omitempty"`
	ReplaceResource        *ReplaceResourceCommand        `json:"replaceResource,omitempty"`
	UpdateResourceMetadata *UpdateResourceMetadataCommand `json:"updateResourceMetadata,omitempty"`
	RemoveResource         *RemoveResourceCommand         `json:"removeResource,omitempty"`
	ClearBinding           *PortCommand                   `json:"clearBinding,omitempty"`
	Connect                *EdgeCommand                   `json:"connect,omitempty"`
	Disconnect             *EdgeCommand                   `json:"disconnect,omitempty"`
	AddGraph               *AddGraphCommand               `json:"addGraph,omitempty"`
	RenameGraph            *RenameGraphCommand            `json:"renameGraph,omitempty"`
	RemoveGraph            *GraphCommand                  `json:"removeGraph,omitempty"`
	UpdateGraphInterface   *GraphInterfaceCommand         `json:"updateGraphInterface,omitempty"`
	AddGraphCall           *GraphCallCommand              `json:"addGraphCall,omitempty"`
	UpdateGraphCall        *GraphCallCommand              `json:"updateGraphCall,omitempty"`
	RemoveGraphCall        *CallCommand                   `json:"removeGraphCall,omitempty"`
	AddAnnotation          *AnnotationCommand             `json:"addAnnotation,omitempty"`
	UpdateAnnotation       *AnnotationCommand             `json:"updateAnnotation,omitempty"`
	RemoveAnnotation       *AnnotationIDCommand           `json:"removeAnnotation,omitempty"`
	SetEdgeReroutes        *SetEdgeReroutesCommand        `json:"setEdgeReroutes,omitempty"`
	CollapseSelection      *CollapseSelectionCommand      `json:"collapseSelection,omitempty"`
}

func (c Command) Validate() error { return validateTaggedCommand(c) }

type RenameWorkflowCommand struct {
	Name string `json:"name"`
}

type WorkflowMetadata struct {
	Name        string
	Description string
	Category    string
	Tags        []string
}

type UpdateWorkflowMetadataCommand struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
}

func NormalizeWorkflowMetadata(metadata WorkflowMetadata) (WorkflowMetadata, error) {
	metadata.Name = strings.TrimSpace(metadata.Name)
	metadata.Description = strings.TrimSpace(metadata.Description)
	metadata.Category = strings.TrimSpace(metadata.Category)
	if metadata.Name == "" || utf8.RuneCountInString(metadata.Name) > 256 {
		return WorkflowMetadata{}, errors.New("workflow name must contain 1 to 256 characters")
	}
	if utf8.RuneCountInString(metadata.Description) > 4096 {
		return WorkflowMetadata{}, errors.New("workflow description must contain at most 4096 characters")
	}
	if utf8.RuneCountInString(metadata.Category) > 128 {
		return WorkflowMetadata{}, errors.New("workflow category must contain at most 128 characters")
	}
	if len(metadata.Tags) > 64 {
		return WorkflowMetadata{}, errors.New("workflow tags must contain at most 64 items")
	}
	seen := make(map[string]struct{}, len(metadata.Tags))
	tags := make([]string, 0, len(metadata.Tags))
	for _, raw := range metadata.Tags {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > 128 {
			return WorkflowMetadata{}, errors.New("workflow tag must contain at most 128 characters")
		}
		key := strings.ToLower(tag)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		tags = append(tags, tag)
	}
	metadata.Tags = tags
	return metadata, nil
}

type SetTargetDefaultCommand struct {
	Target string `json:"target"`
	Slot   string `json:"slot"`
}

type ClearTargetDefaultCommand struct {
	Target string `json:"target"`
}

type AddStateVariableCommand struct {
	Parameter *schema.Parameter       `json:"parameter,omitempty"`
	Name      string                  `json:"name"`
	Type      datatype.TypeExpression `json:"type"`
	Default   any                     `json:"default"`
}

type SetWorkflowTargetsCommand struct {
	Targets []schema.WorkflowTarget `json:"targets" jsonschema:"required,minItems=1,maxItems=64"`
}

type SetParameterBlocksCommand struct {
	Blocks []schema.ParameterBlock `json:"blocks" jsonschema:"required,maxItems=4096"`
}

type UpdateStateVariableCommand struct {
	Parameter      *schema.Parameter       `json:"parameter,omitempty"`
	ClearParameter bool                    `json:"clearParameter,omitempty"`
	Name           string                  `json:"name"`
	Type           datatype.TypeExpression `json:"type"`
	Default        any                     `json:"default"`
}

type RemoveStateVariableCommand struct {
	Name string `json:"name"`
}

type AddNodeCommand struct {
	GraphID    string          `json:"graphId"`
	NodeTypeID string          `json:"nodeTypeId"`
	Handle     string          `json:"handle,omitempty"`
	Position   schema.Position `json:"position"`
}

type NodeCommand struct {
	GraphID string `json:"graphId"`
	NodeID  string `json:"nodeId"`
}

type MoveNodeCommand struct {
	GraphID  string          `json:"graphId"`
	NodeID   string          `json:"nodeId"`
	Position schema.Position `json:"position"`
}

type SetNodeLabelCommand struct {
	GraphID string `json:"graphId"`
	NodeID  string `json:"nodeId"`
	Label   string `json:"label"`
}

type SetNodeDisabledCommand struct {
	GraphID  string `json:"graphId"`
	NodeID   string `json:"nodeId"`
	Disabled bool   `json:"disabled"`
}

type SetConfigCommand struct {
	GraphID string `json:"graphId"`
	NodeID  string `json:"nodeId"`
	FieldID string `json:"fieldId"`
	Value   any    `json:"value"`
}

type FieldCommand struct {
	GraphID string `json:"graphId"`
	NodeID  string `json:"nodeId"`
	FieldID string `json:"fieldId"`
}

type BindValueCommand struct {
	GraphID string `json:"graphId"`
	NodeID  string `json:"nodeId"`
	PortID  string `json:"portId"`
	Value   any    `json:"value"`
}

type PortCommand struct {
	GraphID string `json:"graphId"`
	NodeID  string `json:"nodeId"`
	PortID  string `json:"portId"`
}

type BindBlobCommand struct {
	GraphID string       `json:"graphId"`
	NodeID  string       `json:"nodeId"`
	PortID  string       `json:"portId"`
	Blob    blob.BlobRef `json:"blob"`
}

type BindResourceCommand struct {
	GraphID  string                 `json:"graphId"`
	NodeID   string                 `json:"nodeId"`
	PortID   string                 `json:"portId"`
	Resource schema.ResourceBinding `json:"resource"`
}

type AddResourceCommand struct {
	Resource schema.WorkflowResource `json:"resource"`
}

type ReplaceResourceCommand struct {
	ResourceID string                  `json:"resourceId"`
	Resource   schema.WorkflowResource `json:"resource"`
}

type UpdateResourceMetadataCommand struct {
	ResourceID  string   `json:"resourceId"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Tags        []string `json:"tags"`
}

type RemoveResourceCommand struct {
	ResourceID string `json:"resourceId"`
}

// PatchNodeReference and the patch edge types are intentionally separate from
// schema.Endpoint. Authoring commands may refer to a node created earlier in
// the same atomic patch by its "$handle"; persisted Workflow Source endpoints
// must continue to use formal node IDs only.
type PatchNodeReference string

func (PatchNodeReference) JSONSchema() *jsonschema.Schema {
	formalMax, handleMax := uint64(128), uint64(65)
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		{Type: "string", MaxLength: &formalMax, Pattern: `^[A-Za-z0-9_][A-Za-z0-9._-]*$`},
		{Type: "string", MaxLength: &handleMax, Pattern: `^\$[A-Za-z][A-Za-z0-9_-]{0,63}$`},
	}}
}

type PatchEndpoint struct {
	NodeID PatchNodeReference `json:"nodeId" jsonschema:"required"`
	PortID string             `json:"portId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
}

type PatchEdge struct {
	Channel      schema.EdgeChannel      `json:"channel" jsonschema:"required,enum=data,enum=exec,enum=error"`
	From         PatchEndpoint           `json:"from" jsonschema:"required"`
	To           PatchEndpoint           `json:"to" jsonschema:"required"`
	Presentation schema.EdgePresentation `json:"presentation,omitempty"`
}

func PatchEdgeFromSource(edge schema.Edge) PatchEdge {
	return PatchEdge{
		Channel: edge.Channel,
		From: PatchEndpoint{
			NodeID: PatchNodeReference(edge.From.NodeID),
			PortID: edge.From.PortID,
		},
		To: PatchEndpoint{
			NodeID: PatchNodeReference(edge.To.NodeID),
			PortID: edge.To.PortID,
		},
		Presentation: edge.Presentation,
	}
}

func (e PatchEdge) sourceEdge() schema.Edge {
	return schema.Edge{
		Channel: e.Channel,
		From: schema.Endpoint{
			NodeID: string(e.From.NodeID),
			PortID: e.From.PortID,
		},
		To: schema.Endpoint{
			NodeID: string(e.To.NodeID),
			PortID: e.To.PortID,
		},
		Presentation: e.Presentation,
	}
}

type EdgeCommand struct {
	GraphID string    `json:"graphId"`
	Edge    PatchEdge `json:"edge"`
}

type AddGraphCommand struct {
	Graph schema.Graph `json:"graph"`
}

type RenameGraphCommand struct {
	GraphID string `json:"graphId"`
	Name    string `json:"name"`
}

type GraphCommand struct {
	GraphID string `json:"graphId"`
}

type GraphInterfaceCommand struct {
	GraphID string             `json:"graphId"`
	Inputs  []schema.GraphPort `json:"inputs"`
	Outputs []schema.GraphPort `json:"outputs"`
	Entries []schema.Endpoint  `json:"entries"`
	Exits   []schema.GraphExit `json:"exits"`
}

type GraphCallCommand struct {
	GraphID string           `json:"graphId"`
	Call    schema.GraphCall `json:"call"`
}

type CallCommand struct {
	GraphID string `json:"graphId"`
	CallID  string `json:"callId"`
}

type AnnotationCommand struct {
	GraphID    string            `json:"graphId"`
	Annotation schema.Annotation `json:"annotation"`
}

type AnnotationIDCommand struct {
	GraphID      string `json:"graphId"`
	AnnotationID string `json:"annotationId"`
}

type SetEdgeReroutesCommand struct {
	GraphID  string            `json:"graphId"`
	Edge     PatchEdge         `json:"edge"`
	Reroutes []schema.Position `json:"reroutes"`
}

type CollapseSelectionCommand struct {
	GraphID    string          `json:"graphId"`
	SubgraphID string          `json:"subgraphId"`
	CallID     string          `json:"callId"`
	Name       string          `json:"name"`
	NodeIDs    []string        `json:"nodeIds"`
	Position   schema.Position `json:"position"`
}

type GeneratedNode struct {
	CommandIndex int    `json:"commandIndex"`
	Handle       string `json:"handle,omitempty"`
	NodeID       string `json:"nodeId"`
}

type Result struct {
	Source         schema.WorkflowSource
	Artifact       []byte
	GeneratedNodes []GeneratedNode
}

type PatchError struct {
	CommandIndex int      `json:"commandIndex"`
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	GraphPath    []string `json:"graphPath,omitempty"`
	NodeID       string   `json:"nodeId,omitempty"`
	FieldPath    []string `json:"fieldPath,omitempty"`
}

func (e *PatchError) Error() string {
	if e.CommandIndex < 0 {
		return e.Code + ": " + e.Message
	}
	return fmt.Sprintf("command %d: %s: %s", e.CommandIndex, e.Code, e.Message)
}

func (e *PatchError) RPCErrorEnvelope() apperr.Envelope {
	if e == nil {
		return apperr.Envelope{
			ID:       apperr.IDUnexpected,
			Category: apperr.CategoryInfrastructure,
		}
	}
	params := map[string]any{
		"commandIndex": e.CommandIndex,
	}
	if len(e.GraphPath) != 0 {
		params["graphPath"] = append([]string(nil), e.GraphPath...)
	}
	if e.NodeID != "" {
		params["nodeId"] = e.NodeID
	}
	if len(e.FieldPath) != 0 {
		params["fieldPath"] = append([]string(nil), e.FieldPath...)
	}
	return apperr.Envelope{
		ID:       e.Code,
		Category: apperr.CategoryValidation,
		Params:   params,
	}
}

type IDFactory func() string

type Engine struct {
	packages   map[nodecontract.NodeRef]schema.NodePackageDependency
	catalog    nodecatalog.Snapshot
	projection nodeauthoring.Snapshot
	newID      IDFactory
}

func New(catalog nodecatalog.Snapshot, projection nodeauthoring.Snapshot, newID IDFactory, packages ...schema.NodePackageDependency) (*Engine, error) {
	if !catalog.Valid() || !projection.Valid() || projection.CatalogHash() != catalog.Hash() {
		return nil, errors.New("authoring engine requires matching trusted Catalog and projection")
	}
	if newID == nil {
		newID = uuid.NewString
	}
	byNode := map[nodecontract.NodeRef]schema.NodePackageDependency{}
	for _, p := range packages {
		for _, ref := range p.NodeRefs {
			entry, ok := catalog.Lookup(ref.NodeTypeID)
			if !ok || entry.Contract.NodeRef() != ref || entry.Implementation.PackageID != p.PackageID || entry.Implementation.ArtifactDigest != p.ManifestDigest {
				return nil, errors.New("package dependency does not match Catalog")
			}
			byNode[ref] = p
		}
	}
	return &Engine{catalog: catalog, projection: projection, newID: newID, packages: byNode}, nil
}

func (e *Engine) Apply(source schema.WorkflowSource, commands []Command) (Result, error) {
	if len(commands) == 0 || len(commands) > MaxCommands {
		return Result{}, patchError(-1, "COMMAND_BUDGET", "patch must contain between 1 and 256 commands")
	}
	working, err := cloneSource(source)
	if err != nil {
		return Result{}, err
	}
	handles := make(map[string]string)
	generated := make([]GeneratedNode, 0)
	for index := range commands {
		if err := e.applyCommand(&working, commands[index], index, handles, &generated); err != nil {
			return Result{}, err
		}
	}
	for _, graph := range working.Graphs {
		for _, node := range graph.Nodes {
			dependency, ok := e.packages[node.NodeRef]
			if !ok {
				continue
			}
			found := false
			for index := range working.Dependencies {
				current := &working.Dependencies[index]
				if current.PackageID != dependency.PackageID {
					continue
				}
				found = true
				if current.ManifestDigest != dependency.ManifestDigest {
					break
				}
				hasRef := false
				for _, ref := range current.NodeRefs {
					if ref == node.NodeRef {
						hasRef = true
						break
					}
				}
				if !hasRef {
					current.NodeRefs = append(current.NodeRefs, node.NodeRef)
				}
				break
			}
			if !found {
				dependency.NodeRefs = []nodecontract.NodeRef{node.NodeRef}
				working.Dependencies = append(working.Dependencies, dependency)
			}
		}
	}
	if working.Revision >= schema.MaxRevision {
		return Result{}, patchError(-1, "REVISION_EXHAUSTED", "workflow revision reached its maximum")
	}
	working.Revision++
	raw, err := artifact.Marshal(working)
	if err != nil {
		return Result{}, fmt.Errorf("encode patched Workflow Source: %w", err)
	}
	canonical, diagnostics := schema.ParseSource(raw)
	if len(diagnostics) != 0 {
		diagnostic := diagnostics[0]
		return Result{}, &PatchError{
			CommandIndex: -1, Code: diagnostic.Code, Message: "patched workflow failed validation",
			GraphPath: append([]string(nil), diagnostic.GraphPath...), NodeID: diagnostic.NodeID,
			FieldPath: append([]string(nil), diagnostic.FieldPath...),
		}
	}
	raw, err = artifact.Marshal(canonical)
	if err != nil {
		return Result{}, fmt.Errorf("encode canonical patched Workflow Source: %w", err)
	}
	return Result{Source: canonical, Artifact: raw, GeneratedNodes: generated}, nil
}

func (e *Engine) applyCommand(source *schema.WorkflowSource, command Command, index int, handles map[string]string, generated *[]GeneratedNode) error {
	if err := validateTaggedCommand(command); err != nil {
		return patchError(index, "INVALID_COMMAND", err.Error())
	}
	switch command.Kind {
	case CommandRenameWorkflow:
		name := strings.TrimSpace(command.RenameWorkflow.Name)
		if name == "" || utf8.RuneCountInString(name) > 256 {
			return patchError(index, "INVALID_NAME", "workflow name must contain 1 to 256 characters")
		}
		source.Workflow.Name = name
	case CommandUpdateWorkflowMetadata:
		payload := command.UpdateWorkflowMetadata
		metadata, err := NormalizeWorkflowMetadata(WorkflowMetadata{
			Name: payload.Name, Description: payload.Description, Category: payload.Category, Tags: payload.Tags,
		})
		if err != nil {
			return patchError(index, "INVALID_WORKFLOW_METADATA", err.Error())
		}
		source.Workflow.Name = metadata.Name
		source.Workflow.Description = metadata.Description
		source.Workflow.Category = metadata.Category
		source.Workflow.Tags = metadata.Tags
	case CommandSetTargetDefault:
		payload := command.SetTargetDefault
		if err := schema.SetTargetDefault(source, payload.Target, payload.Slot); err != nil {
			return patchError(index, "INVALID_TARGET_DEFAULT", err.Error())
		}
	case CommandClearTargetDefault:
		if err := schema.ClearTargetDefault(source, command.ClearTargetDefault.Target); err != nil {
			return patchError(index, "INVALID_TARGET_DEFAULT", err.Error())
		}
	case CommandAddStateVariable:
		payload := command.AddStateVariable
		if hasStateVariable(*source, payload.Name) {
			return patchError(index, "DUPLICATE_STATE", "state variable already exists")
		}
		if err := payload.Type.Validate(); err != nil {
			return patchError(index, "INVALID_STATE_TYPE", err.Error())
		}
		value, err := rawValue(payload.Default)
		if err != nil {
			return patchError(index, "INVALID_STATE_DEFAULT", err.Error())
		}
		source.Variables = append(source.Variables, schema.Variable{Name: payload.Name, Type: payload.Type, Default: value, Parameter: payload.Parameter})
	case CommandSetWorkflowTargets:
		next := command.SetWorkflowTargets.Targets
		if len(next) == 0 {
			return patchError(index, "INVALID_TARGET", "workflow requires one default target")
		}
		if err := schema.ValidateWorkflowTargets(next); err != nil {
			return patchError(index, "INVALID_TARGET", err.Error())
		}
		for _, old := range source.Targets {
			retained := false
			for _, item := range next {
				if item.ID == old.ID {
					retained = true
				}
			}
			if !retained {
				for _, graph := range source.Graphs {
					for _, node := range graph.Nodes {
						if e.referencesWorkflowTarget(node, old.ID) {
							return patchError(index, "REFERENCE_IN_USE", "workflow target is referenced by a node")
						}
					}
				}
			}
		}
		source.Targets = next
		for _, item := range next {
			if item.Default {
				if err := schema.SetTargetDefault(source, "target", item.ID); err != nil {
					return patchError(index, "INVALID_TARGET", err.Error())
				}
			}
		}
	case CommandSetParameterBlocks:
		source.ParameterBlocks = command.SetParameterBlocks.Blocks
	case CommandUpdateStateVariable:
		payload := command.UpdateStateVariable
		if !hasStateVariable(*source, payload.Name) {
			return patchError(index, "UNKNOWN_STATE", "state variable does not exist")
		}
		if err := payload.Type.Validate(); err != nil {
			return patchError(index, "INVALID_STATE_TYPE", err.Error())
		}
		value, err := rawValue(payload.Default)
		if err != nil {
			return patchError(index, "INVALID_STATE_DEFAULT", err.Error())
		}
		for variableIndex := range source.Variables {
			if source.Variables[variableIndex].Name == payload.Name {
				source.Variables[variableIndex].Type = payload.Type
				source.Variables[variableIndex].Default = value
				if payload.ClearParameter {
					source.Variables[variableIndex].Parameter = nil
				} else if payload.Parameter != nil {
					source.Variables[variableIndex].Parameter = payload.Parameter
				}
				break
			}
		}
	case CommandRemoveStateVariable:
		name := command.RemoveStateVariable.Name
		if !hasStateVariable(*source, name) {
			return patchError(index, "UNKNOWN_STATE", "state variable does not exist")
		}
		if e.stateVariableReferenced(*source, name) {
			return patchError(index, "REFERENCE_IN_USE", "state variable is still referenced by a node")
		}
		for variableIndex := range source.Variables {
			if source.Variables[variableIndex].Name == name {
				source.Variables = append(source.Variables[:variableIndex], source.Variables[variableIndex+1:]...)
				break
			}
		}
	case CommandAddNode:
		payload := command.AddNode
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		projection, ok := e.projection.Node(payload.NodeTypeID)
		if !ok {
			return patchError(index, "UNKNOWN_NODE_TYPE", "node type is not in the admitted Catalog")
		}
		if graph.Kind != schema.GraphKindMain && projection.Instruction.Kind == nodecontract.InstructionRunRoot {
			return patchError(index, "INVALID_INSTRUCTION_PLACEMENT", "run root nodes are only valid in the main graph")
		}
		if !finitePosition(payload.Position) {
			return patchError(index, "INVALID_POSITION", "node position must be finite")
		}
		if payload.Handle != "" {
			if !handlePattern.MatchString(payload.Handle) {
				return patchError(index, "INVALID_HANDLE", "node handle must match the authoring handle grammar")
			}
			if _, exists := handles[payload.Handle]; exists {
				return patchError(index, "DUPLICATE_HANDLE", "node handle already exists in this patch")
			}
		}
		nodeID := e.newID()
		if nodeID == "" || nodeByID(graph, nodeID) != nil {
			return patchError(index, "INVALID_GENERATED_ID", "host generated an invalid or duplicate node ID")
		}
		config := make(map[string]any)
		for _, field := range projection.ConfigFields {
			if field.HasDefault {
				value, decodeErr := decodeRaw(field.Default)
				if decodeErr != nil {
					return patchError(index, "INVALID_CATALOG_DEFAULT", decodeErr.Error())
				}
				config[field.ID] = value
			}
		}
		bindings := make(map[string]schema.InputBinding)
		for _, port := range projection.DataInputs {
			if port.HasDefault {
				bindings[port.ID] = schema.InputBinding{Kind: schema.BindingDefault}
			}
		}
		graph.Nodes = append(graph.Nodes, schema.Node{
			ID: nodeID, NodeRef: projection.NodeRef, Position: payload.Position, Config: config, Bindings: bindings,
		})
		if payload.Handle != "" {
			handles[payload.Handle] = nodeID
		}
		*generated = append(*generated, GeneratedNode{CommandIndex: index, Handle: payload.Handle, NodeID: nodeID})
	case CommandUpgradeNodeContract:
		graph, node, err := resolveNode(source, command.UpgradeNodeContract.GraphID, command.UpgradeNodeContract.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		projection, ok := e.projection.Node(node.NodeRef.NodeTypeID)
		if !ok {
			return patchError(index, "UNKNOWN_NODE_TYPE", "node type is not in the admitted Catalog")
		}
		if projection.NodeRef == node.NodeRef {
			break
		}
		migrations, ok := admittedNodeContractUpgradePath(node.NodeRef, projection.NodeRef)
		if !ok {
			return patchError(index, "INCOMPATIBLE_NODE_UPGRADE", "node contract migration is not admitted")
		}
		for _, migration := range migrations {
			if err := prepareAdmittedNodeContractUpgrade(migration, graph, node); err != nil {
				return patchError(index, "INCOMPATIBLE_NODE_UPGRADE", err.Error())
			}
		}
		if err := applyCompatibleNodeUpgrade(source, graph, node, projection); err != nil {
			return patchError(index, "INCOMPATIBLE_NODE_UPGRADE", err.Error())
		}
	case CommandRemoveNode:
		graph, node, err := resolveNode(source, command.RemoveNode.GraphID, command.RemoveNode.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		removeNode(graph, node.ID)
	case CommandMoveNode:
		if !finitePosition(command.MoveNode.Position) {
			return patchError(index, "INVALID_POSITION", "node position must be finite")
		}
		_, node, err := resolveNode(source, command.MoveNode.GraphID, command.MoveNode.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		node.Position = command.MoveNode.Position
	case CommandSetNodeLabel:
		_, node, err := resolveNode(source, command.SetNodeLabel.GraphID, command.SetNodeLabel.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		if len(command.SetNodeLabel.Label) > 1024 {
			return patchError(index, "INVALID_LABEL", "node label exceeds 1024 bytes")
		}
		node.Label = command.SetNodeLabel.Label
	case CommandSetNodeDisabled:
		_, node, err := resolveNode(source, command.SetNodeDisabled.GraphID, command.SetNodeDisabled.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		node.Disabled = command.SetNodeDisabled.Disabled
	case CommandSetConfig:
		graph, node, err := resolveNode(source, command.SetConfig.GraphID, command.SetConfig.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		if _, ok := configField(e.projection, *node, command.SetConfig.FieldID); !ok {
			return patchError(index, "UNKNOWN_CONFIG_FIELD", "config field is not declared by the Node Contract")
		}
		value, err := cloneValue(command.SetConfig.Value)
		if err != nil {
			return patchError(index, "INVALID_CONFIG_VALUE", err.Error())
		}
		node.Config[command.SetConfig.FieldID] = value
		pruneInvalidNodeTopology(source, graph, node, e.projection)
	case CommandClearConfig:
		graph, node, err := resolveNode(source, command.ClearConfig.GraphID, command.ClearConfig.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		field, ok := configField(e.projection, *node, command.ClearConfig.FieldID)
		if !ok {
			return patchError(index, "UNKNOWN_CONFIG_FIELD", "config field is not declared by the Node Contract")
		}
		inherited := inheritsConfigField(*source, e.projection, *node, field.ID)
		if field.Required && !field.HasDefault && !inherited {
			return patchError(index, "REQUIRED_CONFIG_FIELD", "required config field cannot be cleared")
		}
		if inherited {
			delete(node.Config, field.ID)
		} else if field.HasDefault {
			value, decodeErr := decodeRaw(field.Default)
			if decodeErr != nil {
				return patchError(index, "INVALID_CATALOG_DEFAULT", decodeErr.Error())
			}
			node.Config[field.ID] = value
		} else {
			delete(node.Config, field.ID)
		}
		pruneInvalidNodeTopology(source, graph, node, e.projection)
	case CommandBindValue:
		graph, node, err := resolveNode(source, command.BindValue.GraphID, command.BindValue.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		if _, ok := dataInput(e.projection, *node, command.BindValue.PortID); !ok {
			return patchError(index, "UNKNOWN_DATA_INPUT", "data input is not declared by the Node Contract")
		}
		value, err := rawValue(command.BindValue.Value)
		if err != nil {
			return patchError(index, "INVALID_BINDING_VALUE", err.Error())
		}
		removeIncomingData(graph, node.ID, command.BindValue.PortID)
		node.Bindings[command.BindValue.PortID] = schema.InputBinding{Kind: schema.BindingValue, Value: value}
	case CommandBindDefault:
		graph, node, err := resolveNode(source, command.BindDefault.GraphID, command.BindDefault.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		port, ok := dataInput(e.projection, *node, command.BindDefault.PortID)
		if !ok || !port.HasDefault {
			return patchError(index, "DEFAULT_UNAVAILABLE", "data input has no declared default")
		}
		removeIncomingData(graph, node.ID, command.BindDefault.PortID)
		node.Bindings[command.BindDefault.PortID] = schema.InputBinding{Kind: schema.BindingDefault}
	case CommandBindBlob:
		graph, node, err := resolveNode(source, command.BindBlob.GraphID, command.BindBlob.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		if _, ok := dataInput(e.projection, *node, command.BindBlob.PortID); !ok {
			return patchError(index, "UNKNOWN_DATA_INPUT", "data input is not declared by the Node Contract")
		}
		if err := command.BindBlob.Blob.Validate(); err != nil {
			return patchError(index, "INVALID_BLOB_REF", err.Error())
		}
		blobRef := command.BindBlob.Blob
		removeIncomingData(graph, node.ID, command.BindBlob.PortID)
		node.Bindings[command.BindBlob.PortID] = schema.InputBinding{Kind: schema.BindingBlob, Blob: &blobRef}
	case CommandBindResource:
		graph, node, err := resolveNode(source, command.BindResource.GraphID, command.BindResource.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		if _, ok := dataInput(e.projection, *node, command.BindResource.PortID); !ok {
			return patchError(index, "UNKNOWN_DATA_INPUT", "data input is not declared by the Node Contract")
		}
		resource := command.BindResource.Resource
		if _, err := schema.ResolveResourceBinding(source.Resources, resource); err != nil {
			return patchError(index, "INVALID_RESOURCE_BINDING", err.Error())
		}
		removeIncomingData(graph, node.ID, command.BindResource.PortID)
		node.Bindings[command.BindResource.PortID] = schema.InputBinding{Kind: schema.BindingResource, Resource: &resource}
	case CommandAddResource:
		resource := command.AddResource.Resource
		if resourceByID(source.Resources, resource.ID) != nil {
			return patchError(index, "DUPLICATE_RESOURCE", "workflow resource already exists")
		}
		resource.Name = strings.TrimSpace(resource.Name)
		resource.Description = strings.TrimSpace(resource.Description)
		resource.Category = strings.TrimSpace(resource.Category)
		resource.Tags = normalizeResourceTags(resource.Tags)
		source.Resources = append(source.Resources, resource)
		sort.Slice(source.Resources, func(left, right int) bool {
			return source.Resources[left].ID < source.Resources[right].ID
		})
	case CommandReplaceResource:
		payload := command.ReplaceResource
		resource := resourceByID(source.Resources, payload.ResourceID)
		if resource == nil {
			return patchError(index, "UNKNOWN_RESOURCE", "workflow resource does not exist")
		}
		replacement := payload.Resource
		if replacement.ID != payload.ResourceID {
			return patchError(index, "RESOURCE_ID_CHANGED", "workflow resource replacement must preserve identity")
		}
		if replacement.Kind != resource.Kind {
			return patchError(index, "RESOURCE_KIND_CHANGED", "workflow resource replacement must preserve kind")
		}
		if err := schema.ValidateWorkflowResource(replacement); err != nil {
			return patchError(index, "INVALID_RESOURCE", err.Error())
		}
		*resource = replacement
	case CommandUpdateResourceMetadata:
		payload := command.UpdateResourceMetadata
		resource := resourceByID(source.Resources, payload.ResourceID)
		if resource == nil {
			return patchError(index, "UNKNOWN_RESOURCE", "workflow resource does not exist")
		}
		resource.Name = strings.TrimSpace(payload.Name)
		resource.Description = strings.TrimSpace(payload.Description)
		resource.Category = strings.TrimSpace(payload.Category)
		resource.Tags = normalizeResourceTags(payload.Tags)
	case CommandRemoveResource:
		resourceID := strings.TrimSpace(command.RemoveResource.ResourceID)
		if resourceByID(source.Resources, resourceID) == nil {
			return patchError(index, "UNKNOWN_RESOURCE", "workflow resource does not exist")
		}
		if countResourceReferences(*source, resourceID) != 0 {
			return patchError(index, "RESOURCE_IN_USE", "workflow resource still has bindings")
		}
		for resourceIndex := range source.Resources {
			if source.Resources[resourceIndex].ID == resourceID {
				source.Resources = append(source.Resources[:resourceIndex], source.Resources[resourceIndex+1:]...)
				break
			}
		}
	case CommandClearBinding:
		_, node, err := resolveNode(source, command.ClearBinding.GraphID, command.ClearBinding.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_NODE", err.Error())
		}
		if _, ok := dataInput(e.projection, *node, command.ClearBinding.PortID); !ok {
			return patchError(index, "UNKNOWN_DATA_INPUT", "data input is not declared by the Node Contract")
		}
		delete(node.Bindings, command.ClearBinding.PortID)
	case CommandConnect:
		payload := command.Connect
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		edge := payload.Edge.sourceEdge()
		edge.From.NodeID, err = resolveNodeReference(edge.From.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_HANDLE", err.Error())
		}
		edge.To.NodeID, err = resolveNodeReference(edge.To.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_HANDLE", err.Error())
		}
		if !graphElementExists(graph, edge.From.NodeID) || !graphElementExists(graph, edge.To.NodeID) {
			return patchError(index, "UNKNOWN_NODE", "edge endpoint node does not exist")
		}
		if !e.edgeMatches(source, graph, edge) {
			return patchError(index, "INVALID_EDGE", "edge channel, direction, or port does not match the Node Contracts")
		}
		if edgeExists(*graph, edge) {
			return patchError(index, "DUPLICATE_EDGE", "edge already exists")
		}
		if edge.Channel == schema.EdgeData {
			if to := nodeByID(graph, edge.To.NodeID); to != nil {
				delete(to.Bindings, edge.To.PortID)
			}
			removeIncomingData(graph, edge.To.NodeID, edge.To.PortID)
		}
		graph.Edges = append(graph.Edges, edge)
	case CommandDisconnect:
		payload := command.Disconnect
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		edge := payload.Edge.sourceEdge()
		edge.From.NodeID, err = resolveNodeReference(edge.From.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_HANDLE", err.Error())
		}
		edge.To.NodeID, err = resolveNodeReference(edge.To.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_HANDLE", err.Error())
		}
		found := false
		for edgeIndex := range graph.Edges {
			if sameEdge(graph.Edges[edgeIndex], edge) {
				graph.Edges = append(graph.Edges[:edgeIndex], graph.Edges[edgeIndex+1:]...)
				found = true
				break
			}
		}
		if !found {
			return patchError(index, "UNKNOWN_EDGE", "edge does not exist")
		}
	case CommandAddGraph:
		graph := command.AddGraph.Graph
		if _, err := graphByID(source, graph.ID); err == nil {
			return patchError(index, "DUPLICATE_GRAPH", "graph already exists")
		}
		if graph.Kind != schema.GraphKindSubgraph {
			return patchError(index, "INVALID_GRAPH", "only subgraphs can be added")
		}
		source.Graphs = append(source.Graphs, graph)
	case CommandRenameGraph:
		graph, err := graphByID(source, command.RenameGraph.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		name := strings.TrimSpace(command.RenameGraph.Name)
		if len(name) > 256 {
			return patchError(index, "INVALID_NAME", "graph name exceeds 256 bytes")
		}
		graph.Name = name
	case CommandRemoveGraph:
		graphID := command.RemoveGraph.GraphID
		if graphID == source.EntryGraph {
			return patchError(index, "ENTRY_GRAPH", "entry graph cannot be removed")
		}
		if _, err := graphByID(source, graphID); err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		for _, graph := range source.Graphs {
			for _, call := range graph.Calls {
				if call.GraphID == graphID {
					return patchError(index, "REFERENCE_IN_USE", "graph is still referenced by a call")
				}
			}
		}
		for graphIndex := range source.Graphs {
			if source.Graphs[graphIndex].ID == graphID {
				source.Graphs = append(source.Graphs[:graphIndex], source.Graphs[graphIndex+1:]...)
				break
			}
		}
	case CommandUpdateGraphInterface:
		payload := command.UpdateGraphInterface
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		if graph.Kind != schema.GraphKindSubgraph {
			return patchError(index, "INVALID_GRAPH", "only subgraphs have a callable interface")
		}
		inputs := append([]schema.GraphPort{}, payload.Inputs...)
		outputs := append([]schema.GraphPort{}, payload.Outputs...)
		entries := append([]schema.Endpoint{}, payload.Entries...)
		exits := append([]schema.GraphExit{}, payload.Exits...)
		for portIndex := range inputs {
			inputs[portIndex].NodeID, err = resolveNodeReference(inputs[portIndex].NodeID, handles)
			if err != nil {
				return patchError(index, "UNKNOWN_HANDLE", err.Error())
			}
		}
		for portIndex := range outputs {
			outputs[portIndex].NodeID, err = resolveNodeReference(outputs[portIndex].NodeID, handles)
			if err != nil {
				return patchError(index, "UNKNOWN_HANDLE", err.Error())
			}
		}
		for entryIndex := range entries {
			entries[entryIndex].NodeID, err = resolveNodeReference(entries[entryIndex].NodeID, handles)
			if err != nil {
				return patchError(index, "UNKNOWN_HANDLE", err.Error())
			}
		}
		for exitIndex := range exits {
			exits[exitIndex].Endpoint.NodeID, err = resolveNodeReference(exits[exitIndex].Endpoint.NodeID, handles)
			if err != nil {
				return patchError(index, "UNKNOWN_HANDLE", err.Error())
			}
		}
		inputIDs, outputIDs, exitIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, port := range inputs {
			inputIDs[port.ID] = true
		}
		for _, port := range outputs {
			outputIDs[port.ID] = true
		}
		for _, exit := range exits {
			exitIDs[exit.ID] = true
		}
		for _, caller := range source.Graphs {
			for _, call := range caller.Calls {
				if call.GraphID != payload.GraphID {
					continue
				}
				for portID := range call.Bindings {
					if !inputIDs[portID] {
						return patchError(index, "REFERENCE_IN_USE", "removed graph input is still bound by a call")
					}
				}
				for _, edge := range caller.Edges {
					if edge.To.NodeID == call.ID && edge.Channel == schema.EdgeData && !inputIDs[edge.To.PortID] ||
						edge.From.NodeID == call.ID && edge.Channel == schema.EdgeData && !outputIDs[edge.From.PortID] ||
						edge.From.NodeID == call.ID && edge.Channel != schema.EdgeData && !exitIDs[edge.From.PortID] {
						return patchError(index, "REFERENCE_IN_USE", "removed graph port is still connected by a call")
					}
				}
			}
		}
		graph.Inputs = inputs
		graph.Outputs = outputs
		graph.Entries = entries
		graph.Exits = exits
	case CommandAddGraphCall:
		payload := command.AddGraphCall
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		callee, err := graphByID(source, payload.Call.GraphID)
		if err != nil || callee.Kind != schema.GraphKindSubgraph {
			return patchError(index, "UNKNOWN_CALLEE", "callee subgraph does not exist")
		}
		if graphElementExists(graph, payload.Call.ID) {
			return patchError(index, "DUPLICATE_ID", "graph element already exists")
		}
		graph.Calls = append(graph.Calls, payload.Call)
	case CommandUpdateGraphCall:
		payload := command.UpdateGraphCall
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		call := graphCallByID(graph, payload.Call.ID)
		if call == nil {
			return patchError(index, "UNKNOWN_CALL", "graph call does not exist")
		}
		*call = payload.Call
	case CommandRemoveGraphCall:
		payload := command.RemoveGraphCall
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		if !removeGraphCall(graph, payload.CallID) {
			return patchError(index, "UNKNOWN_CALL", "graph call does not exist")
		}
	case CommandAddAnnotation:
		payload := command.AddAnnotation
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		if annotationByID(graph, payload.Annotation.ID) != nil {
			return patchError(index, "DUPLICATE_ID", "annotation already exists")
		}
		graph.Annotations = append(graph.Annotations, payload.Annotation)
	case CommandUpdateAnnotation:
		payload := command.UpdateAnnotation
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		annotation := annotationByID(graph, payload.Annotation.ID)
		if annotation == nil {
			return patchError(index, "UNKNOWN_ANNOTATION", "annotation does not exist")
		}
		*annotation = payload.Annotation
	case CommandRemoveAnnotation:
		payload := command.RemoveAnnotation
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		if !removeAnnotation(graph, payload.AnnotationID) {
			return patchError(index, "UNKNOWN_ANNOTATION", "annotation does not exist")
		}
	case CommandSetEdgeReroutes:
		payload := command.SetEdgeReroutes
		graph, err := graphByID(source, payload.GraphID)
		if err != nil {
			return patchError(index, "UNKNOWN_GRAPH", err.Error())
		}
		candidate := payload.Edge.sourceEdge()
		candidate.From.NodeID, err = resolveNodeReference(candidate.From.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_HANDLE", err.Error())
		}
		candidate.To.NodeID, err = resolveNodeReference(candidate.To.NodeID, handles)
		if err != nil {
			return patchError(index, "UNKNOWN_HANDLE", err.Error())
		}
		edge := edgeByIdentity(graph, candidate)
		if edge == nil {
			return patchError(index, "UNKNOWN_EDGE", "edge does not exist")
		}
		edge.Presentation.Reroutes = append([]schema.Position(nil), payload.Reroutes...)
	case CommandCollapseSelection:
		if err := e.collapseSelection(source, *command.CollapseSelection); err != nil {
			return patchError(index, "INVALID_SELECTION", err.Error())
		}
	default:
		return patchError(index, "INVALID_COMMAND", "unknown command kind")
	}
	return nil
}

func validateTaggedCommand(command Command) error {
	payloads := []bool{
		command.SetWorkflowTargets != nil,
		command.SetParameterBlocks != nil,
		command.RenameWorkflow != nil, command.UpdateWorkflowMetadata != nil,
		command.SetTargetDefault != nil, command.ClearTargetDefault != nil,
		command.AddStateVariable != nil, command.UpdateStateVariable != nil, command.RemoveStateVariable != nil,
		command.AddNode != nil, command.UpgradeNodeContract != nil, command.RemoveNode != nil, command.MoveNode != nil, command.SetNodeLabel != nil,
		command.SetNodeDisabled != nil, command.SetConfig != nil, command.ClearConfig != nil,
		command.BindValue != nil, command.BindDefault != nil, command.BindBlob != nil, command.BindResource != nil,
		command.AddResource != nil, command.ReplaceResource != nil, command.UpdateResourceMetadata != nil,
		command.RemoveResource != nil, command.ClearBinding != nil,
		command.Connect != nil, command.Disconnect != nil,
		command.AddGraph != nil, command.RenameGraph != nil, command.RemoveGraph != nil, command.UpdateGraphInterface != nil,
		command.AddGraphCall != nil, command.RemoveGraphCall != nil,
		command.UpdateGraphCall != nil,
		command.AddAnnotation != nil, command.UpdateAnnotation != nil, command.RemoveAnnotation != nil,
		command.SetEdgeReroutes != nil, command.CollapseSelection != nil,
	}
	count := 0
	for _, present := range payloads {
		if present {
			count++
		}
	}
	if count != 1 {
		return errors.New("command must contain exactly one payload")
	}
	matches := map[CommandKind]bool{
		CommandSetWorkflowTargets: command.SetWorkflowTargets != nil,
		CommandSetParameterBlocks: command.SetParameterBlocks != nil,
		CommandRenameWorkflow:     command.RenameWorkflow != nil, CommandAddStateVariable: command.AddStateVariable != nil,
		CommandUpdateWorkflowMetadata: command.UpdateWorkflowMetadata != nil,
		CommandSetTargetDefault:       command.SetTargetDefault != nil, CommandClearTargetDefault: command.ClearTargetDefault != nil,
		CommandUpdateStateVariable: command.UpdateStateVariable != nil,
		CommandRemoveStateVariable: command.RemoveStateVariable != nil, CommandAddNode: command.AddNode != nil,
		CommandUpgradeNodeContract: command.UpgradeNodeContract != nil,
		CommandRemoveNode:          command.RemoveNode != nil, CommandMoveNode: command.MoveNode != nil,
		CommandSetNodeLabel: command.SetNodeLabel != nil, CommandSetNodeDisabled: command.SetNodeDisabled != nil,
		CommandSetConfig: command.SetConfig != nil, CommandClearConfig: command.ClearConfig != nil,
		CommandBindValue: command.BindValue != nil, CommandBindDefault: command.BindDefault != nil,
		CommandBindBlob: command.BindBlob != nil, CommandBindResource: command.BindResource != nil,
		CommandAddResource: command.AddResource != nil, CommandReplaceResource: command.ReplaceResource != nil,
		CommandUpdateResourceMetadata: command.UpdateResourceMetadata != nil,
		CommandRemoveResource:         command.RemoveResource != nil,
		CommandClearBinding:           command.ClearBinding != nil,
		CommandConnect:                command.Connect != nil, CommandDisconnect: command.Disconnect != nil,
		CommandAddGraph: command.AddGraph != nil, CommandRenameGraph: command.RenameGraph != nil,
		CommandRemoveGraph: command.RemoveGraph != nil, CommandAddGraphCall: command.AddGraphCall != nil,
		CommandUpdateGraphInterface: command.UpdateGraphInterface != nil,
		CommandUpdateGraphCall:      command.UpdateGraphCall != nil,
		CommandRemoveGraphCall:      command.RemoveGraphCall != nil, CommandAddAnnotation: command.AddAnnotation != nil,
		CommandUpdateAnnotation: command.UpdateAnnotation != nil, CommandRemoveAnnotation: command.RemoveAnnotation != nil,
		CommandSetEdgeReroutes: command.SetEdgeReroutes != nil, CommandCollapseSelection: command.CollapseSelection != nil,
	}
	if !matches[command.Kind] {
		return errors.New("command kind does not match its payload")
	}
	return nil
}

func cloneSource(source schema.WorkflowSource) (schema.WorkflowSource, error) {
	raw, err := artifact.Marshal(source)
	if err != nil {
		return schema.WorkflowSource{}, err
	}
	clone, diagnostics := schema.ParseSource(raw)
	if len(diagnostics) != 0 {
		return schema.WorkflowSource{}, patchError(-1, "INVALID_SOURCE", diagnostics[0].Code)
	}
	return clone, nil
}

func cloneValue(value any) (any, error) {
	raw, err := artifact.Marshal(value)
	if err != nil {
		return nil, err
	}
	return decodeRaw(raw)
}

func rawValue(value any) (json.RawMessage, error) {
	raw, err := artifact.Marshal(value)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func decodeRaw(raw []byte) (any, error) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func patchError(index int, code, message string) error {
	return &PatchError{CommandIndex: index, Code: code, Message: message}
}

func graphByID(source *schema.WorkflowSource, graphID string) (*schema.Graph, error) {
	for index := range source.Graphs {
		if source.Graphs[index].ID == graphID {
			return &source.Graphs[index], nil
		}
	}
	return nil, fmt.Errorf("graph %q does not exist", graphID)
}

func resolveNode(source *schema.WorkflowSource, graphID, reference string, handles map[string]string) (*schema.Graph, *schema.Node, error) {
	graph, err := graphByID(source, graphID)
	if err != nil {
		return nil, nil, err
	}
	nodeID, err := resolveNodeReference(reference, handles)
	if err != nil {
		return nil, nil, err
	}
	node := nodeByID(graph, nodeID)
	if node == nil {
		return nil, nil, fmt.Errorf("node %q does not exist in graph %q", nodeID, graphID)
	}
	return graph, node, nil
}

func resolveNodeReference(reference string, handles map[string]string) (string, error) {
	if !strings.HasPrefix(reference, "$") {
		return reference, nil
	}
	nodeID, ok := handles[strings.TrimPrefix(reference, "$")]
	if !ok {
		return "", fmt.Errorf("node handle %q has not been generated in this patch", reference)
	}
	return nodeID, nil
}

func nodeByID(graph *schema.Graph, nodeID string) *schema.Node {
	for index := range graph.Nodes {
		if graph.Nodes[index].ID == nodeID {
			return &graph.Nodes[index]
		}
	}
	return nil
}

func graphElementExists(graph *schema.Graph, id string) bool {
	if nodeByID(graph, id) != nil {
		return true
	}
	for _, call := range graph.Calls {
		if call.ID == id {
			return true
		}
	}
	for _, annotation := range graph.Annotations {
		if annotation.ID == id {
			return true
		}
	}
	return false
}

func graphCallByID(graph *schema.Graph, id string) *schema.GraphCall {
	for index := range graph.Calls {
		if graph.Calls[index].ID == id {
			return &graph.Calls[index]
		}
	}
	return nil
}

func removeGraphCall(graph *schema.Graph, id string) bool {
	for index := range graph.Calls {
		if graph.Calls[index].ID == id {
			graph.Calls = append(graph.Calls[:index], graph.Calls[index+1:]...)
			graph.Edges = removeElementEdges(graph.Edges, id)
			return true
		}
	}
	return false
}

func annotationByID(graph *schema.Graph, id string) *schema.Annotation {
	for index := range graph.Annotations {
		if graph.Annotations[index].ID == id {
			return &graph.Annotations[index]
		}
	}
	return nil
}

func removeAnnotation(graph *schema.Graph, id string) bool {
	for index := range graph.Annotations {
		if graph.Annotations[index].ID == id {
			graph.Annotations = append(graph.Annotations[:index], graph.Annotations[index+1:]...)
			return true
		}
	}
	return false
}

func edgeByIdentity(graph *schema.Graph, candidate schema.Edge) *schema.Edge {
	for index := range graph.Edges {
		if sameEdge(graph.Edges[index], candidate) {
			return &graph.Edges[index]
		}
	}
	return nil
}

func sameEdge(left, right schema.Edge) bool {
	return left.Channel == right.Channel && left.From == right.From && left.To == right.To
}

func removeElementEdges(edges []schema.Edge, id string) []schema.Edge {
	result := edges[:0]
	for _, edge := range edges {
		if edge.From.NodeID != id && edge.To.NodeID != id {
			result = append(result, edge)
		}
	}
	return result
}

func removeNode(graph *schema.Graph, nodeID string) {
	for index := range graph.Nodes {
		if graph.Nodes[index].ID == nodeID {
			graph.Nodes = append(graph.Nodes[:index], graph.Nodes[index+1:]...)
			break
		}
	}
	edges := graph.Edges[:0]
	for _, edge := range graph.Edges {
		if edge.From.NodeID != nodeID && edge.To.NodeID != nodeID {
			edges = append(edges, edge)
		}
	}
	graph.Edges = edges
}

func (e *Engine) collapseSelection(source *schema.WorkflowSource, command CollapseSelectionCommand) error {
	graph, err := graphByID(source, command.GraphID)
	if err != nil {
		return err
	}
	if len(command.NodeIDs) == 0 || graphElementExists(graph, command.CallID) {
		return errors.New("selection and unique call ID are required")
	}
	if _, err := graphByID(source, command.SubgraphID); err == nil {
		return errors.New("subgraph ID already exists")
	}
	selected := make(map[string]bool, len(command.NodeIDs))
	for _, id := range command.NodeIDs {
		if selected[id] || nodeByID(graph, id) == nil && graphCallByID(graph, id) == nil {
			return fmt.Errorf("selected graph element %q is invalid", id)
		}
		if node := nodeByID(graph, id); node != nil {
			projection, ok := e.projection.Node(node.NodeRef.NodeTypeID)
			if ok && projection.Instruction.Kind == nodecontract.InstructionRunRoot {
				return errors.New("run root nodes cannot be moved into a subgraph")
			}
		}
		selected[id] = true
	}
	subgraph := schema.Graph{
		ID: command.SubgraphID, Name: strings.TrimSpace(command.Name), Kind: schema.GraphKindSubgraph,
		Nodes: []schema.Node{}, Calls: []schema.GraphCall{}, Edges: []schema.Edge{}, Inputs: []schema.GraphPort{},
		Outputs: []schema.GraphPort{}, Entries: []schema.Endpoint{}, Exits: []schema.GraphExit{}, Annotations: []schema.Annotation{},
	}
	for _, node := range graph.Nodes {
		if selected[node.ID] {
			subgraph.Nodes = append(subgraph.Nodes, node)
		}
	}
	for _, call := range graph.Calls {
		if selected[call.ID] {
			subgraph.Calls = append(subgraph.Calls, call)
		}
	}
	inputIDs, outputIDs, exitIDs := map[schema.Endpoint]string{}, map[schema.Endpoint]string{}, map[struct {
		channel schema.EdgeChannel
		point   schema.Endpoint
	}]string{}
	parentEdges := make([]schema.Edge, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		fromSelected, toSelected := selected[edge.From.NodeID], selected[edge.To.NodeID]
		switch {
		case fromSelected && toSelected:
			subgraph.Edges = append(subgraph.Edges, edge)
		case !fromSelected && !toSelected:
			parentEdges = append(parentEdges, edge)
		case !fromSelected && toSelected:
			if edge.Channel == schema.EdgeError {
				return errors.New("selection has an incoming error route")
			}
			copyEdge := edge
			if edge.Channel == schema.EdgeExec {
				if len(subgraph.Entries) != 0 && subgraph.Entries[0] != edge.To {
					return errors.New("selection has multiple execution entries")
				}
				if len(subgraph.Entries) == 0 {
					subgraph.Entries = append(subgraph.Entries, edge.To)
				}
				copyEdge.To = schema.Endpoint{NodeID: command.CallID, PortID: graphCallInputPort}
			} else {
				portID := inputIDs[edge.To]
				if portID == "" {
					portID = uniqueBoundaryID("input", edge.To.PortID, len(subgraph.Inputs)+1)
					typ, ok := e.endpointType(source, graph, edge.To, false)
					if !ok {
						return errors.New("selection data input has no declared type")
					}
					inputIDs[edge.To] = portID
					subgraph.Inputs = append(subgraph.Inputs, schema.GraphPort{ID: portID, Name: edge.To.PortID, Type: typ, NodeID: edge.To.NodeID, PortID: edge.To.PortID})
				}
				copyEdge.To = schema.Endpoint{NodeID: command.CallID, PortID: portID}
			}
			parentEdges = append(parentEdges, copyEdge)
		case fromSelected && !toSelected:
			copyEdge := edge
			if edge.Channel == schema.EdgeData {
				portID := outputIDs[edge.From]
				if portID == "" {
					portID = uniqueBoundaryID("output", edge.From.PortID, len(subgraph.Outputs)+1)
					typ, ok := e.endpointType(source, graph, edge.From, true)
					if !ok {
						return errors.New("selection data output has no declared type")
					}
					outputIDs[edge.From] = portID
					subgraph.Outputs = append(subgraph.Outputs, schema.GraphPort{ID: portID, Name: edge.From.PortID, Type: typ, NodeID: edge.From.NodeID, PortID: edge.From.PortID})
				}
				copyEdge.From = schema.Endpoint{NodeID: command.CallID, PortID: portID}
			} else {
				key := struct {
					channel schema.EdgeChannel
					point   schema.Endpoint
				}{edge.Channel, edge.From}
				exitID := exitIDs[key]
				if exitID == "" {
					exitID = uniqueBoundaryID("exit", edge.From.PortID, len(subgraph.Exits)+1)
					exitIDs[key] = exitID
					subgraph.Exits = append(subgraph.Exits, schema.GraphExit{ID: exitID, Name: edge.From.PortID, Channel: edge.Channel, Endpoint: edge.From})
				}
				copyEdge.From = schema.Endpoint{NodeID: command.CallID, PortID: exitID}
			}
			parentEdges = append(parentEdges, copyEdge)
		}
	}
	hasIncoming := func(nodeID, portID string, channel schema.EdgeChannel) bool {
		for _, edge := range graph.Edges {
			if edge.Channel == channel && edge.To.NodeID == nodeID && edge.To.PortID == portID {
				return true
			}
		}
		return false
	}
	hasOutgoing := func(nodeID, portID string, channel schema.EdgeChannel) bool {
		for _, edge := range graph.Edges {
			if edge.Channel == channel && edge.From.NodeID == nodeID && edge.From.PortID == portID {
				return true
			}
		}
		return false
	}
	addEntry := func(endpoint schema.Endpoint) error {
		if len(subgraph.Entries) != 0 && subgraph.Entries[0] != endpoint {
			return errors.New("selection has multiple execution entries")
		}
		if len(subgraph.Entries) == 0 {
			subgraph.Entries = append(subgraph.Entries, endpoint)
		}
		return nil
	}
	addExit := func(endpoint schema.Endpoint, channel schema.EdgeChannel) {
		subgraph.Exits = append(subgraph.Exits, schema.GraphExit{
			ID:       uniqueBoundaryID("exit", endpoint.PortID, len(subgraph.Exits)+1),
			Name:     endpoint.PortID,
			Channel:  channel,
			Endpoint: endpoint,
		})
	}
	for _, node := range subgraph.Nodes {
		projection, ok := instanceProjection(e.projection, node)
		if !ok {
			return fmt.Errorf("selected node %q has no exact authoring projection", node.ID)
		}
		for _, signal := range projection.Signals {
			endpoint := schema.Endpoint{NodeID: node.ID, PortID: signal.ID}
			channel := schema.EdgeChannel(signal.Channel)
			if signal.Direction == "input" && channel == schema.EdgeExec &&
				isGraphEntryInput(projection.Instruction, signal.ID) &&
				!hasIncoming(node.ID, signal.ID, channel) {
				if err := addEntry(endpoint); err != nil {
					return err
				}
			}
			if signal.Direction == "output" && !hasOutgoing(node.ID, signal.ID, channel) {
				addExit(endpoint, channel)
			}
		}
	}
	for _, call := range subgraph.Calls {
		if !hasIncoming(call.ID, graphCallInputPort, schema.EdgeExec) {
			if err := addEntry(schema.Endpoint{NodeID: call.ID, PortID: graphCallInputPort}); err != nil {
				return err
			}
		}
		callee, err := graphByID(source, call.GraphID)
		if err != nil {
			return err
		}
		for _, exit := range callee.Exits {
			if !hasOutgoing(call.ID, exit.ID, exit.Channel) {
				addExit(schema.Endpoint{NodeID: call.ID, PortID: exit.ID}, exit.Channel)
			}
		}
	}
	if len(subgraph.Entries) == 0 || len(subgraph.Exits) == 0 {
		return errors.New("selection must have one execution entry and at least one signal exit")
	}
	graph.Nodes = filterNodes(graph.Nodes, selected)
	graph.Calls = filterCalls(graph.Calls, selected)
	graph.Edges = parentEdges
	graph.Calls = append(graph.Calls, schema.GraphCall{ID: command.CallID, GraphID: command.SubgraphID, Label: subgraph.Name, Position: command.Position, Bindings: map[string]schema.InputBinding{}})
	source.Graphs = append(source.Graphs, subgraph)
	return nil
}

func isGraphEntryInput(instruction nodecontract.InstructionSpec, portID string) bool {
	switch instruction.Kind {
	case nodecontract.InstructionInvoke:
		return true
	case nodecontract.InstructionCountedLoop:
		return instruction.CountedLoop != nil && instruction.CountedLoop.EntryInput == portID
	case nodecontract.InstructionForEach:
		return instruction.ForEach != nil && instruction.ForEach.EntryInput == portID
	case nodecontract.InstructionRetry:
		return instruction.Retry != nil && instruction.Retry.EntryInput == portID
	default:
		return false
	}
}

func (e *Engine) endpointType(source *schema.WorkflowSource, graph *schema.Graph, endpoint schema.Endpoint, output bool) (datatype.TypeExpression, bool) {
	if node := nodeByID(graph, endpoint.NodeID); node != nil {
		projection, ok := instanceProjection(e.projection, *node)
		if !ok {
			return datatype.TypeExpression{}, false
		}
		ports := projection.DataInputs
		if output {
			ports = projection.DataOutputs
		}
		for _, port := range ports {
			if port.ID == endpoint.PortID {
				return port.Type.Expression, true
			}
		}
		return datatype.TypeExpression{}, false
	}
	call := graphCallByID(graph, endpoint.NodeID)
	if call == nil {
		return datatype.TypeExpression{}, false
	}
	callee, err := graphByID(source, call.GraphID)
	if err != nil {
		return datatype.TypeExpression{}, false
	}
	ports := callee.Inputs
	if output {
		ports = callee.Outputs
	}
	for _, port := range ports {
		if port.ID == endpoint.PortID {
			return port.Type, true
		}
	}
	return datatype.TypeExpression{}, false
}

func (e *Engine) edgeMatches(source *schema.WorkflowSource, graph *schema.Graph, edge schema.Edge) bool {
	if edge.Channel == schema.EdgeData {
		_, fromOK := e.endpointType(source, graph, edge.From, true)
		_, toOK := e.endpointType(source, graph, edge.To, false)
		return fromOK && toOK
	}
	fromOK := false
	if node := nodeByID(graph, edge.From.NodeID); node != nil {
		projection, ok := instanceProjection(e.projection, *node)
		if ok {
			for _, signal := range projection.Signals {
				fromOK = fromOK || signal.ID == edge.From.PortID && signal.Channel == string(edge.Channel) && signal.Direction == "output"
			}
		}
	} else if call := graphCallByID(graph, edge.From.NodeID); call != nil {
		if callee, err := graphByID(source, call.GraphID); err == nil {
			for _, exit := range callee.Exits {
				fromOK = fromOK || exit.ID == edge.From.PortID && exit.Channel == edge.Channel
			}
		}
	}
	toOK := false
	if node := nodeByID(graph, edge.To.NodeID); node != nil {
		projection, ok := instanceProjection(e.projection, *node)
		if ok {
			for _, signal := range projection.Signals {
				toOK = toOK || signal.ID == edge.To.PortID && signal.Direction == "input" && projection.Instruction.AcceptsSignalInput(string(edge.Channel), edge.To.PortID)
			}
		}
	} else if graphCallByID(graph, edge.To.NodeID) != nil {
		toOK = edge.Channel == schema.EdgeExec && edge.To.PortID == graphCallInputPort
	}
	return fromOK && toOK
}

func uniqueBoundaryID(prefix, portID string, index int) string {
	clean := regexp.MustCompile(`[^A-Za-z0-9_-]+`).ReplaceAllString(portID, "_")
	if clean == "" {
		clean = prefix
	}
	return fmt.Sprintf("%s_%s_%d", prefix, clean, index)
}

func filterNodes(nodes []schema.Node, selected map[string]bool) []schema.Node {
	result := nodes[:0]
	for _, node := range nodes {
		if !selected[node.ID] {
			result = append(result, node)
		}
	}
	return result
}

func filterCalls(calls []schema.GraphCall, selected map[string]bool) []schema.GraphCall {
	result := calls[:0]
	for _, call := range calls {
		if !selected[call.ID] {
			result = append(result, call)
		}
	}
	return result
}

func hasStateVariable(source schema.WorkflowSource, name string) bool {
	for _, variable := range source.Variables {
		if variable.Name == name {
			return true
		}
	}
	return false
}

func (e *Engine) stateVariableReferenced(source schema.WorkflowSource, name string) bool {
	for _, graph := range source.Graphs {
		for _, node := range graph.Nodes {
			projection, ok := e.projection.Node(node.NodeRef.NodeTypeID)
			if !ok || projection.NodeRef != node.NodeRef {
				continue
			}
			for _, access := range projection.StateAccesses {
				if selected, ok := node.Config[access.SlotConfigKey].(string); ok && selected == name {
					return true
				}
			}
		}
	}
	return false
}

func configField(projection nodeauthoring.Snapshot, node schema.Node, fieldID string) (nodeauthoring.FieldProjection, bool) {
	projected, ok := projection.Node(node.NodeRef.NodeTypeID)
	if !ok || projected.NodeRef != node.NodeRef {
		return nodeauthoring.FieldProjection{}, false
	}
	for _, field := range projected.ConfigFields {
		if field.ID == fieldID {
			return field, true
		}
	}
	return nodeauthoring.FieldProjection{}, false
}

// Restoring a target removes its override, including a contract default, so
// subsequent changes to the workflow default continue to reach this node.
func inheritsConfigField(source schema.WorkflowSource, snapshot nodeauthoring.Snapshot, node schema.Node, fieldID string) bool {
	projection, ok := snapshot.Node(node.NodeRef.NodeTypeID)
	if !ok || projection.NodeRef != node.NodeRef {
		return false
	}
	for _, target := range projection.ConfiguredTargets {
		if target.SlotConfigKey == fieldID {
			if slot, exists := schema.TargetDefaultSlot(source, target.TargetSlot); exists && slot != "" {
				return true
			}
		}
	}
	for _, target := range projection.Capabilities {
		if target.TargetSlotConfigKey == fieldID {
			if slot, exists := schema.TargetDefaultSlot(source, target.TargetSlot); exists && slot != "" {
				return true
			}
		}
	}
	return false
}

func dataInput(projection nodeauthoring.Snapshot, node schema.Node, portID string) (nodeauthoring.PortProjection, bool) {
	projected, ok := instanceProjection(projection, node)
	if !ok {
		return nodeauthoring.PortProjection{}, false
	}
	for _, port := range projected.DataInputs {
		if port.ID == portID {
			return port, true
		}
	}
	return nodeauthoring.PortProjection{}, false
}

func instanceProjection(snapshot nodeauthoring.Snapshot, node schema.Node) (nodeauthoring.NodeProjection, bool) {
	base, ok := snapshot.Node(node.NodeRef.NodeTypeID)
	if !ok || base.NodeRef != node.NodeRef {
		return nodeauthoring.NodeProjection{}, false
	}
	resolved, err := nodeauthoring.ResolveInstance(base, node.Config)
	return resolved, err == nil
}

// UpgradeDetachedNode advances a persisted node template, such as a Snippet,
// through the same exact migration chain used by Workflow Source. Detached
// nodes have no graph topology, so migrations that would require rewriting an
// edge remain deliberately unsupported by this interface.
func UpgradeDetachedNode(node schema.Node, snapshot nodeauthoring.Snapshot) (schema.Node, bool, error) {
	if !snapshot.Valid() {
		return schema.Node{}, false, errors.New("upgrade detached node requires a valid authoring projection")
	}
	projection, ok := snapshot.Node(node.NodeRef.NodeTypeID)
	if !ok {
		return schema.Node{}, false, fmt.Errorf("node type %q is unavailable in the current catalog", node.NodeRef.NodeTypeID)
	}
	if projection.NodeRef == node.NodeRef {
		return node, false, nil
	}
	migrations, ok := admittedNodeContractUpgradePath(node.NodeRef, projection.NodeRef)
	if !ok {
		return schema.Node{}, false, fmt.Errorf(
			"node contract %s/%s cannot migrate to %s/%s",
			node.NodeRef.Version, node.NodeRef.SemanticDigest,
			projection.NodeRef.Version, projection.NodeRef.SemanticDigest,
		)
	}
	raw, err := json.Marshal(node)
	if err != nil {
		return schema.Node{}, false, fmt.Errorf("clone detached node: %w", err)
	}
	var upgraded schema.Node
	if err := json.Unmarshal(raw, &upgraded); err != nil {
		return schema.Node{}, false, fmt.Errorf("clone detached node: %w", err)
	}
	graph := schema.Graph{}
	for _, migration := range migrations {
		if err := prepareAdmittedNodeContractUpgrade(migration, &graph, &upgraded); err != nil {
			return schema.Node{}, false, fmt.Errorf("prepare node contract migration: %w", err)
		}
	}
	if err := applyCompatibleNodeUpgrade(nil, &graph, &upgraded, projection); err != nil {
		return schema.Node{}, false, fmt.Errorf("apply node contract migration: %w", err)
	}
	return upgraded, true, nil
}

// applyCompatibleNodeUpgrade advances a stale node reference only when the
// current authoring projection can represent all existing authoring data and
// topology. Missing required values remain compiler diagnostics; an incomplete
// node must not keep a stale digest that hides its real repair.
func applyCompatibleNodeUpgrade(_ *schema.WorkflowSource, graph *schema.Graph, node *schema.Node, base nodeauthoring.NodeProjection) error {
	fields := make(map[string]nodeauthoring.FieldProjection, len(base.ConfigFields))
	for _, field := range base.ConfigFields {
		fields[field.ID] = field
	}
	for fieldID := range node.Config {
		if _, ok := fields[fieldID]; !ok {
			return fmt.Errorf("config field %q is unavailable in the current contract", fieldID)
		}
	}
	for _, field := range base.ConfigFields {
		if _, exists := node.Config[field.ID]; exists {
			continue
		}
		if field.HasDefault {
			value, err := decodeRaw(field.Default)
			if err != nil {
				return fmt.Errorf("decode catalog default for config field %q: %w", field.ID, err)
			}
			node.Config[field.ID] = value
			continue
		}
	}

	projection, err := nodeauthoring.ResolveInstance(base, node.Config)
	if err != nil {
		return fmt.Errorf("resolve current node projection: %w", err)
	}
	inputs := make(map[string]nodeauthoring.PortProjection, len(projection.DataInputs))
	for _, port := range projection.DataInputs {
		inputs[port.ID] = port
	}
	for portID := range node.Bindings {
		if _, ok := inputs[portID]; !ok {
			return fmt.Errorf("bound input %q is unavailable in the current contract", portID)
		}
	}

	incoming := make(map[string]bool)
	for _, edge := range graph.Edges {
		if edge.From.NodeID == node.ID && !projectedEndpointExists(projection, edge.From.PortID, edge.Channel, true) {
			return fmt.Errorf("outgoing %s port %q is unavailable in the current contract", edge.Channel, edge.From.PortID)
		}
		if edge.To.NodeID == node.ID {
			if !projectedEndpointExists(projection, edge.To.PortID, edge.Channel, false) {
				return fmt.Errorf("incoming %s port %q is unavailable in the current contract", edge.Channel, edge.To.PortID)
			}
			if edge.Channel == schema.EdgeData {
				incoming[edge.To.PortID] = true
			}
		}
	}
	for _, port := range graph.Inputs {
		if port.NodeID == node.ID {
			if !projectedEndpointExists(projection, port.PortID, schema.EdgeData, false) {
				return fmt.Errorf("graph input port %q is unavailable in the current contract", port.PortID)
			}
			incoming[port.PortID] = true
		}
	}
	for _, port := range graph.Outputs {
		if port.NodeID == node.ID && !projectedEndpointExists(projection, port.PortID, schema.EdgeData, true) {
			return fmt.Errorf("graph output port %q is unavailable in the current contract", port.PortID)
		}
	}
	for _, entry := range graph.Entries {
		if entry.NodeID == node.ID && !projectedEndpointExists(projection, entry.PortID, schema.EdgeExec, false) {
			return fmt.Errorf("graph entry port %q is unavailable in the current contract", entry.PortID)
		}
	}
	for _, exit := range graph.Exits {
		if exit.Endpoint.NodeID == node.ID && !projectedEndpointExists(projection, exit.Endpoint.PortID, exit.Channel, true) {
			return fmt.Errorf("graph exit port %q is unavailable in the current contract", exit.Endpoint.PortID)
		}
	}

	for _, port := range projection.DataInputs {
		if _, bound := node.Bindings[port.ID]; bound || incoming[port.ID] {
			continue
		}
		if port.HasDefault {
			node.Bindings[port.ID] = schema.InputBinding{Kind: schema.BindingDefault}
		}
	}
	node.NodeRef = projection.NodeRef
	return nil
}

func projectedEndpointExists(projection nodeauthoring.NodeProjection, portID string, channel schema.EdgeChannel, output bool) bool {
	if channel == schema.EdgeData {
		ports := projection.DataInputs
		if output {
			ports = projection.DataOutputs
		}
		for _, port := range ports {
			if port.ID == portID {
				return true
			}
		}
		return false
	}
	direction := "input"
	if output {
		direction = "output"
	}
	for _, signal := range projection.Signals {
		if signal.ID == portID && signal.Channel == string(channel) && signal.Direction == direction {
			return true
		}
	}
	return false
}

func pruneInvalidNodeTopology(source *schema.WorkflowSource, graph *schema.Graph, node *schema.Node, snapshot nodeauthoring.Snapshot) {
	projection, ok := instanceProjection(snapshot, *node)
	if !ok || projection.InstanceResolver == nil {
		return
	}
	inputs := make(map[string]bool, len(projection.DataInputs))
	for _, port := range projection.DataInputs {
		inputs[port.ID] = true
	}
	for portID := range node.Bindings {
		if !inputs[portID] {
			delete(node.Bindings, portID)
		}
	}
	edges := graph.Edges[:0]
	for _, edge := range graph.Edges {
		if edge.From.NodeID != node.ID && edge.To.NodeID != node.ID || (&Engine{projection: snapshot}).edgeMatches(source, graph, edge) {
			edges = append(edges, edge)
		}
	}
	graph.Edges = edges
}

func edgeExists(graph schema.Graph, candidate schema.Edge) bool {
	for _, edge := range graph.Edges {
		if sameEdge(edge, candidate) {
			return true
		}
	}
	return false
}

func removeIncomingData(graph *schema.Graph, nodeID, portID string) {
	edges := graph.Edges[:0]
	for _, edge := range graph.Edges {
		if edge.Channel != schema.EdgeData || edge.To.NodeID != nodeID || edge.To.PortID != portID {
			edges = append(edges, edge)
		}
	}
	graph.Edges = edges
}

func resourceByID(resources []schema.WorkflowResource, resourceID string) *schema.WorkflowResource {
	for index := range resources {
		if resources[index].ID == resourceID {
			return &resources[index]
		}
	}
	return nil
}

func normalizeResourceTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, tag)
	}
	sort.Strings(normalized)
	return normalized
}

func countResourceReferences(source schema.WorkflowSource, resourceID string) int {
	count := 0
	countBindings := func(bindings map[string]schema.InputBinding) {
		for _, binding := range bindings {
			if binding.Kind == schema.BindingResource && binding.Resource != nil && binding.Resource.ResourceID == resourceID {
				count++
			}
		}
	}
	for _, graph := range source.Graphs {
		for _, node := range graph.Nodes {
			countBindings(node.Bindings)
		}
		for _, call := range graph.Calls {
			countBindings(call.Bindings)
		}
	}
	return count
}

func finitePosition(position schema.Position) bool {
	return !math.IsNaN(position.X) && !math.IsInf(position.X, 0) && !math.IsNaN(position.Y) && !math.IsInf(position.Y, 0)
}

func (e *Engine) referencesWorkflowTarget(node schema.Node, id string) bool {
	projection, ok := instanceProjection(e.projection, node)
	if !ok { // Preserve references of an unavailable plugin until its contract can be resolved.
		for _, value := range node.Config {
			if slot, ok := value.(string); ok && slot == id {
				return true
			}
		}
		return false
	}
	for _, target := range projection.ConfiguredTargets {
		if slot, _ := node.Config[target.SlotConfigKey].(string); slot == id {
			return true
		}
	}
	return false
}
