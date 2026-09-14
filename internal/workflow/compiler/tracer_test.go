package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestConcatTracerCompilesOpensAndRunsWithoutExecOut(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	build := testDigest(t, "compiler")
	result, err := New(build, testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: concatSourceForTest(contract.NodeRef(), "hello", " world", nil), Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	program, ok := result.Program()
	if !ok {
		t.Fatal("missing program")
	}
	if plan := program.CapabilityPlan(); !plan.Valid() || len(plan.Entries()) != 0 {
		t.Fatalf("concat capability plan = %#v", plan.Entries())
	}
	nodeViews := program.Nodes()
	if len(nodeViews) != 1 || len(nodeViews[0].Ports.DataInputs) != 2 || len(nodeViews[0].Ports.DataOutputs) != 1 ||
		len(nodeViews[0].Ports.ExecInputs)+len(nodeViews[0].Ports.ExecOutputs)+len(nodeViews[0].Ports.ErrorOutputs) != 0 {
		t.Fatalf("program ports = %#v", nodeViews)
	}
	opened, err := OpenProgram(program.Artifact(), catalog, testConfigValidators(), build)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := catalog.Lookup(nodes.ConcatNodeID)
	run, err := NewInterpreter(catalog, map[string]InstalledBuiltin{
		"text.concat": {Implementation: entry.Implementation, Run: nodes.Concat},
	}).Run(context.Background(), opened)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := datatype.OpenValueEnvelope(catalog, run.NodeOutputs["concat-1"]["result"])
	if err != nil {
		t.Fatal(err)
	}
	if got := string(envelope.InlineJSON()); got != `"hello world"` {
		t.Fatalf("concat result = %s", got)
	}
}

func TestConcatTracerRejectsInventedOutAndContractMismatch(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	build := testDigest(t, "compiler")
	outEdge := `{"channel":"exec","from":{"nodeId":"concat-1","portId":"out"},"to":{"nodeId":"concat-1","portId":"in"}}`
	result, err := New(build, testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: concatSourceForTest(contract.NodeRef(), "a", "b", &outEdge), Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, CodeUnknownPort) {
		t.Fatalf("invented out diagnostics = %#v", result.Diagnostics)
	}
	var unknownPort Diagnostic
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == CodeUnknownPort {
			unknownPort = diagnostic
			break
		}
	}
	if unknownPort.NodeID != "concat-1" ||
		unknownPort.Params["fromNodeId"] != "concat-1" || unknownPort.Params["fromPortId"] != "out" ||
		unknownPort.Params["toNodeId"] != "concat-1" || unknownPort.Params["toPortId"] != "in" {
		t.Fatalf("unknown port omitted actionable edge endpoints = %#v", unknownPort)
	}

	ref := contract.NodeRef()
	ref.SemanticDigest = artifact.Digest("sha256:" + strings.Repeat("0", 64))
	result, err = New(build, testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: concatSourceForTest(ref, "a", "b", &outEdge), Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, CodeNodeContractMismatch) {
		t.Fatalf("contract mismatch diagnostics = %#v", result.Diagnostics)
	}
	if hasDiagnostic(result.Diagnostics, CodeUnknownPort) {
		t.Fatalf("contract mismatch produced derivative unknown-port diagnostics = %#v", result.Diagnostics)
	}
}

func TestCompilerLowersExecAndErrorEdgesIntoOrderedSignalRoutes(t *testing.T) {
	catalog, source, target := signalCatalogForTest(t)
	build := testDigest(t, "signal-compiler")
	raw := signalSourceForTest(source.NodeRef(), target.NodeRef(), []string{
		`{"channel":"exec","from":{"nodeId":"source","portId":"next"},"to":{"nodeId":"target","portId":"in"}}`,
		`{"channel":"error","from":{"nodeId":"source","portId":"failed"},"to":{"nodeId":"target","portId":"in"}}`,
	})
	compiled, err := New(build, testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: raw, Catalog: catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("missing signal Program")
	}
	var document programDocument
	if err := json.Unmarshal(program.Artifact(), &document); err != nil {
		t.Fatal(err)
	}
	graph := document.Body.Graphs[0]
	if len(graph.SignalRoutes) != 2 || graph.SignalRoutes[0].Channel != schema.EdgeExec || graph.SignalRoutes[1].Channel != schema.EdgeError {
		t.Fatalf("signal routes = %#v", graph.SignalRoutes)
	}
	if !slices.Equal(graph.DataOrder, []string{"source", "target"}) {
		t.Fatalf("data order = %#v", graph.DataOrder)
	}
	if _, err := OpenProgram(program.Artifact(), catalog, testConfigValidators(), build); err != nil {
		t.Fatalf("strict-open signal Program: %v", err)
	}

	document.Body.Graphs[0].SignalRoutes[0].To.PortID = "missing"
	forged, err := sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), catalog, testConfigValidators(), build); err == nil {
		t.Fatal("accepted a forged signal route")
	}

	document.Body.Graphs[0].SignalRoutes[0].To.PortID = "in"
	slices.Reverse(document.Body.Graphs[0].DataOrder)
	forged, err = sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), catalog, testConfigValidators(), build); err == nil {
		t.Fatal("accepted a forged data order")
	}
}

func TestCompilerRejectsDuplicateSignalRoutesAndRootlessControlCycles(t *testing.T) {
	catalog, source, target := signalCatalogForTest(t)
	duplicate := `{"channel":"exec","from":{"nodeId":"source","portId":"next"},"to":{"nodeId":"target","portId":"in"}}`
	compiled, err := New(testDigest(t, "duplicate-route"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: signalSourceForTest(source.NodeRef(), target.NodeRef(), []string{duplicate, duplicate}), Catalog: catalog,
	})
	if err != nil || !hasDiagnostic(compiled.Diagnostics, CodeDuplicateSignalRoute) {
		t.Fatalf("duplicate route diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}

	cycleCatalog, left, right := signalCycleCatalogForTest(t)
	cycle := signalSourceForTest(left.NodeRef(), right.NodeRef(), []string{
		`{"channel":"exec","from":{"nodeId":"source","portId":"next"},"to":{"nodeId":"target","portId":"in"}}`,
		`{"channel":"exec","from":{"nodeId":"target","portId":"next"},"to":{"nodeId":"source","portId":"in"}}`,
	})
	compiled, err = New(testDigest(t, "control-cycle"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: cycle, Catalog: cycleCatalog})
	if err != nil || !hasDiagnostic(compiled.Diagnostics, CodeNoExecutionRoot) {
		t.Fatalf("control cycle diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
}

func TestCompilerDistinguishesWrongChannelFromUnknownPort(t *testing.T) {
	catalog, source, target := signalCatalogForTest(t)
	compiled, err := New(testDigest(t, "channel-mismatch"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: signalSourceForTest(source.NodeRef(), target.NodeRef(), []string{
			`{"channel":"error","from":{"nodeId":"source","portId":"next"},"to":{"nodeId":"target","portId":"in"}}`,
		}), Catalog: catalog,
	})
	if err != nil || !hasDiagnostic(compiled.Diagnostics, CodeEdgeChannelMismatch) {
		t.Fatalf("channel mismatch diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
}

func TestConcatTracerFreezesTypedDataEdgesIndependentOfSourceOrder(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	ref := contract.NodeRef()
	raw := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-2","name":"Chain"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"second","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},"bindings":{"b":{"kind":"value","value":"c"}}},
			{"id":"first","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{"a":{"kind":"value","value":"a"},"b":{"kind":"value","value":"b"}}}
		],"edges":[{"channel":"data","from":{"nodeId":"first","portId":"result"},"to":{"nodeId":"second","portId":"a"}}],"inputs":[],"outputs":[]}],
		"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest, ref.NodeTypeID, ref.SemanticDigest))
	result, err := New(testDigest(t, "compiler"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: raw, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	program, ok := result.Program()
	if !ok {
		t.Fatal("missing data-edge Program")
	}
	var document programDocument
	if err := json.Unmarshal(program.Artifact(), &document); err != nil {
		t.Fatal(err)
	}
	if got := document.Body.Graphs[0].Nodes[0].Inputs["a"]; got.Kind != inputEdge || got.From.NodeID != "first" || got.From.PortID != "result" {
		t.Fatalf("frozen data input = %#v", got)
	}
}

func TestOpenProgramRevalidatesConfigAndLiteralBindings(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	build := testDigest(t, "compiler")
	compiled, err := New(build, testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: concatSourceForTest(contract.NodeRef(), "a", "b", nil), Catalog: catalog,
	})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, _ := compiled.Program()

	var document programDocument
	if err := json.Unmarshal(program.Artifact(), &document); err != nil {
		t.Fatal(err)
	}
	document.Body.Graphs[0].Nodes[0].Config["unexpected"] = true
	forged, err := sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), catalog, testConfigValidators(), build); err == nil {
		t.Fatal("accepted rehashed program with config outside the Node Contract schema")
	}

	document.Body.Graphs[0].Nodes[0].Config = map[string]any{}
	document.Body.Graphs[0].Nodes[0].Inputs["a"] = inputPlan{Kind: inputLiteral, Value: json.RawMessage(`1`)}
	forged, err = sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), catalog, testConfigValidators(), build); err == nil {
		t.Fatal("accepted rehashed program with a literal violating the pinned Data Type")
	}

	if err := json.Unmarshal(program.Artifact(), &document); err != nil {
		t.Fatal(err)
	}
	number, ok := catalog.LookupType(nodes.NumberTypeID)
	if !ok {
		t.Fatal("number type is missing")
	}
	document.Body.Graphs[0].Nodes[0].OutputTypes["result"] = datatype.RefResolvedType(number.TypeRef())
	forged, err = sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), catalog, testConfigValidators(), build); err == nil {
		t.Fatal("accepted rehashed Program with an effective type outside the Node Contract")
	}
}

func TestOpenProgramRevalidatesPinnedConfigValidator(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	started, ok := builtins.Definition(nodes.RunStartedNodeID)
	if !ok {
		t.Fatal("RunStarted definition is missing")
	}
	extract := builtins.AIExtractContract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-ai-validator","name":"AI Validator"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"extract","nodeRef":{"nodeTypeId":%q,"version":%q,"semanticDigest":%q},"position":{"x":1,"y":0},
			 "config":{"slot":"default","timeoutMilliseconds":120000,"fields":[{"name":"value","type":"string"}]},"bindings":{"prompt":{"kind":"value","value":"hello"}}}
		],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"extract","portId":"in"}}],
		"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, started.Contract.NodeRef().NodeTypeID, started.Contract.NodeRef().Version, started.Contract.NodeRef().SemanticDigest,
		extract.NodeTypeID, extract.Version, extract.SemanticDigest))
	build := testDigest(t, "AI config validator")
	compiled, err := New(build, builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: source, Catalog: builtins.Catalog,
	})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("missing AI Extract Program")
	}
	var document programDocument
	if err := json.Unmarshal(program.Artifact(), &document); err != nil {
		t.Fatal(err)
	}
	for index := range document.Body.Graphs[0].Nodes {
		if document.Body.Graphs[0].Nodes[index].ID == "extract" {
			document.Body.Graphs[0].Nodes[index].Config["fields"] = []any{
				map[string]any{"name": "value", "type": "string"},
				map[string]any{"name": "value", "type": "number"},
			}
		}
	}
	forged, err := sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), builtins.Catalog, builtins.ConfigValidators, build); err == nil {
		t.Fatal("accepted a rehashed Program whose config violates the pinned validator")
	}
}

func TestCompileResolvesDynamicSwitchPortsIntoProgram(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	started, _ := builtins.Definition(nodes.RunStartedNodeID)
	switchDefinition, _ := builtins.Definition(nodes.SwitchNodeID)
	concatDefinition, _ := builtins.Definition(nodes.ConcatNodeID)
	startRef := started.Contract.NodeRef()
	switchRef := switchDefinition.Contract.NodeRef()
	concatRef := concatDefinition.Contract.NodeRef()
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-switch","name":"Switch"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"start","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"concat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":1},"config":{},"bindings":{"a":{"kind":"value","value":"be"},"b":{"kind":"value","value":"ta"}}},
			{"id":"switch","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"caseCount":2},"bindings":{"case-1":{"kind":"value","value":"alpha"},"case-2":{"kind":"value","value":"beta"}}}
		],"edges":[
			{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"switch","portId":"in"}},
			{"channel":"data","from":{"nodeId":"concat","portId":"result"},"to":{"nodeId":"switch","portId":"value"}}
		],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, startRef.NodeTypeID, startRef.SemanticDigest, concatRef.NodeTypeID, concatRef.SemanticDigest, switchRef.NodeTypeID, switchRef.SemanticDigest))
	compiled, err := New(testDigest(t, "dynamic-switch"), builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: source, Catalog: builtins.Catalog,
	})
	if err != nil || schema.HasErrors(compiled.Diagnostics) {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, ok := compiled.Program()
	if !ok {
		t.Fatal("dynamic switch Program is missing")
	}
	var document programDocument
	if err := json.Unmarshal(program.Artifact(), &document); err != nil {
		t.Fatal(err)
	}
	var switchNode programNode
	for _, node := range document.Body.Graphs[0].Nodes {
		if node.ID == "switch" {
			switchNode = node
		}
	}
	if len(switchNode.Ports.DataInputs) != 3 {
		t.Fatalf("effective switch ports = %#v", switchNode.Ports)
	}
	if _, err := OpenProgram(program.Artifact(), builtins.Catalog, builtins.ConfigValidators, testDigest(t, "dynamic-switch")); err != nil {
		t.Fatalf("strict-open dynamic Switch Program: %v", err)
	}
}

func TestProgramNodeViewsAreDefensive(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	compiled, err := New(testDigest(t, "compiler"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: concatSourceForTest(contract.NodeRef(), "a", "b", nil), Catalog: catalog,
	})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, _ := compiled.Program()
	view := program.Nodes()
	view[0].Ports.DataInputs[0].ID = "mutated"
	delete(view[0].InputTypes, "a")
	if got := program.Nodes()[0].Ports.DataInputs[0].ID; got != "a" {
		t.Fatalf("caller mutated immutable program view: %q", got)
	}
	if got := program.Nodes()[0].InputTypes["a"]; got.Ref == nil || got.Ref.TypeID != nodes.StringTypeID {
		t.Fatalf("caller mutated immutable effective type view: %#v", got)
	}
}

func TestInterpreterRejectsUnpinnedBuiltinAndIllTypedOutput(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	compiled, err := New(testDigest(t, "compiler"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: concatSourceForTest(contract.NodeRef(), "a", "b", nil), Catalog: catalog,
	})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, _ := compiled.Program()
	entry, _ := catalog.Lookup(nodes.ConcatNodeID)
	wrong := entry.Implementation
	wrong.ArtifactDigest = testDigest(t, "wrong implementation")
	if _, err := NewInterpreter(catalog, map[string]InstalledBuiltin{
		"text.concat": {Implementation: wrong, Run: nodes.Concat},
	}).Run(context.Background(), program); err == nil {
		t.Fatal("interpreter dispatched a builtin that did not match the Program lock")
	}
	badOutput := func(context.Context, map[string]json.RawMessage) (map[string]json.RawMessage, error) {
		return map[string]json.RawMessage{"result": json.RawMessage(`1`)}, nil
	}
	if _, err := NewInterpreter(catalog, map[string]InstalledBuiltin{
		"text.concat": {Implementation: entry.Implementation, Run: badOutput},
	}).Run(context.Background(), program); err == nil {
		t.Fatal("interpreter accepted output outside the pinned Data Type")
	}
}

func TestCompilerFailsClosedForDisabledNodes(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	raw := bytes.Replace(concatSourceForTest(contract.NodeRef(), "a", "b", nil), []byte(`"config":{}`), []byte(`"config":{},"disabled":true`), 1)
	result, err := New(testDigest(t, "compiler"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: raw, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, CodeUnsupportedSourceFeature) {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestCompilerFreezesConcreteTypedStateAndStrictOpenRevalidatesInitialValues(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	ref := builtins.ConcatContract.NodeRef()
	typeRef := builtins.StringType.TypeRef()
	raw := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-state","name":"State"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"concat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			"bindings":{"a":{"kind":"value","value":"a"},"b":{"kind":"value","value":"b"}}
		}],"edges":[],"inputs":[],"outputs":[]}],
		"variables":[{"name":"message","type":{"kind":"ref","ref":{"typeId":%q,"semanticDigest":%q}},"default":"ready"}],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest, typeRef.TypeID, typeRef.SemanticDigest))
	build := testDigest(t, "compiler-state")
	compiled, err := New(build, builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{SourceJSON: raw, Catalog: builtins.Catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile=%v diagnostics=%#v", err, compiled.Diagnostics)
	}
	program, ok := compiled.Program()
	if !ok || len(program.State()) != 1 || program.State()[0].Name != "message" || !reflect.DeepEqual(program.State()[0].Type, datatype.RefResolvedType(typeRef)) {
		t.Fatalf("program state = %#v", program.State())
	}
	initial, err := datatype.OpenValueEnvelope(builtins.Catalog, program.State()[0].InitialArtifact)
	if err != nil || string(initial.InlineJSON()) != `"ready"` {
		t.Fatalf("initial=%s err=%v", initial.InlineJSON(), err)
	}
	if _, err := OpenProgram(program.Artifact(), builtins.Catalog, builtins.ConfigValidators, build); err != nil {
		t.Fatalf("strict-open state Program: %v", err)
	}
	view := program.State()
	view[0].Type.Ref.TypeID = "https://attacker.invalid/types/forged/v1"
	view[0].InitialArtifact[0] = '['
	if program.State()[0].Type.Ref.TypeID != typeRef.TypeID || program.State()[0].InitialArtifact[0] != '{' {
		t.Fatal("Program State view leaked mutable snapshot storage")
	}

	var document programDocument
	if err := json.Unmarshal(program.Artifact(), &document); err != nil {
		t.Fatal(err)
	}
	wrong, err := datatype.SealInlineJSON(builtins.Catalog, datatype.RefResolvedType(builtins.BooleanType.TypeRef()), []byte(`true`))
	if err != nil {
		t.Fatal(err)
	}
	document.Body.State[0].Initial = wrong.Artifact()
	forged, err := sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), builtins.Catalog, builtins.ConfigValidators, build); err == nil {
		t.Fatal("strict opener accepted a state initial value with a forged type")
	}
}

func TestCompilerRejectsUnresolvedUnknownAndNonInlineStateDeclarations(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	ref := builtins.ConcatContract.NodeRef()
	base := fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-state-invalid","name":"State invalid"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"concat","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			"bindings":{"a":{"kind":"value","value":"a"},"b":{"kind":"value","value":"b"}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[%s],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest, "%s")
	unknown := artifact.Digest("sha256:" + strings.Repeat("2", 64))
	tests := []string{
		`{"name":"generic","type":{"kind":"variable","variable":"T"},"default":null}`,
		fmt.Sprintf(`{"name":"unknown","type":{"kind":"ref","ref":{"typeId":"https://schemas.yotta.dev/types/unknown/v1","semanticDigest":%q}},"default":null}`, unknown),
		fmt.Sprintf(`{"name":"binary","type":{"kind":"ref","ref":{"typeId":%q,"semanticDigest":%q}},"default":null}`, builtins.BinaryType.TypeRef().TypeID, builtins.BinaryType.TypeRef().SemanticDigest),
	}
	for index, declaration := range tests {
		compiled, err := New(testDigest(t, fmt.Sprintf("invalid-state-%d", index)), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
			SourceJSON: []byte(fmt.Sprintf(base, declaration)), Catalog: builtins.Catalog,
		})
		if err != nil || !hasDiagnostic(compiled.Diagnostics, CodeInvalidStateVariable) {
			t.Fatalf("case %d compile=%v diagnostics=%#v", index, err, compiled.Diagnostics)
		}
	}
}

func TestBlobStreamConversionTracerCompilesExactEffectPlanAndStaysOutOfPreview(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	build := testDigest(t, "compiler-conversion")
	blobRef := blob.BlobRef{MediaType: "application/octet-stream", Digest: testDigest(t, "blob-value"), Size: 4}
	result, err := New(build, builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: conversionSourceForTest(builtins.BlobToStreamContract.NodeRef(), builtins.StreamToBlobContract.NodeRef(), blobRef),
		Catalog:    builtins.Catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	program, ok := result.Program()
	if !ok {
		t.Fatal("missing conversion Program")
	}
	if entries := program.CapabilityPlan().Entries(); len(entries) != 4 {
		t.Fatalf("capability plan = %#v", entries)
	}
	opened, err := OpenProgram(program.Artifact(), builtins.Catalog, builtins.ConfigValidators, build)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := opened.BlobReferences(builtins.Catalog)
	if err != nil || len(refs) != 1 || refs[0] != blobRef {
		t.Fatalf("Program BlobReferences() = %#v, %v", refs, err)
	}
	nodes := opened.Nodes()
	if len(nodes) != 2 || nodes[0].Execution.Class != nodecontract.ExecutionEffect || nodes[1].Execution.Class != nodecontract.ExecutionEffect {
		t.Fatalf("effect nodes = %#v", nodes)
	}
	if _, err := NewInterpreter(builtins.Catalog, nil).Run(context.Background(), opened); err == nil {
		t.Fatal("pure-data preview executed an effect Program")
	}
}

func TestCompilerReportsUnavailableBlobAtItsNodeAndPort(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	ref := blob.BlobRef{MediaType: "application/octet-stream", Digest: testDigest(t, "missing blob"), Size: 4}
	result, err := New(testDigest(t, "compiler blob validation"), builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: conversionSourceForTest(builtins.BlobToStreamContract.NodeRef(), builtins.StreamToBlobContract.NodeRef(), ref),
		Catalog:    builtins.Catalog,
		BlobVerifier: BlobVerifierFunc(func(context.Context, blob.BlobRef) error {
			return errors.New("object is unavailable")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == CodeBlobUnavailable {
			if diagnostic.NodeID != "to-stream" || diagnostic.FieldPath[len(diagnostic.FieldPath)-1] != "blob" {
				t.Fatalf("diagnostic = %+v", diagnostic)
			}
			return
		}
	}
	t.Fatalf("diagnostics = %#v", result.Diagnostics)
}

func TestCompilerResolvesWorkflowResourceBindingThroughBlobVerifier(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	ref := blob.BlobRef{MediaType: schema.MacroResourceMediaType, Digest: testDigest(t, "workflow resource blob"), Size: 4}
	raw := string(conversionSourceForTest(builtins.BlobToStreamContract.NodeRef(), builtins.StreamToBlobContract.NodeRef(), ref))
	raw = strings.Replace(raw,
		fmt.Sprintf(`"blob":{"kind":"blob","blob":{"mediaType":%q,"digest":%q,"size":%d}}`, ref.MediaType, ref.Digest, ref.Size),
		`"blob":{"kind":"resource","resource":{"resourceId":"recording"}}`, 1)
	raw = strings.Replace(raw, `"resources":[]`, fmt.Sprintf(
		`"resources":[{"id":"recording","kind":"macro","name":"Recording","macro":{"blob":{"mediaType":%q,"digest":%q,"size":%d},"baseResolution":[1920,1080],"actionCount":1,"durationUs":1000}}]`,
		ref.MediaType, ref.Digest, ref.Size), 1)
	verified := 0
	result, err := New(testDigest(t, "compiler resource binding"), builtins.ConfigValidators).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: []byte(raw), Catalog: builtins.Catalog,
		BlobVerifier: BlobVerifierFunc(func(_ context.Context, candidate blob.BlobRef) error {
			verified++
			if candidate != ref {
				t.Fatalf("verified blob = %#v, want %#v", candidate, ref)
			}
			return nil
		}),
	})
	if err != nil || hasDiagnostic(result.Diagnostics, CodeInvalidBinding) || hasDiagnostic(result.Diagnostics, CodeBlobUnavailable) || verified != 1 {
		t.Fatalf("diagnostics=%#v verified=%d err=%v", result.Diagnostics, verified, err)
	}
}

func TestCompilerRejectsDependencyThatDoesNotMatchCatalogLock(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	entry, ok := catalog.Lookup(contract.NodeRef().NodeTypeID)
	if !ok {
		t.Fatal("concat node is missing from test Catalog")
	}
	dependency := fmt.Sprintf(
		`{"publisherNamespace":"https://schemas.yotta.dev/packages","packageId":%q,"packageVersion":"1.0.0","manifestDigest":%q,"nodeRefs":[{"nodeTypeId":%q,"version":%q,"semanticDigest":%q}]}`,
		entry.Implementation.PackageID, testDigest(t, "wrong package manifest"), contract.NodeRef().NodeTypeID, contract.NodeRef().Version, contract.NodeRef().SemanticDigest)
	raw := strings.Replace(string(concatSourceForTest(contract.NodeRef(), "a", "b", nil)), `"dependencies":[]`, `"dependencies":[`+dependency+`]`, 1)
	result, err := New(testDigest(t, "compiler dependency mismatch"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: []byte(raw), Catalog: catalog})
	if err != nil || !hasDiagnostic(result.Diagnostics, CodeNodePackageDependencyMismatch) {
		t.Fatalf("diagnostics=%#v err=%v", result.Diagnostics, err)
	}
}

func TestCompilerRejectsBlobLiteralForResourceLeasedInput(t *testing.T) {
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	ref := builtins.StreamToBlobContract.NodeRef()
	blobRef := blob.BlobRef{MediaType: "application/octet-stream", Digest: testDigest(t, "wrong carrier"), Size: 4}
	source := []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-invalid-carrier","name":"Invalid"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"to-blob","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},
			"config":{"mediaType":"application/octet-stream"},"bindings":{"stream":{"kind":"blob","blob":{"mediaType":%q,"digest":%q,"size":%d}}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest, blobRef.MediaType, blobRef.Digest, blobRef.Size))
	result, err := New(testDigest(t, "compiler-invalid-carrier"), testConfigValidators()).CompileDraft(context.Background(), CompileRequest{
		SourceJSON: source, Catalog: builtins.Catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, CodeInvalidBinding) {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestResourceLeaseAssignmentNeverWidensOrChangesCarrierClass(t *testing.T) {
	lease := func(operations ...string) *nodecontract.ResourceLeaseBinding {
		return &nodecontract.ResourceLeaseBinding{RequirementID: "stream", Operations: operations}
	}
	if !resourceLeaseAssignable(lease("cancel", "receive"), lease("receive")) {
		t.Fatal("narrowed resource lease was rejected")
	}
	if resourceLeaseAssignable(lease("receive"), lease("receive", "send")) {
		t.Fatal("resource lease widened across a data edge")
	}
	if resourceLeaseAssignable(nil, lease("receive")) || resourceLeaseAssignable(lease("receive"), nil) {
		t.Fatal("durable/runtime carrier classes were mixed across a data edge")
	}
}

func TestOpenProgramRejectsRehashedEntryAndCapabilityForgery(t *testing.T) {
	catalog, contract := concatCatalogForTest(t)
	build := testDigest(t, "compiler")
	compiled, err := New(build, testConfigValidators()).CompileDraft(context.Background(), CompileRequest{SourceJSON: concatSourceForTest(contract.NodeRef(), "a", "b", nil), Catalog: catalog})
	if err != nil || len(compiled.Diagnostics) != 0 {
		t.Fatalf("compile diagnostics=%#v err=%v", compiled.Diagnostics, err)
	}
	program, _ := compiled.Program()
	var document programDocument
	if err := json.Unmarshal(program.Artifact(), &document); err != nil {
		t.Fatal(err)
	}
	document.Body.EntryGraph = "missing"
	forged, err := sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), catalog, testConfigValidators(), build); err == nil {
		t.Fatal("accepted missing entry graph")
	}
	document.Body.EntryGraph = "main"
	forgedPlan, err := capability.SealPlan([]capability.PlanEntry{{
		GraphID: "main", NodeID: "concat-1", Requirement: capability.Requirement{
			ID: "forged", Capability: capability.Ref{
				CapabilityID: "https://schemas.yotta.dev/capabilities/forged/v1", SemanticDigest: testDigest(t, "forged capability"),
			}, Operations: []string{"read"}, TargetSlot: "target", Scope: json.RawMessage(`{}`),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	document.Body.CapabilityPlan = forgedPlan.Bytes()
	forged, err = sealProgram(document.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProgram(forged.Artifact(), catalog, testConfigValidators(), build); err == nil {
		t.Fatal("accepted forged capability manifest")
	}
}

func concatSourceForTest(ref nodecontract.NodeRef, a, b string, edge *string) []byte {
	edges := ""
	if edge != nil {
		edges = *edge
	}
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-1","name":"Concat"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"concat-1","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},
			"config":{},"bindings":{"a":{"kind":"value","value":%q},"b":{"kind":"value","value":%q}}
		}],"edges":[%s],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, ref.NodeTypeID, ref.SemanticDigest, a, b, edges))
}

func conversionSourceForTest(toStream, toBlob nodecontract.NodeRef, ref blob.BlobRef) []byte {
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-convert","name":"Convert"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"to-blob","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{"mediaType":"application/octet-stream"},"bindings":{}},
			{"id":"to-stream","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},
			 "bindings":{"blob":{"kind":"blob","blob":{"mediaType":%q,"digest":%q,"size":%d}}}}
		],"edges":[{"channel":"data","from":{"nodeId":"to-stream","portId":"stream"},"to":{"nodeId":"to-blob","portId":"stream"}}],"inputs":[],"outputs":[]}],
		"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, toBlob.NodeTypeID, toBlob.SemanticDigest, toStream.NodeTypeID, toStream.SemanticDigest, ref.MediaType, ref.Digest, ref.Size))
}

func signalSourceForTest(source, target nodecontract.NodeRef, edges []string) []byte {
	return []byte(fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-signals","name":"Signals"},
		"revision":0,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[
			{"id":"source","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":0,"y":0},"config":{},"bindings":{}},
			{"id":"target","nodeRef":{"nodeTypeId":%q,"version":"1.0.0","semanticDigest":%q},"position":{"x":1,"y":0},"config":{},"bindings":{}}
		],"edges":[%s],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, source.NodeTypeID, source.SemanticDigest, target.NodeTypeID, target.SemanticDigest, strings.Join(edges, ",")))
}

func signalCatalogForTest(t *testing.T) (nodecatalog.Snapshot, nodecontract.Contract, nodecontract.Contract) {
	t.Helper()
	source := signalContractForTest(t, "source", []string{}, []string{"next"}, []string{"failed"})
	target := signalContractForTest(t, "target", []string{"in"}, []string{}, []string{})
	return sealSignalCatalogForTest(t, source, target), source, target
}

func signalCycleCatalogForTest(t *testing.T) (nodecatalog.Snapshot, nodecontract.Contract, nodecontract.Contract) {
	t.Helper()
	left := signalContractForTest(t, "source", []string{"in"}, []string{"next"}, []string{})
	right := signalContractForTest(t, "target", []string{"in"}, []string{"next"}, []string{})
	return sealSignalCatalogForTest(t, left, right), left, right
}

func signalContractForTest(t *testing.T, name string, execInputs, execOutputs, errorOutputs []string) nodecontract.Contract {
	t.Helper()
	nodeID := "https://schemas.yotta.dev/nodes/test/" + name
	configID := nodeID + "/config"
	ports := func(values []string) []nodecontract.SignalPort {
		result := make([]nodecontract.SignalPort, len(values))
		for index, value := range values {
			result[index] = nodecontract.SignalPort{ID: value}
		}
		return result
	}
	class := nodecontract.ExecutionEvent
	if len(execInputs) != 0 {
		class = nodecontract.ExecutionControl
	}
	contract, err := nodecontract.Seal(nodecontract.Draft{Version: "1.0.0",
		NodeTypeID: nodeID, ConfigSchemaRoot: configID,
		ConfigSchemaBundle: []datatype.SchemaResource{{ID: configID, Schema: json.RawMessage(fmt.Sprintf(`{"$id":%q,"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`, configID))}},
		Ports: nodecontract.PortSet{
			DataInputs: []nodecontract.DataInputPort{}, DataOutputs: []nodecontract.DataOutputPort{},
			ExecInputs: ports(execInputs), ExecOutputs: ports(execOutputs), ErrorOutputs: ports(errorOutputs),
		},
		Execution: nodecontract.ExecutionSpec{
			Class: class, Effects: []nodecontract.EffectID{},
			Determinism: nodecontract.Deterministic, Evaluation: nodecontract.EvaluationPush, Cache: nodecontract.CacheNone,
			Retry: nodecontract.RetryNever, Cancellation: nodecontract.CancellationCooperative, Timeout: nodecontract.TimeoutNone,
		},
		Instruction:            nodecontract.Invoke(),
		CapabilityRequirements: []capability.Requirement{},
		Errors:                 []nodecontract.ErrorSpec{{Code: "test." + name + "_failed", Category: "test", RetryHint: false}},
		StatusEvents:           []nodecontract.StatusEventSpec{},
		ImplementationABI:      []nodecontract.ABIRequirement{{Kind: nodecontract.ABIBuiltin, Version: "v1"}},
		Authoring:              nodecontract.Authoring{Tags: []string{"test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func sealSignalCatalogForTest(t *testing.T, contracts ...nodecontract.Contract) nodecatalog.Snapshot {
	t.Helper()
	bindings := make([]nodecatalog.Binding, len(contracts))
	for index, contract := range contracts {
		bindings[index] = nodecatalog.Binding{Contract: contract, Implementation: nodecatalog.ImplementationLock{
			PackageID: "https://schemas.yotta.dev/packages/test/v1", ArtifactDigest: testDigest(t, contract.NodeRef().NodeTypeID),
			ABI: nodecontract.ABIRequirement{Kind: nodecontract.ABIBuiltin, Version: "v1"}, Entrypoint: "test." + fmt.Sprint(index),
		}}
	}
	catalog, err := nodecatalog.Seal([]datatype.Definition{}, []capability.Definition{}, bindings, "v1")
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func concatCatalogForTest(t *testing.T) (nodecatalog.Snapshot, nodecontract.Contract) {
	t.Helper()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	return builtins.Catalog, builtins.ConcatContract
}

func testDigest(t *testing.T, value string) artifact.Digest {
	t.Helper()
	digest, err := artifact.Sum("yotta/test/v1", []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func hasDiagnostic(values []Diagnostic, code string) bool {
	for _, diagnostic := range values {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
