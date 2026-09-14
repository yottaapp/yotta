package workflowstore

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yottaapp/yotta/internal/artifact"
	"github.com/yottaapp/yotta/internal/durablefs"
)

type sourceMigrationReceipt struct {
	WorkflowID string          `json:"workflowId"`
	Before     artifact.Digest `json:"before"`
	After      artifact.Digest `json:"after"`
}

func migrationReceiptPath(root, id string, before artifact.Digest) string {
	return filepath.Join(root, fmt.Sprintf("%x.json", sha256.Sum256([]byte(id+"\x00"+string(before)))))
}

// Written before catalog CAS: a crash leaves a retryable receipt. Consumers must
// match both the installed baseline and current catalog hash, never just the ID.
func recordSourceMigration(root, id string, before, after artifact.Digest) error {
	if root == "" {
		return nil
	}
	if !before.Valid() || !after.Valid() || id == "" {
		return errors.New("invalid source migration identity")
	}
	receipt := sourceMigrationReceipt{id, before, after}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	return durablefs.WriteFile(migrationReceiptPath(root, id, before), raw, 0600)
}

// KnownSourceMigration proves an exact sequence of store-owned schema upgrades.
// A source edit before or after migration breaks this chain and is never adopted.
func KnownSourceMigration(root, id string, before, current artifact.Digest) (bool, error) {
	if root == "" || id == "" || !before.Valid() || !current.Valid() {
		return false, nil
	}
	seen := map[artifact.Digest]bool{}
	for before != current {
		if seen[before] || len(seen) >= 64 {
			return false, nil
		}
		seen[before] = true
		raw, err := os.ReadFile(migrationReceiptPath(root, id, before))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		var receipt sourceMigrationReceipt
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return false, err
		}
		if receipt.WorkflowID != id || receipt.Before != before || !receipt.After.Valid() {
			return false, errors.New("invalid source migration receipt")
		}
		before = receipt.After
	}
	return true, nil
}
