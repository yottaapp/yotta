package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	runtimejsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/nodecontract"
)

var workflowValidator = sync.OnceValues(compileWorkflowValidator)

func ParseSource(raw []byte) (WorkflowSource, []Diagnostic) {
	if len(raw) == 0 || !utf8.Valid(raw) || bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": "source must be non-empty UTF-8 without BOM"})
	}
	if err := artifact.InspectJSONBudget(raw, 128, 1_048_576, 1<<20); err != nil {
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": "source exceeds structural budget: " + err.Error()})
	}
	if duplicate, err := firstDuplicateField(raw); err != nil {
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": err.Error()})
	} else if duplicate != nil {
		return WorkflowSource{}, oneDiagnostic(CodeDuplicateField, duplicate, map[string]any{"field": duplicate[len(duplicate)-1]})
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": err.Error()})
	}
	if err := requireEOF(decoder); err != nil {
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": err.Error()})
	}
	envelope, ok := document.(map[string]any)
	if !ok {
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": "source must be a JSON object"})
	}
	var format string
	formatValue, hasFormat := envelope["format"]
	versionValue, hasVersion := envelope["version"]
	format, formatOK := formatValue.(string)
	version, versionOK := versionValue.(string)
	if !hasFormat || !hasVersion || !formatOK || !versionOK || format != Format || version != Version {
		return WorkflowSource{}, oneDiagnostic(CodeUnsupportedWorkflowFormat, nil, map[string]any{"expectedFormat": Format, "expectedVersion": Version})
	}
	if schemaContractViolationBudgetExceeded(document, reflect.TypeFor[WorkflowSource](), MaxDiagnostics) {
		return WorkflowSource{}, oneDiagnostic(CodeDiagnosticBudgetExceeded, nil, map[string]any{"maximum": MaxDiagnostics})
	}

	validator, err := workflowValidator()
	if err != nil {
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": "workflow contract is unavailable: " + err.Error()})
	}
	if err := validator.Validate(document); err != nil {
		var validationError *runtimejsonschema.ValidationError
		if errors.As(err, &validationError) {
			return WorkflowSource{}, diagnosticsFromSchemaError(validationError, document)
		}
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": err.Error()})
	}

	var source WorkflowSource
	if err := decodeValidatedSource(document, &source); err != nil {
		return WorkflowSource{}, oneDiagnostic(CodeInvalidWorkflowJSON, nil, map[string]any{"reason": err.Error()})
	}
	diagnostics := validateSource(source)
	if hasError(diagnostics) {
		return WorkflowSource{}, diagnostics
	}
	return source, diagnostics
}

const sourceDigestDomain = "yotta/workflow-source/v1"

// CanonicalSource validates one editable Workflow Source and returns the
// canonical artifact and identity shared by storage and compilation. A source
// with diagnostics never has an executable identity.
func CanonicalSource(raw []byte) (WorkflowSource, []byte, artifact.Digest, []Diagnostic, error) {
	source, diagnostics := ParseSource(raw)
	if len(diagnostics) != 0 {
		return WorkflowSource{}, nil, "", diagnostics, nil
	}
	canonical, digest, err := CanonicalSourceArtifact(raw)
	if err != nil {
		return WorkflowSource{}, nil, "", nil, err
	}
	return source, canonical, digest, nil, nil
}

// CanonicalSourceArtifact computes the byte identity used by stores and bundle
// manifests without assuming the bytes already use the current Source schema.
// Callers must still migrate and ParseSource before interpreting the document.
func CanonicalSourceArtifact(raw []byte) ([]byte, artifact.Digest, error) {
	canonical, err := artifact.Canonicalize(raw)
	if err != nil {
		return nil, "", fmt.Errorf("canonicalize workflow source: %w", err)
	}
	digest, err := artifact.Sum(sourceDigestDomain, canonical)
	if err != nil {
		return nil, "", err
	}
	return canonical, digest, nil
}

func decodeValidatedSource(document any, source *WorkflowSource) error {
	root := document.(map[string]any)
	canonical := make(map[string]any, len(root))
	for key, value := range root {
		canonical[key] = value
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, source)
}

type structuralViolationCounter struct {
	count, limit int
}

func (c *structuralViolationCounter) add() bool {
	c.count++
	return c.count >= c.limit
}

type reflectedFieldContract struct {
	typ       reflect.Type
	required  bool
	minLength bool
	enum      map[string]bool
}

type reflectedStructContract struct {
	fields map[string]reflectedFieldContract
}

var reflectedContracts sync.Map

func schemaContractViolationBudgetExceeded(document any, typ reflect.Type, limit int) bool {
	counter := &structuralViolationCounter{limit: limit}
	inspectSchemaValue(document, typ, reflectedFieldContract{}, counter)
	return counter.count >= limit
}

func inspectSchemaValue(value any, typ reflect.Type, field reflectedFieldContract, counter *structuralViolationCounter) {
	if counter.count >= counter.limit {
		return
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			counter.add()
			return
		}
		contract := reflectedContract(typ)
		for key := range object {
			if _, ok := contract.fields[key]; !ok && counter.add() {
				return
			}
		}
		for name, child := range contract.fields {
			childValue, exists := object[name]
			if !exists {
				if child.required {
					counter.add()
				}
				continue
			}
			inspectSchemaValue(childValue, child.typ, child, counter)
		}
	case reflect.Slice, reflect.Array:
		values, ok := value.([]any)
		if !ok {
			counter.add()
			return
		}
		for _, child := range values {
			inspectSchemaValue(child, typ.Elem(), reflectedFieldContract{}, counter)
			if counter.count >= counter.limit {
				return
			}
		}
	case reflect.Map:
		if _, ok := value.(map[string]any); !ok {
			counter.add()
		}
	case reflect.Interface:
		return
	case reflect.String:
		text, ok := value.(string)
		if !ok || field.minLength && text == "" || len(field.enum) > 0 && !field.enum[text] {
			counter.add()
		}
	case reflect.Bool:
		if _, ok := value.(bool); !ok {
			counter.add()
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		if _, ok := value.(json.Number); !ok {
			counter.add()
		}
	}
}

func reflectedContract(typ reflect.Type) reflectedStructContract {
	if cached, ok := reflectedContracts.Load(typ); ok {
		return cached.(reflectedStructContract)
	}
	contract := reflectedStructContract{fields: make(map[string]reflectedFieldContract, typ.NumField())}
	for index := 0; index < typ.NumField(); index++ {
		modelField := typ.Field(index)
		name := strings.Split(modelField.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		field := reflectedFieldContract{typ: modelField.Type, enum: map[string]bool{}}
		for _, option := range strings.Split(modelField.Tag.Get("jsonschema"), ",") {
			switch {
			case option == "required":
				field.required = true
			case option == "minLength=1":
				field.minLength = true
			case strings.HasPrefix(option, "enum="):
				field.enum[strings.TrimPrefix(option, "enum=")] = true
			}
		}
		contract.fields[name] = field
	}
	actual, _ := reflectedContracts.LoadOrStore(typ, contract)
	return actual.(reflectedStructContract)
}

func validateSource(source WorkflowSource) []Diagnostic {
	out := validateParameters(source.Variables)
	if err := ValidateWorkflowTargets(source.Targets); err != nil {
		appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"targets"}, map[string]any{"reason": err.Error()}))
	}

	if len(source.Targets) > 0 {
		expected := ""
		for _, target := range source.Targets {
			if target.Default {
				expected = target.ID
			}
		}
		actual := ""
		for _, value := range source.TargetDefaults {
			if value.Target == "target" {
				actual = value.Slot
			}
		}
		for _, value := range source.TargetDefaults {
			if value.Target == "application" && value.Slot != expected {
				appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"targetDefaults"}, map[string]any{"keyword": "workflowTargetDefault"}))
			}
		}
		if expected != "" && actual != expected {
			appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"targetDefaults"}, map[string]any{"keyword": "workflowTargetDefault"}))
		}
	}
	out = append(out, validateParameterBlocks(source)...)
	if err := source.Workflow.validateTimestamps(); err != nil {
		appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"workflow"}, map[string]any{"keyword": "timestamps", "reason": err.Error()}))
	}
	if err := validateReleaseOrigin(source.Workflow.ID, source.DerivedFrom); err != nil {
		appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"derivedFrom"}, map[string]any{"keyword": "workflowReleaseOrigin", "reason": err.Error()}))
	}
	if err := validateResources(source.Resources); err != nil {
		appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"resources"}, map[string]any{"keyword": "workflowResources", "reason": err.Error()}))
	}
	if err := validateTargetProfileDefinitions(source.TargetProfileDefinitions); err != nil {
		appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"targetProfileDefinitions"}, map[string]any{"keyword": "targetProfileDefinitions", "reason": err.Error()}))
	}
	if err := validateTargetDefaults(source.TargetDefaults); err != nil {
		appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"targetDefaults"}, map[string]any{"keyword": "targetDefaults", "reason": err.Error()}))
	}
	if err := validateCredentialRequirements(source.CredentialRequirements); err != nil {
		appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"credentialRequirements"}, map[string]any{"keyword": "credentialRequirements", "reason": err.Error()}))
	}

	graphIDs := map[string]bool{}
	graphKinds := map[string]GraphKind{}
	graphIndexes := map[string]int{}
	callTargets := map[string][]struct {
		graphID string
		call    GraphCall
		path    []string
	}{}
	mainGraphs := 0
	usedNodeRefs := make(map[nodecontract.NodeRef]struct{})
	for graphIndex, graph := range source.Graphs {
		if len(out) >= MaxDiagnostics {
			return sortedDiagnostics(out)
		}
		graphPath := []string{graph.ID}
		base := []string{"graphs", fmt.Sprint(graphIndex)}
		if graphIDs[graph.ID] {
			appendDiagnostic(&out, diagnosticAtGraph(CodeDuplicateID, graphPath, append(base, "id"), map[string]any{"id": graph.ID}))
		}
		graphIDs[graph.ID] = true
		graphKinds[graph.ID] = graph.Kind
		graphIndexes[graph.ID] = graphIndex
		if graph.Kind == GraphKindMain {
			mainGraphs++
		}
		nodeIDs := map[string]bool{}
		for nodeIndex, node := range graph.Nodes {
			if len(out) >= MaxDiagnostics {
				return sortedDiagnostics(out)
			}
			nodePath := append(append([]string(nil), base...), "nodes", fmt.Sprint(nodeIndex))
			if nodeIDs[node.ID] {
				appendDiagnostic(&out, Diagnostic{Code: CodeDuplicateID, Severity: SeverityError, GraphPath: graphPath, NodeID: node.ID, FieldPath: append(nodePath, "id"), Params: map[string]any{"id": node.ID}})
			}
			nodeIDs[node.ID] = true
			usedNodeRefs[node.NodeRef] = struct{}{}
			if !finitePosition(node.Position) {
				appendDiagnostic(&out, Diagnostic{Code: CodeInvalidField, Severity: SeverityError, GraphPath: graphPath, NodeID: node.ID, FieldPath: append(nodePath, "position"), Params: map[string]any{"keyword": "finite"}})
			}
			for portID, binding := range node.Bindings {
				bindingPath := append(append([]string(nil), nodePath...), "bindings", portID)
				validateBindingState(&out, graphPath, node.ID, bindingPath, binding, source.Resources)
			}
		}
		for callIndex, call := range graph.Calls {
			callPath := append(append([]string(nil), base...), "calls", fmt.Sprint(callIndex))
			if nodeIDs[call.ID] {
				appendDiagnostic(&out, Diagnostic{Code: CodeDuplicateID, Severity: SeverityError, GraphPath: graphPath, NodeID: call.ID, FieldPath: append(callPath, "id"), Params: map[string]any{"id": call.ID}})
			}
			nodeIDs[call.ID] = true
			if !finitePosition(call.Position) {
				appendDiagnostic(&out, Diagnostic{Code: CodeInvalidField, Severity: SeverityError, GraphPath: graphPath, NodeID: call.ID, FieldPath: append(callPath, "position"), Params: map[string]any{"keyword": "finite"}})
			}
			for portID, binding := range call.Bindings {
				bindingPath := append(append([]string(nil), callPath...), "bindings", portID)
				validateBindingState(&out, graphPath, call.ID, bindingPath, binding, source.Resources)
			}
			callTargets[graph.ID] = append(callTargets[graph.ID], struct {
				graphID string
				call    GraphCall
				path    []string
			}{graphID: graph.ID, call: call, path: callPath})
		}
		validateGraphPorts := func(ports []GraphPort, field string) {
			portIDs := map[string]bool{}
			for portIndex, port := range ports {
				if len(out) >= MaxDiagnostics {
					return
				}
				portPath := append(append([]string(nil), base...), field, fmt.Sprint(portIndex))
				if portIDs[port.ID] {
					appendDiagnostic(&out, diagnosticAtGraph(CodeDuplicateID, graphPath, append(portPath, "id"), map[string]any{"id": port.ID}))
				}
				if err := port.Type.Validate(); err != nil {
					appendDiagnostic(&out, diagnosticAtGraph(CodeInvalidField, graphPath, append(portPath, "type"), map[string]any{"keyword": "typeExpression", "reason": err.Error()}))
				}
				portIDs[port.ID] = true
			}
		}
		validateGraphPorts(graph.Inputs, "inputs")
		validateGraphPorts(graph.Outputs, "outputs")
		if graph.Kind == GraphKindMain && (len(graph.Inputs) != 0 || len(graph.Outputs) != 0 || len(graph.Entries) != 0 || len(graph.Exits) != 0) {
			appendDiagnostic(&out, diagnosticAtGraph(CodeUnsupportedGraphContract, graphPath, base, map[string]any{"kind": graph.Kind}))
		}
		for entryIndex, entry := range graph.Entries {
			if !nodeIDs[entry.NodeID] {
				appendDiagnostic(&out, diagnosticAtGraph(CodeInvalidGraphBoundaryEdge, graphPath, append(base, "entries", fmt.Sprint(entryIndex)), map[string]any{"nodeId": entry.NodeID}))
			}
		}
		exitIDs := map[string]bool{}
		for exitIndex, exit := range graph.Exits {
			path := append(append([]string(nil), base...), "exits", fmt.Sprint(exitIndex))
			if exitIDs[exit.ID] {
				appendDiagnostic(&out, diagnosticAtGraph(CodeDuplicateID, graphPath, append(path, "id"), map[string]any{"id": exit.ID}))
			}
			exitIDs[exit.ID] = true
			if !nodeIDs[exit.Endpoint.NodeID] {
				appendDiagnostic(&out, diagnosticAtGraph(CodeInvalidGraphBoundaryEdge, graphPath, append(path, "endpoint"), map[string]any{"nodeId": exit.Endpoint.NodeID}))
			}
		}
		annotationIDs := map[string]bool{}
		for annotationIndex, annotation := range graph.Annotations {
			path := append(append([]string(nil), base...), "annotations", fmt.Sprint(annotationIndex))
			if annotationIDs[annotation.ID] || nodeIDs[annotation.ID] {
				appendDiagnostic(&out, diagnosticAtGraph(CodeDuplicateID, graphPath, append(path, "id"), map[string]any{"id": annotation.ID}))
			}
			annotationIDs[annotation.ID] = true
			if !finitePosition(annotation.Position) || math.IsNaN(annotation.Size.Width) || math.IsInf(annotation.Size.Width, 0) || math.IsNaN(annotation.Size.Height) || math.IsInf(annotation.Size.Height, 0) || annotation.Size.Width < 80 || annotation.Size.Height < 48 {
				appendDiagnostic(&out, diagnosticAtGraph(CodeInvalidField, graphPath, path, map[string]any{"keyword": "annotationGeometry"}))
			}
		}
		for edgeIndex, edge := range graph.Edges {
			for rerouteIndex, reroute := range edge.Presentation.Reroutes {
				if !finitePosition(reroute) {
					appendDiagnostic(&out, diagnosticAtGraph(CodeInvalidField, graphPath, append(base, "edges", fmt.Sprint(edgeIndex), "presentation", "reroutes", fmt.Sprint(rerouteIndex)), map[string]any{"keyword": "finite"}))
				}
			}
		}
		if len(out) >= MaxDiagnostics {
			return sortedDiagnostics(out)
		}
	}
	if !graphIDs[source.EntryGraph] || graphKinds[source.EntryGraph] != GraphKindMain || mainGraphs != 1 {
		appendDiagnostic(&out, diagnostic(CodeMissingEntryGraph, []string{"entryGraph"}, map[string]any{"entryGraph": source.EntryGraph, "mainGraphs": mainGraphs}))
	}
	for graphID, calls := range callTargets {
		for _, reference := range calls {
			if !graphIDs[reference.call.GraphID] {
				appendDiagnostic(&out, Diagnostic{Code: CodeUnknownCalleeGraph, Severity: SeverityError, GraphPath: []string{graphID}, NodeID: reference.call.ID, FieldPath: append(reference.path, "graphId"), Params: map[string]any{"graphId": reference.call.GraphID}})
			} else if graphKinds[reference.call.GraphID] != GraphKindSubgraph {
				appendDiagnostic(&out, Diagnostic{Code: CodeInvalidCalleeGraphKind, Severity: SeverityError, GraphPath: []string{graphID}, NodeID: reference.call.ID, FieldPath: append(reference.path, "graphId"), Params: map[string]any{"graphId": reference.call.GraphID}})
			}
		}
	}
	state := map[string]uint8{}
	depths := map[string]int{}
	var visitCalls func(string) int
	visitCalls = func(graphID string) int {
		state[graphID] = 1
		maximumDepth := 1
		for _, reference := range callTargets[graphID] {
			if !graphIDs[reference.call.GraphID] || graphKinds[reference.call.GraphID] != GraphKindSubgraph {
				continue
			}
			if state[reference.call.GraphID] == 1 {
				appendDiagnostic(&out, Diagnostic{Code: CodeSubgraphCallCycle, Severity: SeverityError, GraphPath: []string{graphID}, NodeID: reference.call.ID, FieldPath: append(reference.path, "graphId"), Params: map[string]any{"graphId": reference.call.GraphID, "maximumDepth": MaxGraphDepth}})
				continue
			}
			childDepth := depths[reference.call.GraphID]
			if state[reference.call.GraphID] == 0 {
				childDepth = visitCalls(reference.call.GraphID)
			}
			if childDepth+1 > MaxGraphDepth {
				appendDiagnostic(&out, Diagnostic{Code: CodeSubgraphCallCycle, Severity: SeverityError, GraphPath: []string{graphID}, NodeID: reference.call.ID, FieldPath: append(reference.path, "graphId"), Params: map[string]any{"graphId": reference.call.GraphID, "maximumDepth": MaxGraphDepth}})
			}
			maximumDepth = max(maximumDepth, childDepth+1)
		}
		state[graphID] = 2
		depths[graphID] = maximumDepth
		return maximumDepth
	}
	for graphID := range graphIndexes {
		if state[graphID] == 0 {
			visitCalls(graphID)
		}
	}
	if err := validateDependencies(source.Dependencies, usedNodeRefs); err != nil {
		appendDiagnostic(&out, diagnostic(CodeInvalidField, []string{"dependencies"}, map[string]any{"keyword": "nodePackageDependencies", "reason": err.Error()}))
	}
	variableNames := map[string]bool{}
	for index, variable := range source.Variables {
		if len(out) >= MaxDiagnostics {
			return sortedDiagnostics(out)
		}
		path := []string{"variables", fmt.Sprint(index)}
		if variableNames[variable.Name] {
			appendDiagnostic(&out, diagnostic(CodeDuplicateID, append(path, "name"), map[string]any{"id": variable.Name}))
		}
		if err := variable.Type.Validate(); err != nil {
			appendDiagnostic(&out, diagnostic(CodeInvalidField, append(path, "type"), map[string]any{"keyword": "typeExpression", "reason": err.Error()}))
		}
		variableNames[variable.Name] = true
	}
	return sortedDiagnostics(out)
}

func validateBindingState(out *[]Diagnostic, graphPath []string, nodeID string, bindingPath []string, binding InputBinding, resources []WorkflowResource) {
	field := "value"
	invalid := false
	switch binding.Kind {
	case BindingValue:
		invalid = len(binding.Value) == 0 || binding.Blob != nil || binding.Resource != nil
	case BindingDefault:
		invalid = len(binding.Value) != 0 || binding.Blob != nil || binding.Resource != nil
	case BindingBlob:
		field = "blob"
		invalid = len(binding.Value) != 0 || binding.Blob == nil || binding.Resource != nil || binding.Blob.Validate() != nil
	case BindingResource:
		field = "resource"
		if binding.Resource == nil || len(binding.Value) != 0 || binding.Blob != nil {
			invalid = true
		} else if _, err := ResolveResourceBinding(resources, *binding.Resource); err != nil {
			invalid = true
		}
	}
	if invalid {
		appendDiagnostic(out, Diagnostic{Code: CodeInvalidField, Severity: SeverityError, GraphPath: graphPath, NodeID: nodeID, FieldPath: append(bindingPath, field), Params: map[string]any{"keyword": "bindingState"}})
	}
}

func finitePosition(position Position) bool {
	return !math.IsNaN(position.X) && !math.IsInf(position.X, 0) && !math.IsNaN(position.Y) && !math.IsInf(position.Y, 0)
}

func sortedDiagnostics(values []Diagnostic) []Diagnostic {
	sortDiagnostics(values)
	return values
}

func compileWorkflowValidator() (*runtimejsonschema.Schema, error) {
	raw, err := GenerateContract("workflow")
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	compiler := runtimejsonschema.NewCompiler()
	if err := compiler.AddResource(workflowSchemaID, document); err != nil {
		return nil, err
	}
	return compiler.Compile(workflowSchemaID)
}

func diagnosticsFromSchemaError(validationError *runtimejsonschema.ValidationError, document any) []Diagnostic {
	var out []Diagnostic
	var visit func(*runtimejsonschema.ValidationError)
	visit = func(current *runtimejsonschema.ValidationError) {
		if len(out) >= MaxDiagnostics {
			return
		}
		if len(current.Causes) > 0 {
			for _, cause := range current.Causes {
				visit(cause)
				if len(out) >= MaxDiagnostics {
					return
				}
			}
			return
		}
		base := append([]string(nil), current.InstanceLocation...)
		keyword := strings.Join(current.ErrorKind.KeywordPath(), "/")
		switch typed := current.ErrorKind.(type) {
		case *kind.Required:
			for _, field := range typed.Missing {
				appendDiagnostic(&out, schemaDiagnostic(CodeMissingRequiredField, appendPath(base, field), keyword, document))
			}
		case *kind.AdditionalProperties:
			for _, field := range typed.Properties {
				appendDiagnostic(&out, schemaDiagnostic(CodeUnknownField, appendPath(base, field), keyword, document))
			}
		default:
			appendDiagnostic(&out, schemaDiagnostic(CodeInvalidField, base, keyword, document))
		}
	}
	visit(validationError)
	sortDiagnostics(out)
	return out
}

func schemaDiagnostic(code string, fieldPath []string, keyword string, document any) Diagnostic {
	d := diagnostic(code, fieldPath, map[string]any{"keyword": keyword})
	d.GraphPath, d.NodeID = locationContext(document, fieldPath)
	return d
}

func locationContext(document any, fieldPath []string) ([]string, string) {
	root, _ := document.(map[string]any)
	graphs, _ := root["graphs"].([]any)
	if len(fieldPath) < 2 || fieldPath[0] != "graphs" {
		return nil, ""
	}
	graphIndex, err := strconv.Atoi(fieldPath[1])
	if err != nil || graphIndex < 0 || graphIndex >= len(graphs) {
		return nil, ""
	}
	graph, _ := graphs[graphIndex].(map[string]any)
	graphID, _ := graph["id"].(string)
	graphPath := []string{graphID}
	if len(fieldPath) < 4 || fieldPath[2] != "nodes" {
		return graphPath, ""
	}
	nodes, _ := graph["nodes"].([]any)
	nodeIndex, err := strconv.Atoi(fieldPath[3])
	if err != nil || nodeIndex < 0 || nodeIndex >= len(nodes) {
		return graphPath, ""
	}
	node, _ := nodes[nodeIndex].(map[string]any)
	nodeID, _ := node["id"].(string)
	return graphPath, nodeID
}

func appendPath(path []string, field string) []string {
	return append(append([]string(nil), path...), field)
}

func firstDuplicateField(raw []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	duplicate, err := scanJSONValue(decoder, nil)
	if err != nil {
		return nil, err
	}
	if duplicate != nil {
		return duplicate, nil
	}
	if err := requireEOF(decoder); err != nil {
		return nil, err
	}
	return nil, nil
}

func scanJSONValue(decoder *json.Decoder, path []string) ([]string, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil, nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key is not a string")
			}
			keyPath := append(append([]string(nil), path...), key)
			if seen[key] {
				return keyPath, nil
			}
			seen[key] = true
			if duplicate, err := scanJSONValue(decoder, keyPath); err != nil || duplicate != nil {
				return duplicate, err
			}
		}
		_, err = decoder.Token()
		return nil, err
	case '[':
		index := 0
		for decoder.More() {
			itemPath := append(append([]string(nil), path...), fmt.Sprint(index))
			if duplicate, err := scanJSONValue(decoder, itemPath); err != nil || duplicate != nil {
				return duplicate, err
			}
			index++
		}
		_, err = decoder.Token()
		return nil, err
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func requireEOF(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func diagnostic(code string, fieldPath []string, params map[string]any) Diagnostic {
	return Diagnostic{Code: code, Severity: SeverityError, FieldPath: fieldPath, Params: params}
}

func diagnosticAtGraph(code string, graphPath, fieldPath []string, params map[string]any) Diagnostic {
	d := diagnostic(code, fieldPath, params)
	d.GraphPath = graphPath
	return d
}

func oneDiagnostic(code string, fieldPath []string, params map[string]any) []Diagnostic {
	return []Diagnostic{diagnostic(code, fieldPath, params)}
}

func appendDiagnostic(out *[]Diagnostic, value Diagnostic) {
	if len(*out) < MaxDiagnostics-1 {
		*out = append(*out, value)
		return
	}
	if len(*out) == MaxDiagnostics-1 {
		*out = append(*out, diagnostic(CodeDiagnosticBudgetExceeded, nil, map[string]any{"maximum": MaxDiagnostics}))
	}
}

func hasError(diagnostics []Diagnostic) bool {
	return slices.ContainsFunc(diagnostics, func(d Diagnostic) bool { return d.Severity == SeverityError })
}

func sortDiagnostics(diagnostics []Diagnostic) {
	sortable := diagnostics
	if len(diagnostics) > 0 && diagnostics[len(diagnostics)-1].Code == CodeDiagnosticBudgetExceeded {
		sortable = diagnostics[:len(diagnostics)-1]
	}
	sort.SliceStable(sortable, func(i, j int) bool {
		left, right := sortable[i], sortable[j]
		for _, pair := range [][2]string{{strings.Join(left.GraphPath, "/"), strings.Join(right.GraphPath, "/")}, {left.NodeID, right.NodeID}, {strings.Join(left.FieldPath, "/"), strings.Join(right.FieldPath, "/")}, {left.Code, right.Code}} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		return false
	})
}
