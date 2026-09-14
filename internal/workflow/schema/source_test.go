package schema

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestParseSource31PreservesExplicitBindingStateAndChannels(t *testing.T) {
	raw := fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5",
		"workflow":{"id":"wf-1","name":"Concat"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"concat-1","nodeRef":{"nodeTypeId":"https://schemas.yotta.dev/nodes/text/concat","version":"1.0.0","semanticDigest":"sha256:%s"},
			"position":{"x":0,"y":0},"config":{},
			"bindings":{"a":{"kind":"value","value":"hello"},"b":{"kind":"default"}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, strings.Repeat("1", 64))
	source, diagnostics := ParseSource([]byte(raw))
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	node := source.Graphs[0].Nodes[0]
	if node.NodeRef.NodeTypeID == "" || node.Bindings["a"].Kind != BindingValue || string(node.Bindings["a"].Value) != `"hello"` || node.Bindings["b"].Kind != BindingDefault {
		t.Fatalf("parsed node = %#v", node)
	}
}

func TestParseSource31PreservesTypedBlobBinding(t *testing.T) {
	raw := strings.Replace(validSource31ForTest(), `"bindings":{"a":{"kind":"value","value":"hello"},"b":{"kind":"default"}}`,
		`"bindings":{"a":{"kind":"blob","blob":{"mediaType":"application/octet-stream","digest":"sha256:`+strings.Repeat("2", 64)+`","size":4}},"b":{"kind":"default"}}`, 1)
	source, diagnostics := ParseSource([]byte(raw))
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	binding := source.Graphs[0].Nodes[0].Bindings["a"]
	if binding.Kind != BindingBlob || binding.Blob == nil || binding.Blob.Size != 4 {
		t.Fatalf("blob binding = %#v", binding)
	}
}

func TestParseSource31ValidatesOptionalWorkflowTimestamps(t *testing.T) {
	valid := strings.Replace(
		validSource31ForTest(),
		`"name":"Concat"`,
		`"name":"Concat","createdAt":"2026-07-19T10:00:00+08:00","updatedAt":"2026-07-19T11:00:00+08:00"`,
		1,
	)
	source, diagnostics := ParseSource([]byte(valid))
	if len(diagnostics) != 0 || source.Workflow.CreatedAt == "" || source.Workflow.UpdatedAt == "" {
		t.Fatalf("timestamped source = %#v, diagnostics = %#v", source.Workflow, diagnostics)
	}

	for _, raw := range []string{
		strings.Replace(valid, `"createdAt":"2026-07-19T10:00:00+08:00"`, `"createdAt":"not-a-time"`, 1),
		strings.Replace(valid, `"updatedAt":"2026-07-19T11:00:00+08:00"`, `"updatedAt":"2026-07-19T09:00:00+08:00"`, 1),
	} {
		if _, diagnostics := ParseSource([]byte(raw)); len(diagnostics) == 0 || diagnostics[0].Code != CodeInvalidField {
			t.Fatalf("invalid timestamp diagnostics = %#v", diagnostics)
		}
	}
}

func TestParseSource31RejectsLegacyKindAndImplicitEdgeChannel(t *testing.T) {
	legacy := `{"format":"yotta.workflow","version":3,"workflow":{"id":"wf","name":"x"},"revision":0,"entryGraph":"main","graphs":[],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`
	if _, diagnostics := ParseSource([]byte(legacy)); len(diagnostics) == 0 || diagnostics[0].Code != CodeUnsupportedWorkflowFormat {
		t.Fatalf("legacy diagnostics = %#v", diagnostics)
	}
}

func TestParseSource31PreservesStableGraphInterfaceIDsAndDisplayNames(t *testing.T) {
	raw := `{
		"format":"yotta.workflow","version":"5",
		"workflow":{"id":"wf-interface","name":"Interface"},"revision":0,"entryGraph":"main",
		"graphs":[
			{"id":"main","kind":"main","nodes":[],"edges":[],"inputs":[],"outputs":[]},
			{"id":"child","kind":"subgraph","nodes":[
				{"id":"inner","nodeRef":{"nodeTypeId":"https://schemas.yotta.dev/nodes/control/delay","version":"1.0.0","semanticDigest":"sha256:1111111111111111111111111111111111111111111111111111111111111111"},"position":{"x":0,"y":0},"config":{},"bindings":{}}
			],"edges":[],"entries":[{"nodeId":"inner","portId":"in"}],
			 "inputs":[{"id":"duration_1","name":"等待时长","type":{"kind":"ref","ref":{"typeId":"https://schemas.yotta.dev/types/core/duration/v1","semanticDigest":"sha256:2222222222222222222222222222222222222222222222222222222222222222"}},"nodeId":"inner","portId":"duration-milliseconds"}],
			 "outputs":[],
			 "exits":[{"id":"completed_1","name":"等待完成","channel":"exec","endpoint":{"nodeId":"inner","portId":"done"}}]}
		],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`

	source, diagnostics := ParseSource([]byte(raw))
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	child := source.Graphs[1]
	if child.Inputs[0].ID != "duration_1" || child.Inputs[0].Name != "等待时长" ||
		child.Exits[0].ID != "completed_1" || child.Exits[0].Name != "等待完成" {
		t.Fatalf("interface = inputs:%#v exits:%#v", child.Inputs, child.Exits)
	}
}

func TestParseSource31RejectsUnknownDuplicateAndMalformedBindingState(t *testing.T) {
	valid := validSource31ForTest()
	tests := []struct {
		name string
		raw  string
		code string
	}{
		{"unknown", strings.Replace(valid, `"revision":0`, `"revision":0,"legacy":true`, 1), CodeUnknownField},
		{"duplicate", strings.Replace(valid, `"revision":0`, `"revision":0,"revision":1`, 1), CodeDuplicateField},
		{"value absent", strings.Replace(valid, `{"kind":"value","value":"hello"}`, `{"kind":"value"}`, 1), CodeInvalidField},
		{"default has value", strings.Replace(valid, `{"kind":"default"}`, `{"kind":"default","value":"x"}`, 1), CodeInvalidField},
		{"blob absent", strings.Replace(valid, `{"kind":"value","value":"hello"}`, `{"kind":"blob"}`, 1), CodeInvalidField},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := ParseSource([]byte(test.raw))
			if len(diagnostics) == 0 || diagnostics[0].Code != test.code {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestParseSource31RequiresExplicitEdgeChannel(t *testing.T) {
	raw := strings.Replace(validSource31ForTest(), `"edges":[]`, `"edges":[{"from":{"nodeId":"a","portId":"result"},"to":{"nodeId":"b","portId":"a"}}]`, 1)
	_, diagnostics := ParseSource([]byte(raw))
	if len(diagnostics) == 0 || diagnostics[0].Code != CodeMissingRequiredField {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestParseSource31RejectsStatusAsWorkflowEdge(t *testing.T) {
	raw := strings.Replace(validSource31ForTest(), `"edges":[]`, `"edges":[{"channel":"status","from":{"nodeId":"a","portId":"progress"},"to":{"nodeId":"b","portId":"in"}}]`, 1)
	_, diagnostics := ParseSource([]byte(raw))
	if len(diagnostics) == 0 || diagnostics[0].Code != CodeInvalidField {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestParseSource31RejectsAttributionThatCanCarryPathsOrPrompts(t *testing.T) {
	valid := validSource31ForTest()
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "filesystem path", raw: strings.Replace(valid, `"id":"concat-1"`, `"id":"C:\\secrets\\api-key"`, 1)},
		{name: "prompt text", raw: strings.Replace(valid, `"id":"concat-1"`, `"id":"ignore previous instructions"`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, diagnostics := ParseSource([]byte(test.raw)); len(diagnostics) == 0 || diagnostics[0].Code != CodeInvalidField {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestParseSource31ValidatesNestedTypeExpressionsAndWholeDocumentBudget(t *testing.T) {
	invalidType := strings.Replace(validSource31ForTest(), `"variables":[]`, fmt.Sprintf(`"variables":[{"name":"bad","type":{"kind":"ref","ref":{"typeId":"https://schemas.yotta.dev/types/bad","semanticDigest":"sha256:%s"}},"default":null}]`, strings.Repeat("2", 64)), 1)
	if _, diagnostics := ParseSource([]byte(invalidType)); len(diagnostics) == 0 || diagnostics[0].Code != CodeInvalidField {
		t.Fatalf("invalid TypeExpression diagnostics = %#v", diagnostics)
	}
	deep := strings.Replace(validSource31ForTest(), `"config":{}`, `"config":{"deep":`+strings.Repeat("[", 129)+`0`+strings.Repeat("]", 129)+`}`, 1)
	if _, diagnostics := ParseSource([]byte(deep)); len(diagnostics) == 0 || diagnostics[0].Code != CodeInvalidWorkflowJSON {
		t.Fatalf("deep source diagnostics = %#v", diagnostics)
	}
}

func TestParseSource31RequiresAnExplicitVariableInitialValue(t *testing.T) {
	withMissingDefault := strings.Replace(validSource31ForTest(), `"variables":[]`, fmt.Sprintf(`"variables":[{"name":"state","type":{"kind":"ref","ref":{"typeId":"https://schemas.yotta.dev/types/core/string/v1","semanticDigest":"sha256:%s"}}}]`, strings.Repeat("2", 64)), 1)
	if _, diagnostics := ParseSource([]byte(withMissingDefault)); len(diagnostics) == 0 || diagnostics[0].Code != CodeMissingRequiredField || !slices.Equal(diagnostics[0].FieldPath, []string{"variables", "0", "default"}) {
		t.Fatalf("missing variable default diagnostics = %#v", diagnostics)
	}
}

func TestParseSource31RejectsSubgraphCyclesAndPreservesAuthoringProjection(t *testing.T) {
	raw := `{
		"format":"yotta.workflow","version":"5","workflow":{"id":"wf-graphs","name":"Graphs"},"revision":0,"entryGraph":"main",
		"graphs":[
			{"id":"main","kind":"main","nodes":[],"edges":[],"inputs":[],"outputs":[],"annotations":[{"id":"note","text":"why","position":{"x":1,"y":2},"size":{"width":180,"height":80}}]},
			{"id":"a","kind":"subgraph","nodes":[],"calls":[{"id":"call-b","graphId":"b","position":{"x":0,"y":0},"bindings":{}}],"edges":[],"inputs":[],"outputs":[]},
			{"id":"b","kind":"subgraph","nodes":[],"calls":[{"id":"call-a","graphId":"a","position":{"x":0,"y":0},"bindings":{}}],"edges":[],"inputs":[],"outputs":[]}
		],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`
	_, diagnostics := ParseSource([]byte(raw))
	found := false
	for _, diagnostic := range diagnostics {
		found = found || diagnostic.Code == CodeSubgraphCallCycle
	}
	if !found {
		t.Fatalf("cycle diagnostics = %#v", diagnostics)
	}

	withProjection := strings.Replace(validSource31ForTest(), `"edges":[]`, `"edges":[{"channel":"data","from":{"nodeId":"concat-1","portId":"result"},"to":{"nodeId":"concat-1","portId":"a"},"presentation":{"reroutes":[{"x":10,"y":20}]}}],"annotations":[{"id":"note","text":"why","position":{"x":1,"y":2},"size":{"width":180,"height":80}}]`, 1)
	source, diagnostics := ParseSource([]byte(withProjection))
	if len(diagnostics) != 0 || len(source.Graphs[0].Annotations) != 1 || len(source.Graphs[0].Edges[0].Presentation.Reroutes) != 1 {
		t.Fatalf("projection source=%#v diagnostics=%#v", source, diagnostics)
	}
}

func TestParseSource31RejectsEveryCallPathBeyondTheRuntimeDepthBudget(t *testing.T) {
	graphs := make([]string, 0, MaxGraphDepth+1)
	for depth := 0; depth <= MaxGraphDepth; depth++ {
		id, kind := fmt.Sprintf("g%d", depth), "subgraph"
		if depth == 0 {
			id, kind = "main", "main"
		}
		calls := ""
		if depth < MaxGraphDepth {
			calls = fmt.Sprintf(`,"calls":[{"id":"next","graphId":"g%d","position":{"x":0,"y":0},"bindings":{}}]`, depth+1)
		}
		graphs = append(graphs, fmt.Sprintf(`{"id":%q,"kind":%q,"nodes":[],"edges":[],"inputs":[],"outputs":[]%s}`, id, kind, calls))
	}
	raw := fmt.Sprintf(`{"format":"yotta.workflow","version":"5","workflow":{"id":"wf-depth","name":"Depth"},"revision":0,"entryGraph":"main","graphs":[%s],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`, strings.Join(graphs, ","))
	_, diagnostics := ParseSource([]byte(raw))
	if !slices.ContainsFunc(diagnostics, func(diagnostic Diagnostic) bool { return diagnostic.Code == CodeSubgraphCallCycle }) {
		t.Fatalf("depth diagnostics = %#v", diagnostics)
	}
}

func validSource31ForTest() string {
	return fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5",
		"workflow":{"id":"wf-1","name":"Concat"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"concat-1","nodeRef":{"nodeTypeId":"https://schemas.yotta.dev/nodes/text/concat","version":"1.0.0","semanticDigest":"sha256:%s"},
			"position":{"x":0,"y":0},"config":{},
			"bindings":{"a":{"kind":"value","value":"hello"},"b":{"kind":"default"}}
		}],"edges":[],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]
	}`, strings.Repeat("1", 64))
}
