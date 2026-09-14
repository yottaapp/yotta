// Package schema defines the current durable Workflow Source contract.
package schema

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodecontract"
)

const (
	Format            = "yotta.workflow"
	Version           = "5"
	SchemaPathVersion = "v" + Version
	MaxRevision       = 9_007_199_254_740_991
	MaxDiagnostics    = 10_000
	MaxVariables      = 4_096
	MaxResources      = 4_096
	MaxCredentials    = 4_096
	MaxDependencies   = 4_096
	MaxGraphDepth     = 32
	MaxGraphPath      = MaxGraphDepth*2 - 1
)

type GraphKind string

const (
	GraphKindMain     GraphKind = "main"
	GraphKindSubgraph GraphKind = "subgraph"
)

type WorkflowSource struct {
	Targets                  []WorkflowTarget          `json:"targets,omitempty" jsonschema:"maxItems=64"`
	ParameterBlocks          []ParameterBlock          `json:"parameterBlocks,omitempty" jsonschema:"maxItems=4096"`
	Format                   string                    `json:"format" jsonschema:"required,enum=yotta.workflow"`
	Version                  string                    `json:"version" jsonschema:"required,enum=5"`
	Workflow                 Workflow                  `json:"workflow" jsonschema:"required"`
	DerivedFrom              *WorkflowReleaseOrigin    `json:"derivedFrom,omitempty"`
	Revision                 int64                     `json:"revision" jsonschema:"required,minimum=0,maximum=9007199254740991"`
	EntryGraph               string                    `json:"entryGraph" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Graphs                   []Graph                   `json:"graphs" jsonschema:"required,minItems=1,maxItems=256"`
	Resources                []WorkflowResource        `json:"resources" jsonschema:"required,maxItems=4096"`
	TargetProfileDefinitions []TargetProfileDefinition `json:"targetProfileDefinitions" jsonschema:"required,maxItems=64"`
	TargetDefaults           []TargetDefault           `json:"targetDefaults,omitempty" jsonschema:"maxItems=64"`
	CredentialRequirements   []CredentialRequirement   `json:"credentialRequirements" jsonschema:"required,maxItems=4096"`
	Dependencies             []NodePackageDependency   `json:"dependencies" jsonschema:"required,maxItems=4096"`
	Variables                []Variable                `json:"variables" jsonschema:"required,maxItems=4096"`
}

// WorkflowReleaseOrigin preserves exact provenance when an immutable Release
// is explicitly copied into a new local editable Source. It contains no
// installation-local configuration or authority.
type WorkflowReleaseOrigin struct {
	ReleaseDigest      artifact.Digest `json:"releaseDigest" jsonschema:"required,pattern=^sha256:[a-f0-9]{64}$"`
	SourceHash         artifact.Digest `json:"sourceHash" jsonschema:"required,pattern=^sha256:[a-f0-9]{64}$"`
	AttestationDigest  artifact.Digest `json:"attestationDigest" jsonschema:"required,pattern=^sha256:[a-f0-9]{64}$"`
	PublisherNamespace string          `json:"publisherNamespace" jsonschema:"required,maxLength=1024"`
	WorkflowID         string          `json:"workflowId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	ReleaseVersion     string          `json:"releaseVersion" jsonschema:"required,maxLength=128"`
}

type ResourceKind string

const (
	ResourceImage     ResourceKind = "image"
	ResourceMacro     ResourceKind = "macro"
	ResourceInputClip ResourceKind = "input-clip"
)

type WorkflowResource struct {
	ID          string             `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Kind        ResourceKind       `json:"kind" jsonschema:"required,enum=image,enum=macro,enum=input-clip"`
	Name        string             `json:"name" jsonschema:"required,minLength=1,maxLength=256"`
	Description string             `json:"description,omitempty" jsonschema:"maxLength=4096"`
	Category    string             `json:"category,omitempty" jsonschema:"maxLength=128"`
	Tags        []string           `json:"tags,omitempty" jsonschema:"maxItems=64"`
	Image       *ImageResource     `json:"image,omitempty"`
	Macro       *MacroResource     `json:"macro,omitempty"`
	InputClip   *InputClipResource `json:"inputClip,omitempty"`
}

type ImageResource struct {
	Variants []ImageResourceVariant `json:"variants" jsonschema:"required,minItems=1,maxItems=256"`
}

type ImageResourceVariant struct {
	ID         string       `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Resolution [2]int       `json:"resolution" jsonschema:"required"`
	BBox       [4]int       `json:"bbox" jsonschema:"required"`
	Regions    [][4]int     `json:"regions,omitempty" jsonschema:"maxItems=256"`
	Blob       blob.BlobRef `json:"blob" jsonschema:"required"`
}

type MacroResource struct {
	Blob           blob.BlobRef `json:"blob" jsonschema:"required"`
	BaseResolution [2]int       `json:"baseResolution" jsonschema:"required"`
	ActionCount    int          `json:"actionCount" jsonschema:"required,minimum=0,maximum=4096"`
	DurationUs     uint64       `json:"durationUs" jsonschema:"required,maximum=3600000000"`
}

type InputClipResource struct {
	Blob           blob.BlobRef `json:"blob" jsonschema:"required"`
	DurationUs     uint64       `json:"durationUs" jsonschema:"required,maximum=3600000000"`
	EventCount     int          `json:"eventCount" jsonschema:"required,minimum=0,maximum=10000000"`
	RecordingMode  string       `json:"recordingMode" jsonschema:"required,enum=simple,enum=precise"`
	MouseMode      string       `json:"mouseMode" jsonschema:"required,enum=relative,enum=absolute,enum=mixed"`
	BaseResolution [2]int       `json:"baseResolution" jsonschema:"required"`
	MouseCounts360 int          `json:"mouseCounts360" jsonschema:"required,minimum=0,maximum=10000000"`
	StopHotkeyVK   uint32       `json:"stopHotkeyVk" jsonschema:"required,maximum=255"`
}

type TargetProfileDefinition struct {
	ID                   string                    `json:"id" jsonschema:"required,maxLength=128,pattern=^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$"`
	Name                 string                    `json:"name" jsonschema:"required,minLength=1,maxLength=256"`
	Description          string                    `json:"description,omitempty" jsonschema:"maxLength=4096"`
	TargetKind           string                    `json:"targetKind" jsonschema:"required,maxLength=128,pattern=^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$"`
	AdapterKind          string                    `json:"adapterKind" jsonschema:"required,maxLength=128,pattern=^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$"`
	ProfileVersion       string                    `json:"profileVersion" jsonschema:"required,maxLength=32,pattern=^[1-9][0-9]*$"`
	SettingsSchemaRoot   string                    `json:"settingsSchemaRoot" jsonschema:"required,maxLength=1024"`
	SettingsSchemaBundle []datatype.SchemaResource `json:"settingsSchemaBundle" jsonschema:"required,minItems=1,maxItems=256"`
	InitialDefaults      json.RawMessage           `json:"initialDefaults" jsonschema:"required"`
	DiscoveryHints       []TargetDiscoveryHint     `json:"discoveryHints" jsonschema:"required,maxItems=64"`
}

type TargetDiscoveryHint struct {
	Kind  string `json:"kind" jsonschema:"required,enum=application-name,enum=executable-name,enum=window-title,enum=android-package,enum=device-model,enum=browser-host"`
	Value string `json:"value" jsonschema:"required,minLength=1,maxLength=512"`
}

type CredentialRequirement struct {
	Slot    string `json:"slot" jsonschema:"required,maxLength=128,pattern=^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$"`
	Kind    string `json:"kind" jsonschema:"required,maxLength=1024"`
	Purpose string `json:"purpose" jsonschema:"required,minLength=1,maxLength=4096"`
}

type NodePackageDependency struct {
	PublisherNamespace string                 `json:"publisherNamespace" jsonschema:"required,maxLength=1024"`
	PackageID          string                 `json:"packageId" jsonschema:"required,maxLength=1024"`
	PackageVersion     string                 `json:"packageVersion" jsonschema:"required,maxLength=128"`
	ManifestDigest     artifact.Digest        `json:"manifestDigest" jsonschema:"required,pattern=^sha256:[a-f0-9]{64}$"`
	NodeRefs           []nodecontract.NodeRef `json:"nodeRefs" jsonschema:"required,minItems=1,maxItems=4096"`
}

type TargetDefault struct {
	Target string `json:"target" jsonschema:"required,maxLength=128,pattern=^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$"`
	Slot   string `json:"slot" jsonschema:"required,maxLength=128,pattern=^[a-z][a-z0-9]*(?:[-_][a-z0-9]+)*$"`
}

type Workflow struct {
	ID          string   `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Name        string   `json:"name" jsonschema:"required,minLength=1,maxLength=256"`
	Description string   `json:"description,omitempty" jsonschema:"maxLength=4096"`
	Category    string   `json:"category,omitempty" jsonschema:"maxLength=128"`
	Tags        []string `json:"tags,omitempty" jsonschema:"maxItems=64"`
	CreatedAt   string   `json:"createdAt,omitempty" jsonschema:"maxLength=64"`
	UpdatedAt   string   `json:"updatedAt,omitempty" jsonschema:"maxLength=64"`
}

func (w Workflow) validateTimestamps() error {
	createdAt, created, err := parseWorkflowTimestamp("createdAt", w.CreatedAt)
	if err != nil {
		return err
	}
	updatedAt, updated, err := parseWorkflowTimestamp("updatedAt", w.UpdatedAt)
	if err != nil {
		return err
	}
	if created && updated && updatedAt.Before(createdAt) {
		return fmt.Errorf("updatedAt must not be before createdAt")
	}
	return nil
}

func parseWorkflowTimestamp(field, value string) (time.Time, bool, error) {
	if value == "" {
		return time.Time{}, false, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%s must be RFC 3339: %w", field, err)
	}
	return parsed, true, nil
}

type Graph struct {
	ID          string       `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Name        string       `json:"name,omitempty" jsonschema:"maxLength=256"`
	Kind        GraphKind    `json:"kind" jsonschema:"required,enum=main,enum=subgraph"`
	Nodes       []Node       `json:"nodes" jsonschema:"required,maxItems=4096"`
	Calls       []GraphCall  `json:"calls,omitempty" jsonschema:"maxItems=4096"`
	Edges       []Edge       `json:"edges" jsonschema:"required,maxItems=16384"`
	Inputs      []GraphPort  `json:"inputs" jsonschema:"required,maxItems=4096"`
	Outputs     []GraphPort  `json:"outputs" jsonschema:"required,maxItems=4096"`
	Entries     []Endpoint   `json:"entries,omitempty" jsonschema:"maxItems=1"`
	Exits       []GraphExit  `json:"exits,omitempty" jsonschema:"maxItems=64"`
	Annotations []Annotation `json:"annotations,omitempty" jsonschema:"maxItems=4096"`
}

type GraphPort struct {
	ID     string                  `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Name   string                  `json:"name,omitempty" jsonschema:"maxLength=256"`
	Type   datatype.TypeExpression `json:"type" jsonschema:"required"`
	NodeID string                  `json:"nodeId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	PortID string                  `json:"portId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
}

type GraphCall struct {
	ID       string                  `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	GraphID  string                  `json:"graphId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Label    string                  `json:"label,omitempty" jsonschema:"maxLength=1024"`
	Position Position                `json:"position" jsonschema:"required"`
	Bindings map[string]InputBinding `json:"bindings" jsonschema:"required"`
}

type GraphExit struct {
	ID       string      `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Name     string      `json:"name,omitempty" jsonschema:"maxLength=256"`
	Channel  EdgeChannel `json:"channel" jsonschema:"required,enum=exec,enum=error"`
	Endpoint Endpoint    `json:"endpoint" jsonschema:"required"`
}

type Annotation struct {
	ID       string   `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Text     string   `json:"text" jsonschema:"required,maxLength=16384"`
	Color    string   `json:"color,omitempty" jsonschema:"maxLength=32"`
	Position Position `json:"position" jsonschema:"required"`
	Size     Size     `json:"size" jsonschema:"required"`
}

type Size struct {
	Width  float64 `json:"width" jsonschema:"required"`
	Height float64 `json:"height" jsonschema:"required"`
}

type BindingKind string

const (
	BindingValue    BindingKind = "value"
	BindingDefault  BindingKind = "default"
	BindingBlob     BindingKind = "blob"
	BindingResource BindingKind = "resource"
)

type InputBinding struct {
	Kind     BindingKind      `json:"kind" jsonschema:"required,enum=value,enum=default,enum=blob,enum=resource"`
	Value    json.RawMessage  `json:"value,omitempty"`
	Blob     *blob.BlobRef    `json:"blob,omitempty"`
	Resource *ResourceBinding `json:"resource,omitempty"`
}

type ResourceBinding struct {
	ResourceID string `json:"resourceId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	VariantID  string `json:"variantId,omitempty" jsonschema:"maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
}

type Node struct {
	ID       string                  `json:"id" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	NodeRef  nodecontract.NodeRef    `json:"nodeRef" jsonschema:"required"`
	Label    string                  `json:"label,omitempty"`
	Position Position                `json:"position" jsonschema:"required"`
	Config   map[string]any          `json:"config" jsonschema:"required"`
	Bindings map[string]InputBinding `json:"bindings" jsonschema:"required"`
	Disabled bool                    `json:"disabled,omitempty"`
}

type Position struct {
	X float64 `json:"x" jsonschema:"required"`
	Y float64 `json:"y" jsonschema:"required"`
}

type EdgeChannel string

const (
	EdgeData  EdgeChannel = "data"
	EdgeExec  EdgeChannel = "exec"
	EdgeError EdgeChannel = "error"
)

type Endpoint struct {
	NodeID string `json:"nodeId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	PortID string `json:"portId" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
}

type Edge struct {
	Channel      EdgeChannel      `json:"channel" jsonschema:"required,enum=data,enum=exec,enum=error"`
	From         Endpoint         `json:"from" jsonschema:"required"`
	To           Endpoint         `json:"to" jsonschema:"required"`
	Presentation EdgePresentation `json:"presentation,omitempty"`
}

type EdgePresentation struct {
	Reroutes []Position `json:"reroutes,omitempty" jsonschema:"maxItems=64"`
}

type Variable struct {
	Parameter *Parameter              `json:"parameter,omitempty"`
	Name      string                  `json:"name" jsonschema:"required,maxLength=128,pattern=^[A-Za-z0-9_][A-Za-z0-9._-]*$"`
	Type      datatype.TypeExpression `json:"type" jsonschema:"required"`
	Default   json.RawMessage         `json:"default" jsonschema:"required"`
}
