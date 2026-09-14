package authoring_test

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/workflow/authoring"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func TestEngineAppliesAtomicTypedPatchWithHostOwnedNodeIDs(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"node-left", "node-right"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	source := emptySource()
	result, err := engine.Apply(source, []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "left", Position: schema.Position{X: 10, Y: 20},
		}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "right", Position: schema.Position{X: 30, Y: 40},
		}},
		{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$left", PortID: "a", Value: "hello"}},
		{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "$left", PortID: "b", Value: " world"}},
		{Kind: authoring.CommandClearBinding, ClearBinding: &authoring.PortCommand{GraphID: "main", NodeID: "$right", PortID: "a"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{
			Channel: schema.EdgeData,
			From:    schema.Endpoint{NodeID: "$left", PortID: "result"},
			To:      schema.Endpoint{NodeID: "$right", PortID: "a"},
		})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.Revision != 1 || len(result.Source.Graphs[0].Nodes) != 2 || len(result.Source.Graphs[0].Edges) != 1 {
		t.Fatalf("result = %#v", result.Source)
	}
	if got := result.Source.Graphs[0].Edges[0]; got.From.NodeID != "node-left" || got.To.NodeID != "node-right" {
		t.Fatalf("resolved edge = %#v", got)
	}
	if len(result.GeneratedNodes) != 2 || result.GeneratedNodes[0].Handle != "left" || result.GeneratedNodes[1].NodeID != "node-right" {
		t.Fatalf("generated nodes = %#v", result.GeneratedNodes)
	}
	if source.Revision != 0 || len(source.Graphs[0].Nodes) != 0 {
		t.Fatalf("input source was mutated: %#v", source)
	}
}

func TestEngineKeepsRunRootNodesInTheMainGraph(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "run-root" })
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind: authoring.CommandAddGraph,
		AddGraph: &authoring.AddGraphCommand{Graph: schema.Graph{
			ID: "child", Name: "Child", Kind: schema.GraphKindSubgraph,
			Nodes: []schema.Node{}, Calls: []schema.GraphCall{}, Edges: []schema.Edge{},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{}, Entries: []schema.Endpoint{},
			Exits: []schema.GraphExit{}, Annotations: []schema.Annotation{},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Apply(created.Source, []authoring.Command{{
		Kind: authoring.CommandAddNode,
		AddNode: &authoring.AddNodeCommand{
			GraphID: "child", NodeTypeID: nodes.RunStartedNodeID, Position: schema.Position{},
		},
	}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "INVALID_INSTRUCTION_PLACEMENT" {
		t.Fatalf("subgraph run root error = %#v", err)
	}

	withRoot, err := engine.Apply(created.Source, []authoring.Command{{
		Kind: authoring.CommandAddNode,
		AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.RunStartedNodeID, Position: schema.Position{},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Apply(withRoot.Source, []authoring.Command{{
		Kind: authoring.CommandCollapseSelection,
		CollapseSelection: &authoring.CollapseSelectionCommand{
			GraphID: "main", SubgraphID: "collapsed", CallID: "call-collapsed", Name: "Collapsed",
			NodeIDs: []string{"run-root"}, Position: schema.Position{},
		},
	}})
	if !errors.As(err, &patchErr) || patchErr.Code != "INVALID_SELECTION" {
		t.Fatalf("collapsed run root error = %#v", err)
	}
}

func TestEngineBindsWorkflowResourceWithoutLosingPortableIdentity(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "node-resource" })
	if err != nil {
		t.Fatal(err)
	}
	source := emptySource()
	source.Resources = []schema.WorkflowResource{{
		ID: "template", Kind: schema.ResourceImage, Name: "Template",
		Image: &schema.ImageResource{Variants: []schema.ImageResourceVariant{{
			ID: "default", Resolution: [2]int{1, 1}, BBox: [4]int{0, 0, 1, 1},
			Blob: blob.BlobRef{
				MediaType: "image/png",
				Digest:    artifact.Digest("sha256:" + strings.Repeat("1", 64)),
				Size:      1,
			},
		}}},
	}}
	resource := schema.ResourceBinding{ResourceID: "template", VariantID: "default"}
	result, err := engine.Apply(source, []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "node", Position: schema.Position{},
		}},
		{Kind: authoring.CommandBindResource, BindResource: &authoring.BindResourceCommand{
			GraphID: "main", NodeID: "$node", PortID: "a", Resource: resource,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := result.Source.Graphs[0].Nodes[0].Bindings["a"]
	if binding.Kind != schema.BindingResource || binding.Resource == nil || *binding.Resource != resource {
		t.Fatalf("resource binding = %#v", binding)
	}
}

func TestEngineAddsTargetNodeWithWorkflowTargetDefault(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "click-template" })
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandSetTargetDefault, SetTargetDefault: &authoring.SetTargetDefaultCommand{
			Target: "target", Slot: "window-target",
		}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.ClickTemplateNodeID, Handle: "click", Position: schema.Position{},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	node := result.Source.Graphs[0].Nodes[0]
	if node.Config["slot"] != nil {
		t.Fatalf("workflow target default was copied into node config: %#v", node.Config)
	}
}

func TestEngineAddsTargetNodeWithExplicitTargetSlot(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "click-template" })
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.ClickTemplateNodeID, Handle: "click", Position: schema.Position{},
		}},
		{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{
			GraphID: "main", NodeID: "$click", FieldID: "slot", Value: "window-target",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.Graphs[0].Nodes[0].Config["slot"] != "window-target" {
		t.Fatalf("node config = %#v", result.Source.Graphs[0].Nodes[0].Config)
	}
}

func TestEngineAuthorsWorkflowResourceLifecycleAndProtectsReferences(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "node-resource-lifecycle" })
	if err != nil {
		t.Fatal(err)
	}
	resource := schema.WorkflowResource{
		ID: "template-lifecycle", Kind: schema.ResourceImage, Name: "Template",
		Image: &schema.ImageResource{Variants: []schema.ImageResourceVariant{{
			ID: "default", Resolution: [2]int{1, 1}, BBox: [4]int{0, 0, 1, 1},
			Blob: blob.BlobRef{
				MediaType: "image/png",
				Digest:    artifact.Digest("sha256:" + strings.Repeat("2", 64)),
				Size:      1,
			},
		}}},
	}

	added, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind: authoring.CommandAddResource, AddResource: &authoring.AddResourceCommand{Resource: resource},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(added.Source.Resources) != 1 || added.Source.Resources[0].ID != resource.ID {
		t.Fatalf("resources after add = %#v", added.Source.Resources)
	}

	updated, err := engine.Apply(added.Source, []authoring.Command{{
		Kind: authoring.CommandUpdateResourceMetadata,
		UpdateResourceMetadata: &authoring.UpdateResourceMetadataCommand{
			ResourceID:  resource.ID,
			Name:        "  Updated template  ",
			Description: "  local resource  ",
			Category:    "  Fishing  ",
			Tags:        []string{" UI ", "ui", "", "Fishing"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := updated.Source.Resources[0]
	if got.Name != "Updated template" || got.Description != "local resource" || got.Category != "Fishing" ||
		len(got.Tags) != 2 || got.Tags[0] != "Fishing" || got.Tags[1] != "UI" {
		t.Fatalf("resource after metadata update = %#v", got)
	}

	removed, err := engine.Apply(updated.Source, []authoring.Command{{
		Kind: authoring.CommandRemoveResource, RemoveResource: &authoring.RemoveResourceCommand{ResourceID: resource.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Source.Resources) != 0 {
		t.Fatalf("resources after remove = %#v", removed.Source.Resources)
	}

	referenced, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddResource, AddResource: &authoring.AddResourceCommand{Resource: resource}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "node", Position: schema.Position{},
		}},
		{Kind: authoring.CommandBindResource, BindResource: &authoring.BindResourceCommand{
			GraphID: "main", NodeID: "$node", PortID: "a",
			Resource: schema.ResourceBinding{ResourceID: resource.ID, VariantID: "default"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement := resource
	replacement.Image = &schema.ImageResource{Variants: []schema.ImageResourceVariant{{
		ID: "default", Resolution: [2]int{2, 2}, BBox: [4]int{0, 0, 2, 2},
		Blob: blob.BlobRef{
			MediaType: "image/png",
			Digest:    artifact.Digest("sha256:" + strings.Repeat("3", 64)),
			Size:      2,
		},
	}}}
	replaced, err := engine.Apply(referenced.Source, []authoring.Command{{
		Kind: authoring.CommandReplaceResource,
		ReplaceResource: &authoring.ReplaceResourceCommand{
			ResourceID: resource.ID,
			Resource:   replacement,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	gotBinding := replaced.Source.Graphs[0].Nodes[0].Bindings["a"]
	if gotBinding.Resource == nil || gotBinding.Resource.ResourceID != resource.ID ||
		replaced.Source.Resources[0].Image.Variants[0].Blob != replacement.Image.Variants[0].Blob {
		t.Fatalf("replacement did not preserve shared binding: %#v %#v", gotBinding, replaced.Source.Resources)
	}

	changedID := replacement
	changedID.ID = "different"
	_, err = engine.Apply(referenced.Source, []authoring.Command{{
		Kind: authoring.CommandReplaceResource,
		ReplaceResource: &authoring.ReplaceResourceCommand{
			ResourceID: resource.ID,
			Resource:   changedID,
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "RESOURCE_ID_CHANGED") {
		t.Fatalf("changed replacement ID error = %v", err)
	}

	changedKind := replacement
	changedKind.Kind = schema.ResourceMacro
	changedKind.Image = nil
	changedKind.Macro = &schema.MacroResource{
		Blob: blob.BlobRef{
			MediaType: schema.MacroResourceMediaType,
			Digest:    artifact.Digest("sha256:" + strings.Repeat("4", 64)),
			Size:      2,
		},
		BaseResolution: [2]int{2, 2},
	}
	_, err = engine.Apply(referenced.Source, []authoring.Command{{
		Kind: authoring.CommandReplaceResource,
		ReplaceResource: &authoring.ReplaceResourceCommand{
			ResourceID: resource.ID,
			Resource:   changedKind,
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "RESOURCE_KIND_CHANGED") {
		t.Fatalf("changed replacement kind error = %v", err)
	}

	_, err = engine.Apply(referenced.Source, []authoring.Command{{
		Kind: authoring.CommandRemoveResource, RemoveResource: &authoring.RemoveResourceCommand{ResourceID: resource.ID},
	}})
	if err == nil || !strings.Contains(err.Error(), "RESOURCE_IN_USE") {
		t.Fatalf("remove referenced resource error = %v", err)
	}
}

func TestEngineRetractsTurnScaleContractWithoutBreakingPlaybackNode(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "playback" })
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind: authoring.CommandAddNode,
		AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.PlayInputClipNodeID, Position: schema.Position{},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stale := created.Source
	node := &stale.Graphs[0].Nodes[0]
	currentRef := node.NodeRef
	node.NodeRef.Version = "1.0.0"
	node.NodeRef.SemanticDigest = "sha256:ff7ea9d0b2ca91cb2062cff30dd5ca8575555ec5363b4c76e746925ee6ae027b"
	node.Bindings["clip"] = schema.InputBinding{Kind: schema.BindingDefault}
	node.Bindings["turn-scale"] = schema.InputBinding{Kind: schema.BindingDefault}

	upgraded, err := engine.Apply(stale, []authoring.Command{{
		Kind:                authoring.CommandUpgradeNodeContract,
		UpgradeNodeContract: &authoring.NodeCommand{GraphID: "main", NodeID: "playback"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := upgraded.Source.Graphs[0].Nodes[0]
	_, retainedTurnScale := got.Bindings["turn-scale"]
	if got.NodeRef != currentRef || retainedTurnScale {
		t.Fatalf("upgraded node = %#v", got)
	}
}

func TestEngineRejectsNodeContractUpgradeThatWouldDropUserBinding(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "playback" })
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind:    authoring.CommandAddNode,
		AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.PlayInputClipNodeID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stale := created.Source
	node := &stale.Graphs[0].Nodes[0]
	node.NodeRef.Version = "1.0.0"
	node.NodeRef.SemanticDigest = "sha256:ff7ea9d0b2ca91cb2062cff30dd5ca8575555ec5363b4c76e746925ee6ae027b"
	node.Bindings["turn-scale"] = schema.InputBinding{Kind: schema.BindingDefault}
	node.Bindings["removed-input"] = schema.InputBinding{Kind: schema.BindingDefault}
	_, err = engine.Apply(stale, []authoring.Command{{
		Kind:                authoring.CommandUpgradeNodeContract,
		UpgradeNodeContract: &authoring.NodeCommand{GraphID: "main", NodeID: "playback"},
	}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "INCOMPATIBLE_NODE_UPGRADE" {
		t.Fatalf("error = %#v", err)
	}
}

func TestEngineRejectsUnregisteredNodeContractMigration(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "concat" })
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind:    authoring.CommandAddNode,
		AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	stale := created.Source
	stale.Graphs[0].Nodes[0].NodeRef.SemanticDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	stale.Graphs[0].Nodes[0].Bindings["a"] = schema.InputBinding{Kind: schema.BindingValue, Value: json.RawMessage(`"hello"`)}
	stale.Graphs[0].Nodes[0].Bindings["b"] = schema.InputBinding{Kind: schema.BindingValue, Value: json.RawMessage(`" world"`)}
	_, err = engine.Apply(stale, []authoring.Command{{
		Kind:                authoring.CommandUpgradeNodeContract,
		UpgradeNodeContract: &authoring.NodeCommand{GraphID: "main", NodeID: "concat"},
	}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "INCOMPATIBLE_NODE_UPGRADE" {
		t.Fatalf("error = %#v", err)
	}
}

func TestEngineMigratesMovePointerWithoutChangingLegacyDefaults(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "move" })
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind:    authoring.CommandAddNode,
		AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.MovePointerNodeID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	currentRef := created.Source.Graphs[0].Nodes[0].NodeRef
	stale := created.Source
	node := &stale.Graphs[0].Nodes[0]
	node.NodeRef.Version = "1.0.0"
	node.NodeRef.SemanticDigest = "sha256:2bf1f8059f1269e407d2aedf4f717cc6c0b860eb46b92abd1794a3aa3bf559af"

	upgraded, err := engine.Apply(stale, []authoring.Command{{
		Kind:                authoring.CommandUpgradeNodeContract,
		UpgradeNodeContract: &authoring.NodeCommand{GraphID: "main", NodeID: "move"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := upgraded.Source.Graphs[0].Nodes[0]
	if got.NodeRef != currentRef {
		t.Fatalf("upgraded node ref = %#v", got.NodeRef)
	}
	if binding := got.Bindings["duration"]; binding.Kind != schema.BindingValue || string(binding.Value) != "300" {
		t.Fatalf("duration binding = %#v", binding)
	}
	if binding := got.Bindings["motion"]; binding.Kind != schema.BindingValue || string(binding.Value) != `"linear"` {
		t.Fatalf("motion binding = %#v", binding)
	}
}

func TestEngineRejectsMismatchedUnionAndPublishesNothing(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "node-one" })
	if err != nil {
		t.Fatal(err)
	}
	source := emptySource()
	_, err = engine.Apply(source, []authoring.Command{{
		Kind:     authoring.CommandAddNode,
		MoveNode: &authoring.MoveNodeCommand{GraphID: "main", NodeID: "missing", Position: schema.Position{}},
	}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "INVALID_COMMAND" || patchErr.CommandIndex != 0 {
		t.Fatalf("error = %#v", err)
	}
	if source.Revision != 0 || len(source.Graphs[0].Nodes) != 0 {
		t.Fatalf("failed patch mutated input: %#v", source)
	}
	envelope := apperr.From(err)
	if envelope.ID != "INVALID_COMMAND" || envelope.Category != apperr.CategoryValidation {
		t.Fatalf("error envelope = %#v", envelope)
	}
	details, ok := envelope.Params.(map[string]any)
	if !ok || details["commandIndex"] != 0 {
		t.Fatalf("error params = %#v", envelope.Params)
	}
}

func TestEngineSetsAndClearsWorkflowTargetDefault(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "unused" })
	if err != nil {
		t.Fatal(err)
	}
	source := emptySource()
	source.TargetProfileDefinitions = []schema.TargetProfileDefinition{{
		ID: "window-target", Name: "Window target", TargetKind: "desktop-window", AdapterKind: "win32", ProfileVersion: "1",
		SettingsSchemaRoot: "https://schemas.yotta.dev/targets/window/v1/schema",
		SettingsSchemaBundle: []datatype.SchemaResource{{
			ID:     "https://schemas.yotta.dev/targets/window/v1/schema",
			Schema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://schemas.yotta.dev/targets/window/v1/schema","type":"object","additionalProperties":false}`),
		}},
		InitialDefaults: json.RawMessage(`{}`), DiscoveryHints: []schema.TargetDiscoveryHint{},
	}}
	set, err := engine.Apply(source, []authoring.Command{{
		Kind:             authoring.CommandSetTargetDefault,
		SetTargetDefault: &authoring.SetTargetDefaultCommand{Target: "target", Slot: "window-target"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if slot, ok := schema.TargetDefaultSlot(set.Source, "target"); !ok || slot != "window-target" {
		t.Fatalf("target defaults = %+v", set.Source.TargetDefaults)
	}
	cleared, err := engine.Apply(set.Source, []authoring.Command{{
		Kind:               authoring.CommandClearTargetDefault,
		ClearTargetDefault: &authoring.ClearTargetDefaultCommand{Target: "target"},
	}})
	if err != nil || len(cleared.Source.TargetDefaults) != 0 {
		t.Fatalf("cleared=%+v err=%v", cleared.Source.TargetDefaults, err)
	}
}

func TestEngineCollapsesSelectionAndProtectsReferencedSubgraph(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"root", "delay", "end"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.RunStartedNodeID, Handle: "root"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.DelayNodeID, Handle: "delay"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.EndBranchNodeID, Handle: "end"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "$root", PortID: "started"}, To: schema.Endpoint{NodeID: "$delay", PortID: "in"}})}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{Channel: schema.EdgeExec, From: schema.Endpoint{NodeID: "$delay", PortID: "done"}, To: schema.Endpoint{NodeID: "$end", PortID: "in"}})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	collapsed, err := engine.Apply(base.Source, []authoring.Command{{
		Kind:              authoring.CommandCollapseSelection,
		CollapseSelection: &authoring.CollapseSelectionCommand{GraphID: "main", SubgraphID: "wait", CallID: "call-wait", Name: "Wait", NodeIDs: []string{"delay"}, Position: schema.Position{X: 100, Y: 100}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	main, child := collapsed.Source.Graphs[0], collapsed.Source.Graphs[1]
	if collapsed.Source.Revision != 2 || len(main.Calls) != 1 || len(main.Nodes) != 2 || len(main.Edges) != 2 || len(child.Nodes) != 1 || len(child.Entries) != 1 || len(child.Exits) != 2 {
		t.Fatalf("collapsed source = %#v", collapsed.Source)
	}
	_, err = engine.Apply(collapsed.Source, []authoring.Command{{Kind: authoring.CommandRemoveGraph, RemoveGraph: &authoring.GraphCommand{GraphID: "wait"}}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "REFERENCE_IN_USE" {
		t.Fatalf("referenced delete error = %#v", err)
	}
}

func TestEngineCollapsesTrailingNodeWithUnconnectedSignalExits(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"root", "keys"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.RunStartedNodeID, Handle: "root"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.PressKeysNodeID, Handle: "keys"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{
			Channel: schema.EdgeExec,
			From:    schema.Endpoint{NodeID: "$root", PortID: "started"},
			To:      schema.Endpoint{NodeID: "$keys", PortID: "in"},
		})}},
	})
	if err != nil {
		t.Fatal(err)
	}

	collapsed, err := engine.Apply(base.Source, []authoring.Command{{
		Kind: authoring.CommandCollapseSelection,
		CollapseSelection: &authoring.CollapseSelectionCommand{
			GraphID: "main", SubgraphID: "press", CallID: "call-press",
			Name: "Press keys", NodeIDs: []string{"keys"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	child := collapsed.Source.Graphs[1]
	if len(child.Entries) != 1 || len(child.Exits) != 2 {
		t.Fatalf("collapsed child boundaries = entries:%+v exits:%+v", child.Entries, child.Exits)
	}
	if child.Exits[0].Channel != schema.EdgeExec || child.Exits[0].Endpoint.PortID != "completed" ||
		child.Exits[1].Channel != schema.EdgeError || child.Exits[1].Endpoint.PortID != "failed" {
		t.Fatalf("collapsed child exits = %+v", child.Exits)
	}
}

func TestEngineCollapsesLoopWithoutTreatingOptionalControlInputsAsGraphEntries(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"root", "loop", "orphan"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.RunStartedNodeID, Handle: "root",
		}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.RepeatNodeID, Handle: "loop",
		}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "main", NodeTypeID: nodes.DelayNodeID, Handle: "orphan",
		}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{
			GraphID: "main",
			Edge: patchEdge(schema.Edge{
				Channel: schema.EdgeExec,
				From:    schema.Endpoint{NodeID: "$root", PortID: "started"},
				To:      schema.Endpoint{NodeID: "$loop", PortID: "in"},
			}),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	collapsed, err := engine.Apply(base.Source, []authoring.Command{{
		Kind: authoring.CommandCollapseSelection,
		CollapseSelection: &authoring.CollapseSelectionCommand{
			GraphID: "main", SubgraphID: "loop-body", CallID: "call-loop",
			Name: "Loop", NodeIDs: []string{"loop"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	child := collapsed.Source.Graphs[1]
	if len(child.Entries) != 1 || child.Entries[0] != (schema.Endpoint{NodeID: "loop", PortID: "in"}) {
		t.Fatalf("collapsed loop entries = %+v", child.Entries)
	}

	_, err = engine.Apply(base.Source, []authoring.Command{{
		Kind: authoring.CommandCollapseSelection,
		CollapseSelection: &authoring.CollapseSelectionCommand{
			GraphID: "main", SubgraphID: "invalid", CallID: "call-invalid",
			Name: "Invalid", NodeIDs: []string{"loop", "orphan"},
		},
	}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "INVALID_SELECTION" {
		t.Fatalf("disconnected execution root error = %#v", err)
	}
}

func TestEngineUpdatesCallableSubgraphInterfaceAsOneCommand(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "wait" })
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind: authoring.CommandAddGraph,
		AddGraph: &authoring.AddGraphCommand{Graph: schema.Graph{
			ID: "child", Kind: schema.GraphKindSubgraph,
			Nodes: []schema.Node{},
			Edges: []schema.Edge{}, Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := engine.Apply(created.Source, []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "child", NodeTypeID: nodes.DelayNodeID, Handle: "wait"}},
		{
			Kind: authoring.CommandUpdateGraphInterface,
			UpdateGraphInterface: &authoring.GraphInterfaceCommand{
				GraphID: "child", Entries: []schema.Endpoint{{NodeID: "$wait", PortID: "in"}},
				Exits:  []schema.GraphExit{{ID: "done", Name: "等待完成", Channel: schema.EdgeExec, Endpoint: schema.Endpoint{NodeID: "$wait", PortID: "done"}}},
				Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	child := updated.Source.Graphs[1]
	if len(child.Entries) != 1 || len(child.Exits) != 1 || child.Exits[0].ID != "done" ||
		child.Exits[0].Name != "等待完成" || updated.Source.Revision != 2 {
		t.Fatalf("updated subgraph = %#v", child)
	}
	_, err = engine.Apply(updated.Source, []authoring.Command{{
		Kind: authoring.CommandUpdateGraphInterface,
		UpdateGraphInterface: &authoring.GraphInterfaceCommand{
			GraphID: "child",
			Entries: []schema.Endpoint{
				{NodeID: "wait", PortID: "in"},
				{NodeID: "wait", PortID: "in"},
			},
			Exits:  child.Exits,
			Inputs: child.Inputs, Outputs: child.Outputs,
		},
	}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != schema.CodeInvalidField ||
		!slices.Equal(patchErr.GraphPath, []string{"child"}) ||
		!slices.Equal(patchErr.FieldPath, []string{"graphs", "1", "entries"}) {
		t.Fatalf("multiple entry error = %#v", err)
	}
}

func TestEngineAuthorsGraphCallAnnotationAndRerouteLifecycle(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"left", "right"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	edge := schema.Edge{
		Channel: schema.EdgeData,
		From:    schema.Endpoint{NodeID: "left", PortID: "result"},
		To:      schema.Endpoint{NodeID: "right", PortID: "a"},
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "left"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "right"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(edge)}},
		{Kind: authoring.CommandAddGraph, AddGraph: &authoring.AddGraphCommand{Graph: schema.Graph{
			ID: "child", Name: "Child", Kind: schema.GraphKindSubgraph,
			Nodes: []schema.Node{}, Calls: []schema.GraphCall{}, Edges: []schema.Edge{},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{}, Entries: []schema.Endpoint{}, Exits: []schema.GraphExit{}, Annotations: []schema.Annotation{},
		}}},
		{Kind: authoring.CommandRenameGraph, RenameGraph: &authoring.RenameGraphCommand{GraphID: "child", Name: "  Reusable child  "}},
		{Kind: authoring.CommandAddGraphCall, AddGraphCall: &authoring.GraphCallCommand{GraphID: "main", Call: schema.GraphCall{
			ID: "call-child", GraphID: "child", Label: "Call child", Position: schema.Position{X: 300, Y: 80}, Bindings: map[string]schema.InputBinding{},
		}}},
		{Kind: authoring.CommandAddAnnotation, AddAnnotation: &authoring.AnnotationCommand{GraphID: "main", Annotation: schema.Annotation{
			ID: "note", Text: "Reusable section", Color: "amber", Position: schema.Position{X: 260, Y: 20}, Size: schema.Size{Width: 240, Height: 120},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.Source.Graphs[1].Name; got != "Reusable child" {
		t.Fatalf("renamed graph = %q", got)
	}

	updatedCall := created.Source.Graphs[0].Calls[0]
	updatedCall.Label = "Updated call"
	updatedCall.Position = schema.Position{X: 420, Y: 160}
	updatedNote := created.Source.Graphs[0].Annotations[0]
	updatedNote.Text = "Updated reusable section"
	updatedNote.Size = schema.Size{Width: 320, Height: 180}
	updated, err := engine.Apply(created.Source, []authoring.Command{
		{Kind: authoring.CommandUpdateGraphCall, UpdateGraphCall: &authoring.GraphCallCommand{GraphID: "main", Call: updatedCall}},
		{Kind: authoring.CommandUpdateAnnotation, UpdateAnnotation: &authoring.AnnotationCommand{GraphID: "main", Annotation: updatedNote}},
		{Kind: authoring.CommandSetEdgeReroutes, SetEdgeReroutes: &authoring.SetEdgeReroutesCommand{GraphID: "main", Edge: patchEdge(edge), Reroutes: []schema.Position{{X: 120, Y: 40}, {X: 220, Y: 60}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	main := updated.Source.Graphs[0]
	if main.Calls[0].Label != "Updated call" || main.Annotations[0].Text != "Updated reusable section" || len(main.Edges[0].Presentation.Reroutes) != 2 {
		t.Fatalf("updated graph elements = %#v", main)
	}

	removed, err := engine.Apply(updated.Source, []authoring.Command{
		{Kind: authoring.CommandSetEdgeReroutes, SetEdgeReroutes: &authoring.SetEdgeReroutesCommand{GraphID: "main", Edge: patchEdge(edge), Reroutes: []schema.Position{}}},
		{Kind: authoring.CommandRemoveGraphCall, RemoveGraphCall: &authoring.CallCommand{GraphID: "main", CallID: "call-child"}},
		{Kind: authoring.CommandRemoveAnnotation, RemoveAnnotation: &authoring.AnnotationIDCommand{GraphID: "main", AnnotationID: "note"}},
		{Kind: authoring.CommandRemoveGraph, RemoveGraph: &authoring.GraphCommand{GraphID: "child"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Source.Graphs) != 1 || len(removed.Source.Graphs[0].Calls) != 0 || len(removed.Source.Graphs[0].Annotations) != 0 || len(removed.Source.Graphs[0].Edges[0].Presentation.Reroutes) != 0 {
		t.Fatalf("removed graph elements = %#v", removed.Source)
	}
}

func TestEngineAppliesSubgraphExpansionAndCascadeAsAtomicPatchTransactions(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"wait-node", "expanded-node"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddGraph, AddGraph: &authoring.AddGraphCommand{Graph: schema.Graph{
			ID: "child", Name: "Wait", Kind: schema.GraphKindSubgraph,
			Nodes: []schema.Node{}, Calls: []schema.GraphCall{}, Edges: []schema.Edge{},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
			Entries: []schema.Endpoint{}, Exits: []schema.GraphExit{}, Annotations: []schema.Annotation{},
		}}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
			GraphID: "child", NodeTypeID: nodes.DelayNodeID, Handle: "wait",
		}},
		{Kind: authoring.CommandUpdateGraphInterface, UpdateGraphInterface: &authoring.GraphInterfaceCommand{
			GraphID: "child",
			Entries: []schema.Endpoint{{NodeID: "$wait", PortID: "in"}},
			Exits: []schema.GraphExit{{
				ID: "done", Name: "Done", Channel: schema.EdgeExec,
				Endpoint: schema.Endpoint{NodeID: "$wait", PortID: "done"},
			}},
			Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
		}},
		{Kind: authoring.CommandAddGraphCall, AddGraphCall: &authoring.GraphCallCommand{
			GraphID: "main",
			Call: schema.GraphCall{
				ID: "call-child", GraphID: "child",
				Position: schema.Position{X: 100, Y: 100}, Bindings: map[string]schema.InputBinding{},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("expand", func(t *testing.T) {
		expanded, err := engine.Apply(created.Source, []authoring.Command{
			{Kind: authoring.CommandRemoveGraphCall, RemoveGraphCall: &authoring.CallCommand{
				GraphID: "main", CallID: "call-child",
			}},
			{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{
				GraphID: "main", NodeTypeID: nodes.DelayNodeID, Handle: "expanded",
				Position: schema.Position{X: 100, Y: 100},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(expanded.Source.Graphs[0].Calls) != 0 ||
			len(expanded.Source.Graphs[0].Nodes) != 1 ||
			len(expanded.Source.Graphs) != 2 {
			t.Fatalf("expanded source = %#v", expanded.Source.Graphs)
		}
	})

	t.Run("cascade", func(t *testing.T) {
		withWrapper, err := engine.Apply(created.Source, []authoring.Command{
			{Kind: authoring.CommandAddGraph, AddGraph: &authoring.AddGraphCommand{Graph: schema.Graph{
				ID: "wrapper", Name: "Wrapper", Kind: schema.GraphKindSubgraph,
				Nodes: []schema.Node{}, Calls: []schema.GraphCall{}, Edges: []schema.Edge{},
				Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
				Entries: []schema.Endpoint{}, Exits: []schema.GraphExit{}, Annotations: []schema.Annotation{},
			}}},
			{Kind: authoring.CommandAddGraphCall, AddGraphCall: &authoring.GraphCallCommand{
				GraphID: "wrapper",
				Call: schema.GraphCall{
					ID: "nested-child", GraphID: "child",
					Position: schema.Position{}, Bindings: map[string]schema.InputBinding{},
				},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		removed, err := engine.Apply(withWrapper.Source, []authoring.Command{
			{Kind: authoring.CommandRemoveGraphCall, RemoveGraphCall: &authoring.CallCommand{
				GraphID: "main", CallID: "call-child",
			}},
			{Kind: authoring.CommandRemoveGraphCall, RemoveGraphCall: &authoring.CallCommand{
				GraphID: "wrapper", CallID: "nested-child",
			}},
			{Kind: authoring.CommandRemoveGraph, RemoveGraph: &authoring.GraphCommand{GraphID: "child"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(removed.Source.Graphs) != 2 ||
			removed.Source.Graphs[0].ID != "main" ||
			removed.Source.Graphs[1].ID != "wrapper" {
			t.Fatalf("cascade source = %#v", removed.Source.Graphs)
		}
	})
}

func TestEngineUsesInstructionSignalChannels(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"delay", "retry"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.DelayNodeID, Handle: "delay"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.RetryNodeID, Handle: "retry"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{
			Channel: schema.EdgeError,
			From:    schema.Endpoint{NodeID: "$delay", PortID: "failed"},
			To:      schema.Endpoint{NodeID: "$retry", PortID: "retry"},
		})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Source.Graphs[0].Edges; len(got) != 1 || got[0].Channel != schema.EdgeError {
		t.Fatalf("error route = %#v", got)
	}

	_, err = engine.Apply(result.Source, []authoring.Command{{
		Kind: authoring.CommandConnect,
		Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{
			Channel: schema.EdgeExec,
			From:    schema.Endpoint{NodeID: "delay", PortID: "done"},
			To:      schema.Endpoint{NodeID: "retry", PortID: "retry"},
		})},
	}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "INVALID_EDGE" {
		t.Fatalf("wrong-channel error = %#v", err)
	}
}

func TestEngineConnectsEffectToNewTypedSwitch(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"delay", "switch"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.DelayNodeID, Handle: "delay"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.SwitchNodeID, Handle: "switch"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{
			Channel: schema.EdgeExec,
			From:    schema.Endpoint{NodeID: "$delay", PortID: "done"},
			To:      schema.Endpoint{NodeID: "$switch", PortID: "in"},
		})}},
	})
	if err != nil {
		t.Fatalf("connect run root to switch: %v", err)
	}
	if got := result.Source.Graphs[0].Edges; len(got) != 1 || got[0].To.PortID != "in" {
		t.Fatalf("switch edge = %#v", got)
	}
}

func TestEngineEditsStateNodesAndDisconnectsAtomically(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"node-read", "node-concat", "node-delay"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	edge := schema.Edge{
		Channel: schema.EdgeData,
		From:    schema.Endpoint{NodeID: "$read", PortID: "result"},
		To:      schema.Endpoint{NodeID: "$concat", PortID: "a"},
	}
	result, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandRenameWorkflow, RenameWorkflow: &authoring.RenameWorkflowCommand{Name: "  Stateful workflow  "}},
		{Kind: authoring.CommandAddStateVariable, AddStateVariable: &authoring.AddStateVariableCommand{
			Name: "message", Type: datatype.RefExpression(builtins.StringType.TypeRef()), Default: "hello",
		}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.StateReadNodeID, Handle: "read"}},
		{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "$read", FieldID: "variable", Value: "message"}},
		{Kind: authoring.CommandMoveNode, MoveNode: &authoring.MoveNodeCommand{GraphID: "main", NodeID: "$read", Position: schema.Position{X: 12, Y: 34}}},
		{Kind: authoring.CommandSetNodeLabel, SetNodeLabel: &authoring.SetNodeLabelCommand{GraphID: "main", NodeID: "$read", Label: "Read message"}},
		{Kind: authoring.CommandSetNodeDisabled, SetNodeDisabled: &authoring.SetNodeDisabledCommand{GraphID: "main", NodeID: "$read", Disabled: true}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "concat"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.DelayNodeID, Handle: "delay"}},
		{Kind: authoring.CommandBindDefault, BindDefault: &authoring.PortCommand{GraphID: "main", NodeID: "$delay", PortID: "duration-milliseconds"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(edge)}},
		{Kind: authoring.CommandDisconnect, Disconnect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(edge)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.Workflow.Name != "Stateful workflow" || len(result.Source.Variables) != 1 || len(result.Source.Graphs[0].Edges) != 0 {
		t.Fatalf("patched source = %#v", result.Source)
	}
	read := result.Source.Graphs[0].Nodes[0]
	if read.ID != "node-read" || read.Position.X != 12 || read.Label != "Read message" || !read.Disabled || read.Config["variable"] != "message" {
		t.Fatalf("state read node = %#v", read)
	}

	removed, err := engine.Apply(result.Source, []authoring.Command{
		{Kind: authoring.CommandRemoveNode, RemoveNode: &authoring.NodeCommand{GraphID: "main", NodeID: "node-read"}},
		{Kind: authoring.CommandRemoveNode, RemoveNode: &authoring.NodeCommand{GraphID: "main", NodeID: "node-concat"}},
		{Kind: authoring.CommandRemoveNode, RemoveNode: &authoring.NodeCommand{GraphID: "main", NodeID: "node-delay"}},
		{Kind: authoring.CommandRemoveStateVariable, RemoveStateVariable: &authoring.RemoveStateVariableCommand{Name: "message"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Source.Graphs[0].Nodes) != 0 || len(removed.Source.Variables) != 0 || removed.Source.Revision != 2 {
		t.Fatalf("removed source = %#v", removed.Source)
	}
}

func TestEngineUpdatesWorkflowMetadataWithNormalizedTags(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind: authoring.CommandUpdateWorkflowMetadata,
		UpdateWorkflowMetadata: &authoring.UpdateWorkflowMetadataCommand{
			Name: "  Daily report  ", Description: "  Exports the daily report  ",
			Category: "  Operations  ", Tags: []string{"Daily", " daily ", "Report", ""},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	metadata := result.Source.Workflow
	if metadata.Name != "Daily report" || metadata.Description != "Exports the daily report" ||
		metadata.Category != "Operations" || len(metadata.Tags) != 2 || metadata.Tags[0] != "Daily" || metadata.Tags[1] != "Report" {
		t.Fatalf("workflow metadata = %#v", metadata)
	}
	_, err = engine.Apply(result.Source, []authoring.Command{{
		Kind: authoring.CommandUpdateWorkflowMetadata,
		UpdateWorkflowMetadata: &authoring.UpdateWorkflowMetadataCommand{
			Name: "Daily report", Tags: []string{strings.Repeat("x", 129)},
		},
	}})
	var patchErr *authoring.PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != "INVALID_WORKFLOW_METADATA" {
		t.Fatalf("invalid metadata error = %#v", err)
	}
}

func TestEngineReducesReferencedStateUpdateButProtectsReferenceIntegrity(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "node-read" })
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddStateVariable, AddStateVariable: &authoring.AddStateVariableCommand{
			Name: "message", Type: datatype.RefExpression(builtins.StringType.TypeRef()), Default: "hello",
		}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.StateReadNodeID}},
		{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "node-read", FieldID: "variable", Value: "message"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := engine.Apply(result.Source, []authoring.Command{{
		Kind: authoring.CommandUpdateStateVariable,
		UpdateStateVariable: &authoring.UpdateStateVariableCommand{
			Name: "message", Type: datatype.RefExpression(builtins.IntegerType.TypeRef()), Default: 0,
		},
	}})
	if err != nil || updated.Source.Variables[0].Type.Ref == nil || *updated.Source.Variables[0].Type.Ref != builtins.IntegerType.TypeRef() {
		t.Fatalf("referenced state reduction = %#v, %v", updated.Source.Variables, err)
	}
	for _, command := range []authoring.Command{
		{Kind: authoring.CommandClearConfig, ClearConfig: &authoring.FieldCommand{GraphID: "main", NodeID: "node-read", FieldID: "variable"}},
		{Kind: authoring.CommandRemoveStateVariable, RemoveStateVariable: &authoring.RemoveStateVariableCommand{Name: "message"}},
	} {
		_, err := engine.Apply(result.Source, []authoring.Command{command})
		var patchErr *authoring.PatchError
		if !errors.As(err, &patchErr) {
			t.Fatalf("command %#v error = %v", command, err)
		}
	}
	if result.Source.Revision != 1 || len(result.Source.Variables) != 1 {
		t.Fatalf("failed mutations changed source = %#v", result.Source)
	}
}

func TestEngineUpdatesUnreferencedStateTypeAndDefault(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, nil)
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.Apply(emptySource(), []authoring.Command{{
		Kind: authoring.CommandAddStateVariable,
		AddStateVariable: &authoring.AddStateVariableCommand{
			Name: "value", Type: datatype.RefExpression(builtins.StringType.TypeRef()), Default: "",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := engine.Apply(created.Source, []authoring.Command{{
		Kind: authoring.CommandUpdateStateVariable,
		UpdateStateVariable: &authoring.UpdateStateVariableCommand{
			Name: "value", Type: datatype.RefExpression(builtins.IntegerType.TypeRef()), Default: 0,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	variable := updated.Source.Variables[0]
	if variable.Type.Ref == nil || *variable.Type.Ref != builtins.IntegerType.TypeRef() || string(variable.Default) != "0" {
		t.Fatalf("updated state = %#v", variable)
	}
}

func TestEngineRejectsInvalidCommandBoundariesWithoutPublishing(t *testing.T) {
	builtins, projection := testContracts(t)
	ids := []string{"left", "right"}
	engine, err := authoring.New(builtins.Catalog, projection, func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := engine.Apply(emptySource(), []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "left"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "right"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{
			Channel: schema.EdgeData, From: schema.Endpoint{NodeID: "$left", PortID: "result"}, To: schema.Endpoint{NodeID: "$right", PortID: "a"},
		})}},
	})
	if err != nil {
		t.Fatal(err)
	}
	edge := base.Source.Graphs[0].Edges[0]
	for _, command := range []authoring.Command{
		{Kind: authoring.CommandRenameWorkflow, RenameWorkflow: &authoring.RenameWorkflowCommand{Name: " "}},
		{Kind: authoring.CommandRemoveStateVariable, RemoveStateVariable: &authoring.RemoveStateVariableCommand{Name: "missing"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "missing", NodeTypeID: nodes.ConcatNodeID}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: "https://schemas.example.test/missing", Handle: "node"}},
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.ConcatNodeID, Handle: "bad handle"}},
		{Kind: authoring.CommandMoveNode, MoveNode: &authoring.MoveNodeCommand{GraphID: "main", NodeID: "left", Position: schema.Position{X: math.NaN()}}},
		{Kind: authoring.CommandSetNodeLabel, SetNodeLabel: &authoring.SetNodeLabelCommand{GraphID: "main", NodeID: "left", Label: strings.Repeat("x", 1025)}},
		{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "left", FieldID: "missing", Value: true}},
		{Kind: authoring.CommandBindValue, BindValue: &authoring.BindValueCommand{GraphID: "main", NodeID: "left", PortID: "missing", Value: true}},
		{Kind: authoring.CommandBindDefault, BindDefault: &authoring.PortCommand{GraphID: "main", NodeID: "left", PortID: "a"}},
		{Kind: authoring.CommandClearBinding, ClearBinding: &authoring.PortCommand{GraphID: "main", NodeID: "left", PortID: "missing"}},
		{Kind: authoring.CommandConnect, Connect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(edge)}},
		{Kind: authoring.CommandDisconnect, Disconnect: &authoring.EdgeCommand{GraphID: "main", Edge: patchEdge(schema.Edge{
			Channel: schema.EdgeData, From: schema.Endpoint{NodeID: "left", PortID: "result"}, To: schema.Endpoint{NodeID: "right", PortID: "b"},
		})}},
		{Kind: authoring.CommandRemoveNode, RemoveNode: &authoring.NodeCommand{GraphID: "main", NodeID: "missing"}},
		{Kind: authoring.CommandAddGraph, AddGraph: &authoring.AddGraphCommand{Graph: schema.Graph{ID: "main", Kind: schema.GraphKindSubgraph}}},
		{Kind: authoring.CommandAddGraph, AddGraph: &authoring.AddGraphCommand{Graph: schema.Graph{ID: "invalid-kind", Kind: schema.GraphKindMain}}},
		{Kind: authoring.CommandRenameGraph, RenameGraph: &authoring.RenameGraphCommand{GraphID: "missing", Name: "Missing"}},
		{Kind: authoring.CommandRenameGraph, RenameGraph: &authoring.RenameGraphCommand{GraphID: "main", Name: strings.Repeat("x", 257)}},
		{Kind: authoring.CommandRemoveGraph, RemoveGraph: &authoring.GraphCommand{GraphID: "main"}},
		{Kind: authoring.CommandRemoveGraph, RemoveGraph: &authoring.GraphCommand{GraphID: "missing"}},
		{Kind: authoring.CommandUpdateGraphInterface, UpdateGraphInterface: &authoring.GraphInterfaceCommand{GraphID: "missing"}},
		{Kind: authoring.CommandUpdateGraphInterface, UpdateGraphInterface: &authoring.GraphInterfaceCommand{GraphID: "main"}},
		{Kind: authoring.CommandAddGraphCall, AddGraphCall: &authoring.GraphCallCommand{GraphID: "missing", Call: schema.GraphCall{ID: "call", GraphID: "missing", Bindings: map[string]schema.InputBinding{}}}},
		{Kind: authoring.CommandAddGraphCall, AddGraphCall: &authoring.GraphCallCommand{GraphID: "main", Call: schema.GraphCall{ID: "call", GraphID: "missing", Bindings: map[string]schema.InputBinding{}}}},
		{Kind: authoring.CommandUpdateGraphCall, UpdateGraphCall: &authoring.GraphCallCommand{GraphID: "main", Call: schema.GraphCall{ID: "missing", GraphID: "missing", Bindings: map[string]schema.InputBinding{}}}},
		{Kind: authoring.CommandRemoveGraphCall, RemoveGraphCall: &authoring.CallCommand{GraphID: "main", CallID: "missing"}},
		{Kind: authoring.CommandAddAnnotation, AddAnnotation: &authoring.AnnotationCommand{GraphID: "missing", Annotation: schema.Annotation{ID: "note"}}},
		{Kind: authoring.CommandUpdateAnnotation, UpdateAnnotation: &authoring.AnnotationCommand{GraphID: "main", Annotation: schema.Annotation{ID: "missing"}}},
		{Kind: authoring.CommandRemoveAnnotation, RemoveAnnotation: &authoring.AnnotationIDCommand{GraphID: "main", AnnotationID: "missing"}},
		{Kind: authoring.CommandSetEdgeReroutes, SetEdgeReroutes: &authoring.SetEdgeReroutesCommand{GraphID: "missing", Edge: patchEdge(edge)}},
		{Kind: authoring.CommandSetEdgeReroutes, SetEdgeReroutes: &authoring.SetEdgeReroutesCommand{GraphID: "main", Edge: patchEdge(schema.Edge{Channel: schema.EdgeData, From: schema.Endpoint{NodeID: "left", PortID: "result"}, To: schema.Endpoint{NodeID: "right", PortID: "b"}})}},
	} {
		_, err := engine.Apply(base.Source, []authoring.Command{command})
		var patchErr *authoring.PatchError
		if !errors.As(err, &patchErr) {
			t.Fatalf("command %#v error = %v", command, err)
		}
	}
	if base.Source.Revision != 1 || len(base.Source.Graphs[0].Nodes) != 2 || len(base.Source.Graphs[0].Edges) != 1 {
		t.Fatalf("rejected commands mutated base = %#v", base.Source)
	}
}

func testContracts(t *testing.T) (nodes.Builtins, nodeauthoring.Snapshot) {
	t.Helper()
	builtins, err := nodes.Build()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := nodeauthoring.Project(nodeauthoring.Input{
		Catalog: builtins.Catalog, Types: builtins.Types, Capabilities: builtins.Capabilities,
		Contracts: builtins.Contracts, GeneratorVersion: nodes.GeneratorVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	return builtins, projection
}

func emptySource() schema.WorkflowSource {
	return schema.WorkflowSource{
		Format: schema.Format, Version: schema.Version,
		Workflow: schema.Workflow{ID: "workflow-authoring", Name: "Authoring"},
		Revision: 0, EntryGraph: "main",
		Graphs: []schema.Graph{{
			ID: "main", Kind: schema.GraphKindMain, Nodes: []schema.Node{}, Edges: []schema.Edge{}, Inputs: []schema.GraphPort{}, Outputs: []schema.GraphPort{},
		}},
		Resources: []schema.WorkflowResource{}, TargetProfileDefinitions: []schema.TargetProfileDefinition{},
		CredentialRequirements: []schema.CredentialRequirement{}, Dependencies: []schema.NodePackageDependency{}, Variables: []schema.Variable{},
	}
}

func patchEdge(edge schema.Edge) authoring.PatchEdge {
	return authoring.PatchEdgeFromSource(edge)
}
func TestEngineRestoresInheritedWindowSlotAndAllowsLaterOverride(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "window" })
	if err != nil {
		t.Fatal(err)
	}
	source := emptySource()
	source.TargetDefaults = []schema.TargetDefault{{Target: "target", Slot: "desktop"}}
	created, err := engine.Apply(source, []authoring.Command{
		{Kind: authoring.CommandAddNode, AddNode: &authoring.AddNodeCommand{GraphID: "main", NodeTypeID: nodes.MoveResizeWindowNodeID, Handle: "window"}},
		{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "$window", FieldID: "slot", Value: "other"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := engine.Apply(created.Source, []authoring.Command{{Kind: authoring.CommandClearConfig, ClearConfig: &authoring.FieldCommand{GraphID: "main", NodeID: "window", FieldID: "slot"}}})
	if err != nil {
		t.Fatalf("restore inheritance: %v", err)
	}
	if _, ok := restored.Source.Graphs[0].Nodes[0].Config["slot"]; ok {
		t.Fatal("inheritance must remove the override")
	}
	_, err = engine.Apply(created.Source, []authoring.Command{
		{Kind: authoring.CommandClearConfig, ClearConfig: &authoring.FieldCommand{GraphID: "main", NodeID: "window", FieldID: "slot"}},
		{Kind: authoring.CommandSetConfig, SetConfig: &authoring.SetConfigCommand{GraphID: "main", NodeID: "window", FieldID: "slot", Value: "third"}},
	})
	if err != nil {
		t.Fatalf("correcting an unsaved restore: %v", err)
	}
}

func TestParameterLayoutPatchPersistsPresentationWithoutRuntimeState(t *testing.T) {
	builtins, projection := testContracts(t)
	engine, err := authoring.New(builtins.Catalog, projection, func() string { return "unused" })
	if err != nil {
		t.Fatal(err)
	}
	source := emptySource()
	result, err := engine.Apply(source, []authoring.Command{{Kind: authoring.CommandSetParameterBlocks, SetParameterBlocks: &authoring.SetParameterBlocksCommand{Blocks: []schema.ParameterBlock{{ID: "blank", Kind: "label", Description: "Heading help"}, {ID: "line", Kind: "separator", Order: 1}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.ParameterBlocks[0].Description != "Heading help" || len(result.Source.ParameterBlocks) != 2 || len(result.Source.Variables) != 0 || len(source.ParameterBlocks) != 0 {
		t.Fatal("presentation patch mutated runtime state or original source")
	}
	raw, err := json.Marshal(result.Source)
	if err != nil {
		t.Fatal(err)
	}
	_, diagnostics := schema.ParseSource(raw)
	if schema.HasErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
}
