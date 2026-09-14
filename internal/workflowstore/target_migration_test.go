package workflowstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	"github.com/yottaapp/yotta/internal/workflow/schema"
)

func targetMigrationFixture(t *testing.T) schema.WorkflowSource {
	t.Helper()
	source, ds := schema.ParseSource(currentMigrationTestSource(t))
	if schema.HasErrors(ds) {
		t.Fatal(ds)
	}
	source.Version = "4"
	source.TargetDefaults = []schema.TargetDefault{{Target: "application", Slot: "author-launcher"}, {Target: "target", Slot: "author-game"}}
	ids := []string{"automation/activate-window", "navigation/follow-saved-path", "application/launch", "network/http-get", "panels/show", "custom/plugin"}
	for i, id := range ids {
		slot := "author-game"
		if i == 2 {
			slot = "author-launcher"
		}
		if i >= 3 {
			slot = "untouched"
		}
		source.Graphs[0].Nodes = append(source.Graphs[0].Nodes, schema.Node{
			ID: strings.ReplaceAll(id, "/", "-"), NodeRef: nodecontract.NodeRef{NodeTypeID: "https://schemas.yotta.dev/nodes/" + id, Version: "1.0.0", SemanticDigest: artifact.Digest("sha256:" + strings.Repeat("a", 64))},
			Config: map[string]any{"slot": slot, "precise": json.Number("1234567890123456")}, Bindings: map[string]schema.InputBinding{},
		})
	}
	source.Resources = []schema.WorkflowResource{{ID: "template", Kind: schema.ResourceImage, Name: "Template", Image: &schema.ImageResource{Variants: []schema.ImageResourceVariant{{ID: "original", Resolution: [2]int{10, 10}, BBox: [4]int{0, 0, 10, 10}, Blob: blob.BlobRef{MediaType: "image/png", Digest: artifact.Digest("sha256:" + strings.Repeat("b", 64)), Size: 3}}}}}}
	return source
}
func targetMigrationJSON(t *testing.T, source schema.WorkflowSource) []byte {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTargetMigrationPortableArtifactAndReleasedNodeInventory(t *testing.T) {
	before := targetMigrationFixture(t)
	raw := targetMigrationJSON(t, before)
	migrated, changed, err := MigrateSourceArtifact(raw)
	if err != nil || !changed {
		t.Fatalf("migrate: %v %v", changed, err)
	}
	after, ds := schema.ParseSource(migrated)
	if schema.HasErrors(ds) {
		t.Fatal(ds)
	}
	if bytes.Contains(migrated, []byte("author-game")) || bytes.Contains(migrated, []byte("author-launcher")) || bytes.Contains(migrated, []byte(schema.TargetParameterPrefix)) {
		t.Fatal("portable artifact contains machine bindings")
	}
	if len(after.Targets) != 2 || after.Targets[0].Kind != "configured-application" || !after.Targets[1].Default {
		t.Fatalf("targets: %+v", after.Targets)
	}
	if !reflect.DeepEqual(after.Workflow, before.Workflow) || after.Revision != before.Revision || !reflect.DeepEqual(after.Resources, before.Resources) {
		t.Fatal("migration changed identity or resources")
	}
	for i, node := range after.Graphs[0].Nodes {
		old := before.Graphs[0].Nodes[i]
		if node.NodeRef != old.NodeRef || node.ID != old.ID {
			t.Fatal("node identity changed")
		}
		if i < 3 && node.Config["slot"] == old.Config["slot"] {
			t.Fatal("built-in reference not migrated")
		}
		if i >= 3 && node.Config["slot"] != old.Config["slot"] {
			t.Fatal("unrelated/plugin slot rewritten")
		}
	}
	second, changed, err := MigrateSourceArtifact(migrated)
	if err != nil || changed || !bytes.Equal(migrated, second) {
		t.Fatal("artifact migration is not idempotent")
	}
	// The bundle read seam has no local store and cannot transfer author bindings.
	receiving, _ := OpenParameterStore("")
	values, err := receiving.Load(after.Workflow.ID)
	if err != nil || len(values) != 0 {
		t.Fatal("portable migration created local overrides")
	}
}

func TestTargetMigrationRejectsMalformedLegacyBeforeRoundTrip(t *testing.T) {
	base := targetMigrationJSON(t, targetMigrationFixture(t))
	for name, raw := range map[string][]byte{
		"unknown graph":    bytes.Replace(base, []byte(`"kind":"main"`), []byte(`"kind":"main","unknown":true`), 1),
		"unknown node":     bytes.Replace(base, []byte(`"nodeRef":`), []byte(`"unknown":true,"nodeRef":`), 1),
		"unknown position": bytes.Replace(base, []byte(`"position":{"x":0,"y":0}`), []byte(`"position":{"x":0,"y":0,"z":3}`), 1),
		"duplicate key":    bytes.Replace(base, []byte(`"revision":0`), []byte(`"revision":0,"revision":1`), 1),
		"targets in v4":    bytes.Replace(base, []byte(`"version":"4"`), []byte(`"version":"4","targets":[]`), 1),
		"bad graph":        bytes.Replace(base, []byte(`"nodes":[`), []byte(`"nodes":null,"oldNodes":[`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(raw, base) {
				t.Fatal("test did not mutate fixture")
			}
			if _, _, err := MigrateSourceArtifact(raw); err == nil {
				t.Fatal("malformed old source accepted")
			}
		})
	}
}

func TestOpenSourceStoreMigratesLocalBindingsAndPreservesReferencesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	roots, err := storage.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := catalog.Open(ctx, roots)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if foundation != nil {
			foundation.Close()
		}
	}()
	before := targetMigrationFixture(t)
	raw := targetMigrationJSON(t, before)
	hash, err := artifact.Sum("yotta/test/legacy-workflow-source/v1", raw)
	if err != nil {
		t.Fatal(err)
	}
	ref := before.Resources[0].Image.Variants[0].Blob
	if err := foundation.Objects().Observe(ctx, blob.Object{Digest: ref.Digest, Size: ref.Size}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.WorkflowSourceRecord{WorkflowID: before.Workflow.ID, Name: before.Workflow.Name, Revision: 0, Hash: hash, Format: schema.Format, Version: "4", Artifact: raw, CreatedAt: now, UpdatedAt: now}
	if err := foundation.Workflows().Commit(ctx, -1, record, []catalog.WorkflowReference{{Role: "blob/000000", Blob: ref}}); err != nil {
		t.Fatal(err)
	}
	// Exercise a used workflow, not just a newly created revision zero source.
	for revision := int64(1); revision <= 3; revision++ {
		before.Revision = revision
		raw = targetMigrationJSON(t, before)
		hash, err = artifact.Sum("yotta/test/legacy-workflow-source/v1", raw)
		if err != nil {
			t.Fatal(err)
		}
		record.Revision, record.Hash, record.Artifact = revision, hash, raw
		if err := foundation.Workflows().Commit(ctx, revision-1, record, []catalog.WorkflowReference{{Role: "blob/000000", Blob: ref}}); err != nil {
			t.Fatal(err)
		}
	}
	parameters, err := OpenParameterStore(filepath.Join(root, "parameters"))
	if err != nil {
		t.Fatal(err)
	}
	// A retry must keep preexisting user changes, including explicit unbinding.
	retained := map[string]json.RawMessage{"quantity": json.RawMessage(`99`), schema.TargetParameterPrefix + legacyTargetID("author-launcher"): json.RawMessage(`""`)}
	if err := parameters.Save(before.Workflow.ID, retained); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSourceStore(foundation.Workflows(), SourceStoreOptions{MaxSources: 4}); err == nil {
		t.Fatal("migration lost bindings without local store")
	}
	original, _, _ := foundation.Workflows().Get(ctx, before.Workflow.ID)
	if original.Hash != hash {
		t.Fatal("failed preparation published source")
	}
	store, err := OpenSourceStore(foundation.Workflows(), SourceStoreOptions{MaxSources: 4, Parameters: parameters})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load(before.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.WorkflowID() != before.Workflow.ID || snapshot.Revision() != before.Revision || snapshot.Hash() == hash {
		t.Fatal("invalid migrated identity")
	}
	values, err := parameters.Load(before.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(values[schema.TargetParameterPrefix+legacyTargetID("author-game")]) != `"author-game"` || string(values["quantity"]) != "99" || string(values[schema.TargetParameterPrefix+legacyTargetID("author-launcher")]) != `""` {
		t.Fatalf("bindings: %s", values)
	}
	plan, err := foundation.Objects().PlanGC(ctx, nil, now.Add(time.Hour), 0)
	if err != nil || len(plan.Candidates) != 0 || plan.LiveCount != 1 {
		t.Fatalf("resource reference lost: %+v %v", plan, err)
	}
	if err := foundation.Close(); err != nil {
		t.Fatal(err)
	}
	foundation = nil
	reopened, err := catalog.Open(ctx, roots)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	parameters, err = OpenParameterStore(filepath.Join(root, "parameters"))
	if err != nil {
		t.Fatal(err)
	}
	store, err = OpenSourceStore(reopened.Workflows(), SourceStoreOptions{MaxSources: 4, Parameters: parameters})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.Load(before.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := parameters.Load(before.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Hash() != snapshot.Hash() || !bytes.Equal(restored.Artifact(), snapshot.Artifact()) || !reflect.DeepEqual(values, reloaded) {
		t.Fatal("restart changed migration result")
	}
	persisted, _, err := reopened.Workflows().Get(ctx, before.Workflow.ID)
	if err != nil || !persisted.CreatedAt.Equal(now) {
		t.Fatal("migration changed creation timestamp")
	}
}

// Check the migration's frozen inventory against the released contracts, not
// today's mutable catalog, so later plugins/nodes never silently change v4 reads.
func TestTargetMigrationInventoryMatchesReleasedConfiguredTargets(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/node/v4/builtin-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Nodes []struct {
			NodeRef struct {
				NodeTypeID string `json:"nodeTypeId"`
			} `json:"nodeRef"`
			Semantic struct {
				Targets []struct {
					TargetSlot string `json:"targetSlot"`
					ConfigKey  string `json:"slotConfigKey"`
				} `json:"configuredTargets"`
			} `json:"semantic"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, node := range catalog.Nodes {
		want := ""
		for _, target := range node.Semantic.Targets {
			if target.ConfigKey != "slot" {
				continue
			}
			if target.TargetSlot == "target" {
				want = "automation"
			}
			if target.TargetSlot == "application" {
				want = "configured-application"
			}
		}
		if got := legacyTargetKind(node.NodeRef.NodeTypeID); got != want {
			t.Errorf("%s: got %q want %q", node.NodeRef.NodeTypeID, got, want)
		}
	}
}
func TestTargetMigrationCreatesOptionalDefaultAndPreservesOtherDefaults(t *testing.T) {
	source := targetMigrationFixture(t)
	source.TargetDefaults = []schema.TargetDefault{{Target: "network", Slot: "local-network"}, {Target: "z-custom", Slot: "custom-slot"}}
	// Inherited nodes remain inherited; migration must not invent an override.
	delete(source.Graphs[0].Nodes[0].Config, "slot")
	migrated, _, err := MigrateSourceArtifact(targetMigrationJSON(t, source))
	if err != nil {
		t.Fatal(err)
	}
	result, ds := schema.ParseSource(migrated)
	if schema.HasErrors(ds) {
		t.Fatal(ds)
	}
	if len(result.TargetDefaults) != 3 || result.TargetDefaults[1].Target != "target" || result.TargetDefaults[0].Slot != "local-network" || result.TargetDefaults[2].Slot != "custom-slot" {
		t.Fatalf("defaults: %+v", result.TargetDefaults)
	}
	if _, exists := result.Graphs[0].Nodes[0].Config["slot"]; exists {
		t.Fatal("inheritance replaced with override")
	}
	if slot, ok := schema.TargetDefaultSlot(result, "target"); !ok || slot != schema.DefaultWorkflowTargetID {
		t.Fatal("missing declared default")
	}
}

func TestTargetMigrationPreservesFormerApplicationDefaultAsExplicitReference(t *testing.T) {
	before := targetMigrationFixture(t)
	delete(before.Graphs[0].Nodes[2].Config, "slot")
	raw, _, err := MigrateSourceArtifact(targetMigrationJSON(t, before))
	if err != nil {
		t.Fatal(err)
	}
	after, ds := schema.ParseSource(raw)
	if schema.HasErrors(ds) {
		t.Fatal(ds)
	}
	if got := after.Graphs[0].Nodes[2].Config["slot"]; got != legacyTargetID("author-launcher") {
		t.Fatalf("former application inheritance lost: %v", got)
	}
	for _, d := range after.TargetDefaults {
		if d.Target == "application" {
			t.Fatal("retained second default")
		}
	}
	if got, _ := schema.TargetDefaultSlot(after, "application"); got != legacyTargetID("author-game") {
		t.Fatalf("new nodes do not use unified default: %v", got)
	}
}
