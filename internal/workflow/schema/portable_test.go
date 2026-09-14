package schema

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestParseSource31PreservesPortableReleaseDefinitions(t *testing.T) {
	source, diagnostics := ParseSource([]byte(portableSourceForTest()))
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(source.Resources) != 1 || source.Resources[0].ID != "turn-recording" || source.Resources[0].InputClip == nil {
		t.Fatalf("resources = %#v", source.Resources)
	}
	if len(source.TargetProfileDefinitions) != 1 || source.TargetProfileDefinitions[0].ID != "game" {
		t.Fatalf("target definitions = %#v", source.TargetProfileDefinitions)
	}
	if len(source.CredentialRequirements) != 1 || source.CredentialRequirements[0].Slot != "vision" {
		t.Fatalf("credential requirements = %#v", source.CredentialRequirements)
	}
	if len(source.Dependencies) != 1 || len(source.Dependencies[0].NodeRefs) != 1 {
		t.Fatalf("dependencies = %#v", source.Dependencies)
	}
	binding := source.Graphs[0].Nodes[0].Bindings["clip"]
	if binding.Kind != BindingResource || binding.Resource == nil || binding.Resource.ResourceID != "turn-recording" {
		t.Fatalf("resource binding = %#v", binding)
	}
}

func TestParseSource31PreservesExactDerivedReleaseOrigin(t *testing.T) {
	origin := fmt.Sprintf(
		`"derivedFrom":{"releaseDigest":"sha256:%s","sourceHash":"sha256:%s","attestationDigest":"sha256:%s","publisherNamespace":"https://schemas.example.test/publishers/acme","workflowId":"published-workflow","releaseVersion":"1.2.3"},`,
		strings.Repeat("4", 64),
		strings.Repeat("5", 64),
		strings.Repeat("6", 64),
	)
	raw := strings.Replace(portableSourceForTest(), `"revision":0`, origin+`"revision":0`, 1)
	source, canonical, _, diagnostics, err := CanonicalSource([]byte(raw))
	if err != nil || len(diagnostics) != 0 || source.DerivedFrom == nil {
		t.Fatalf("CanonicalSource() = %#v, %#v, %v", source.DerivedFrom, diagnostics, err)
	}
	reopened, reopenedDiagnostics := ParseSource(canonical)
	if len(reopenedDiagnostics) != 0 || reopened.DerivedFrom == nil ||
		reopened.DerivedFrom.WorkflowID != "published-workflow" ||
		reopened.DerivedFrom.ReleaseVersion != "1.2.3" {
		t.Fatalf("reopened origin = %#v, diagnostics = %#v", reopened.DerivedFrom, reopenedDiagnostics)
	}
}

func TestParseSource31RejectsInvalidOrSelfDerivedReleaseOrigin(t *testing.T) {
	valid := portableSourceForTest()
	for name, origin := range map[string]string{
		"invalid digest": fmt.Sprintf(
			`"derivedFrom":{"releaseDigest":"sha256:%s","sourceHash":"sha256:%s","attestationDigest":"invalid","publisherNamespace":"https://schemas.example.test/publishers/acme","workflowId":"published-workflow","releaseVersion":"1.2.3"},`,
			strings.Repeat("4", 64),
			strings.Repeat("5", 64),
		),
		"self reference": fmt.Sprintf(
			`"derivedFrom":{"releaseDigest":"sha256:%s","sourceHash":"sha256:%s","attestationDigest":"sha256:%s","publisherNamespace":"https://schemas.example.test/publishers/acme","workflowId":"wf-portable","releaseVersion":"1.2.3"},`,
			strings.Repeat("4", 64),
			strings.Repeat("5", 64),
			strings.Repeat("6", 64),
		),
	} {
		t.Run(name, func(t *testing.T) {
			raw := strings.Replace(valid, `"revision":0`, origin+`"revision":0`, 1)
			if _, diagnostics := ParseSource([]byte(raw)); len(diagnostics) == 0 {
				t.Fatal("invalid derived release origin was accepted")
			}
		})
	}
}

func TestParseSource31RejectsInvalidPortableDefinitions(t *testing.T) {
	valid := portableSourceForTest()
	tests := []struct {
		name string
		raw  string
		path []string
	}{
		{
			name: "duplicate resource",
			raw:  strings.Replace(valid, `"resources":[{`, `"resources":[{"id":"turn-recording","kind":"macro","name":"Duplicate","macro":{"blob":{"mediaType":"application/vnd.yotta.macro+json","digest":"sha256:`+strings.Repeat("8", 64)+`","size":4},"baseResolution":[1920,1080],"actionCount":1,"durationUs":1}},{`, 1),
			path: []string{"resources"},
		},
		{
			name: "resource kind shape",
			raw:  strings.Replace(valid, `"inputClip":{`, `"macro":{"blob":{"mediaType":"application/vnd.yotta.macro+json","digest":"sha256:`+strings.Repeat("8", 64)+`","size":4},"baseResolution":[1920,1080],"actionCount":1,"durationUs":1},"inputClip":{`, 1),
			path: []string{"resources"},
		},
		{
			name: "dangling resource binding",
			raw:  strings.Replace(valid, `"resourceId":"turn-recording"`, `"resourceId":"missing"`, 1),
			path: []string{"graphs", "0", "nodes", "0", "bindings", "clip", "resource"},
		},
		{
			name: "local target path",
			raw:  strings.Replace(valid, `"windowTitle":"Example Game"`, `"windowTitle":"Example Game","executablePath":"C:\\\\Games\\\\game.exe"`, 1),
			path: []string{"targetProfileDefinitions"},
		},
		{
			name: "target defaults violate schema",
			raw:  strings.Replace(valid, `"windowTitle":"Example Game"`, `"windowTitle":42`, 1),
			path: []string{"targetProfileDefinitions"},
		},
		{
			name: "dependency node not used",
			raw:  strings.Replace(valid, `"nodeTypeId":"https://schemas.example.test/publishers/acme/nodes/playback"`, `"nodeTypeId":"https://schemas.example.test/publishers/acme/nodes/other"`, 1),
			path: []string{"dependencies"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics := ParseSource([]byte(test.raw))
			if !slices.ContainsFunc(diagnostics, func(diagnostic Diagnostic) bool {
				return diagnostic.Code == CodeInvalidField && slices.Equal(diagnostic.FieldPath, test.path)
			}) {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestParseSource31AllowsLocalLogicalTargetSlotWithoutPortableDefinition(t *testing.T) {
	raw := strings.Replace(
		portableSourceForTest(),
		`"targetProfileDefinitions":[{"id":"game","name":"Game","targetKind":"desktop-window","adapterKind":"win32","profileVersion":"1",
			"settingsSchemaRoot":"https://schemas.example.test/targets/game/v1/schema","settingsSchemaBundle":[{"id":"https://schemas.example.test/targets/game/v1/schema","schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://schemas.example.test/targets/game/v1/schema","type":"object","properties":{"windowTitle":{"type":"string"}},"additionalProperties":false}}],
			"initialDefaults":{"windowTitle":"Example Game"},"discoveryHints":[{"kind":"executable-name","value":"game.exe"}]}],
		"targetDefaults":[{"target":"target","slot":"game"}]`,
		`"targetProfileDefinitions":[],
		"targetDefaults":[{"target":"target","slot":"window-target"}]`,
		1,
	)
	source, diagnostics := ParseSource([]byte(raw))
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(source.TargetDefaults) != 1 || source.TargetDefaults[0].Slot != "window-target" {
		t.Fatalf("target defaults = %#v", source.TargetDefaults)
	}
}

func portableSourceForTest() string {
	nodeDigest := strings.Repeat("1", 64)
	manifestDigest := strings.Repeat("2", 64)
	blobDigest := strings.Repeat("3", 64)
	return fmt.Sprintf(`{
		"format":"yotta.workflow","version":"5",
		"workflow":{"id":"wf-portable","name":"Portable"},"revision":0,"entryGraph":"main",
		"graphs":[{"id":"main","kind":"main","nodes":[{
			"id":"playback","nodeRef":{"nodeTypeId":"https://schemas.example.test/publishers/acme/nodes/playback","version":"1.0.0","semanticDigest":"sha256:%s"},
			"position":{"x":0,"y":0},"config":{},"bindings":{"clip":{"kind":"resource","resource":{"resourceId":"turn-recording"}}}
		}],"edges":[],"inputs":[],"outputs":[]}],
		"resources":[{"id":"turn-recording","kind":"input-clip","name":"Turn recording","description":"source facts","tags":["camera"],
			"inputClip":{"blob":{"mediaType":"application/vnd.yotta.input-clip","digest":"sha256:%s","size":128},"durationUs":1000,"eventCount":2,
			"recordingMode":"precise","mouseMode":"relative","baseResolution":[1920,1080],"mouseCounts360":1504,"stopHotkeyVk":123}}],
		"targetProfileDefinitions":[{"id":"game","name":"Game","targetKind":"desktop-window","adapterKind":"win32","profileVersion":"1",
			"settingsSchemaRoot":"https://schemas.example.test/targets/game/v1/schema","settingsSchemaBundle":[{"id":"https://schemas.example.test/targets/game/v1/schema","schema":{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://schemas.example.test/targets/game/v1/schema","type":"object","properties":{"windowTitle":{"type":"string"}},"additionalProperties":false}}],
			"initialDefaults":{"windowTitle":"Example Game"},"discoveryHints":[{"kind":"executable-name","value":"game.exe"}]}],
		"targetDefaults":[{"target":"target","slot":"game"}],
		"credentialRequirements":[{"slot":"vision","kind":"https://schemas.example.test/credentials/vision/v1","purpose":"Analyze screenshots"}],
		"dependencies":[{"publisherNamespace":"https://schemas.example.test/publishers/acme","packageId":"https://schemas.example.test/publishers/acme/packages/automation/v1","packageVersion":"1.2.3","manifestDigest":"sha256:%s",
			"nodeRefs":[{"nodeTypeId":"https://schemas.example.test/publishers/acme/nodes/playback","version":"1.0.0","semanticDigest":"sha256:%s"}]}],
		"variables":[]
	}`, nodeDigest, blobDigest, manifestDigest, nodeDigest)
}
