package workflowstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/yottaapp/yotta/internal/durablefs"
)

// ParameterStore owns local overrides only. A complete replacement is atomic;
// readers always receive detached bytes, including concurrent queued runs.
type ParameterStore struct {
	mu     sync.Mutex
	root   string
	memory map[string]map[string]json.RawMessage
}

func OpenParameterStore(root string) (*ParameterStore, error) {
	if root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			return nil, err
		}
	}
	return &ParameterStore{root: root, memory: map[string]map[string]json.RawMessage{}}, nil
}

func (s *ParameterStore) path(id string) string {
	hash := sha256.Sum256([]byte(id))
	return filepath.Join(s.root, hex.EncodeToString(hash[:])+".json")
}

func (s *ParameterStore) Load(id string) (map[string]json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]json.RawMessage{}
	if s.root == "" {
		for key, value := range s.memory[id] {
			result[key] = append(json.RawMessage(nil), value...)
		}
		return result, nil
	}
	raw, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, errors.New("parameter configuration exceeds byte budget")
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = map[string]json.RawMessage{}
	}
	return result, nil
}

func (s *ParameterStore) Save(id string, values map[string]json.RawMessage) error {
	if id == "" || len(values) > 4096 {
		return errors.New("invalid parameter configuration")
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if len(raw) > 4<<20 {
		return errors.New("parameter configuration exceeds byte budget")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root != "" {
		return durablefs.WriteFile(s.path(id), raw, 0600)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	s.memory[id] = snapshot
	return nil
}
