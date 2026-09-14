package workflowstore

import (
	"github.com/yottaapp/yotta/internal/artifact"
	"os"
	"testing"
)

func TestKnownSourceMigrationRequiresExactUneditedChain(t *testing.T) {
	root := t.TempDir()
	digest := func(s string) artifact.Digest {
		d, err := artifact.Sum("yotta/test/migration/v1", []byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	before, middle, after, edit := digest("before"), digest("middle"), digest("after"), digest("edit")
	if err := recordSourceMigration(root, "workflow", before, middle); err != nil {
		t.Fatal(err)
	}
	if err := recordSourceMigration(root, "workflow", middle, after); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id       string
		from, to artifact.Digest
		want     bool
	}{
		{"workflow", before, after, true}, {"workflow", before, edit, false}, {"workflow", edit, after, false}, {"other", before, after, false},
	} {
		got, err := KnownSourceMigration(root, tc.id, tc.from, tc.to)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: %v %v", tc, got, err)
		}
	}
	// A pending receipt is harmless before the corresponding catalog CAS.
	got, err := KnownSourceMigration(root, "workflow", middle, before)
	if err != nil || got {
		t.Fatalf("pending CAS: %v %v", got, err)
	}
	// Retry and process restart read the same durable receipt.
	if err := recordSourceMigration(root, "workflow", before, middle); err != nil {
		t.Fatal(err)
	}
	if got, err := KnownSourceMigration(root, "workflow", before, after); err != nil || !got {
		t.Fatalf("retry: %v %v", got, err)
	}
	if err := os.WriteFile(migrationReceiptPath(root, "workflow", before), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := KnownSourceMigration(root, "workflow", before, after); err == nil {
		t.Fatal("corrupt history accepted")
	}
}
