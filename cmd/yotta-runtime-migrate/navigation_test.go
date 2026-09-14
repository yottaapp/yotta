package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
)

func TestNavigationMigrationCompilesAndPreservesTargetAndAxis(t *testing.T) {
	b, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	start, _ := b.Definition(nodes.RunStartedNodeID)
	startRef, _ := json.Marshal(start.Contract.NodeRef())
	raw := []byte(`{"format":"yotta.workflow","version":"5","workflow":{"id":"migration","name":"Movement"},"revision":19,"entryGraph":"main","graphs":[{"id":"main","kind":"main","nodes":[{"id":"start","nodeRef":` + string(startRef) + `,"position":{"x":0,"y":0},"config":{},"bindings":{}},{"id":"move","nodeRef":{"nodeTypeId":"https://schemas.yotta.dev/nodes/automation/move-character-to","version":"1.0.0","semanticDigest":"sha256:289bf5eb7fdbebf90bf4b9076a9c10e809e312618e1149a32ae3c87f6472eac9"},"position":{"x":200,"y":0},"config":{"slot":"game","source":"coordinates","axisHeading":90,"axisSign":-1,"path":"/coordinates"},"bindings":{"target-x":{"kind":"value","value":123},"target-y":{"kind":"value","value":456},"timeout":{"kind":"default"},"tolerance":{"kind":"default"},"pulse":{"kind":"value","value":500}}}],"edges":[{"channel":"exec","from":{"nodeId":"start","portId":"started"},"to":{"nodeId":"move","portId":"in"}}],"inputs":[],"outputs":[]}],"variables":[],"resources":[],"targetProfileDefinitions":[],"credentialRequirements":[],"dependencies":[]}`)
	converted, err := migrateNavigation(raw, b)
	if err != nil {
		t.Fatal(err)
	}
	build, _ := artifact.Sum("yotta/test/v1", []byte("migration"))
	result, err := compiler.New(build, b.ConfigValidators).CompileDraft(context.Background(), compiler.CompileRequest{SourceJSON: converted, Catalog: b.Catalog})
	if _, ok := result.Program(); err != nil || !ok {
		t.Fatalf("migration did not compile: %v %v", err, result.Diagnostics)
	}
	for _, expected := range []string{`"axisHeading":90`, `"axisSign":-1`, `"slot":"coordinates"`, `"value":"/coordinates"`, `"value":123`, `"value":456`, `"revision":19`} {
		if !bytes.Contains(converted, []byte(expected)) {
			t.Fatalf("lost %s", expected)
		}
	}
	if bytes.Contains(converted, []byte(`"pulse"`)) {
		t.Fatal("old movement binding retained")
	}
	again, err := migrateNavigation(converted, b)
	if err != nil || !bytes.Equal(converted, again) {
		t.Fatal("migration is not idempotent", err)
	}
}
