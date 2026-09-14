package workflowstore

import (
	"encoding/json"
	"sync"
	"testing"
)

func TestParameterStorePersistsDetachedAtomicSnapshots(t *testing.T) {
	root := t.TempDir()
	store, err := OpenParameterStore(root)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]json.RawMessage{"person": json.RawMessage(`"a"`)}
	if err := store.Save("workflow", values); err != nil {
		t.Fatal(err)
	}
	values["person"][1] = 'b'
	reopened, err := OpenParameterStore(root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Load("workflow")
	if err != nil || string(loaded["person"]) != `"a"` {
		t.Fatalf("loaded=%s error=%v", loaded, err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := store.Save("workflow", map[string]json.RawMessage{"person": json.RawMessage(`"b"`)}); err != nil {
				t.Error(err)
			}
			if _, err := store.Load("workflow"); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	other, err := store.Load("other-workflow")
	if err != nil || len(other) != 0 {
		t.Fatal("configuration leaked across workflows")
	}
	if err := store.Save("workflow", map[string]json.RawMessage{}); err != nil {
		t.Fatal(err)
	}
	reset, err := reopened.Load("workflow")
	if err != nil || len(reset) != 0 {
		t.Fatal("reset was not durable")
	}
}
