package targetruntime

import "fmt"

// Bind aliases once per Run. Adapter operations retain their direct lookup path.
// Unbound roles cannot fall through to an identically named device slot.
func (s Snapshot) Bind(roles []string, bindings map[string]string) (Snapshot, error) {
	if !s.Valid() {
		return Snapshot{}, fmt.Errorf("configured target snapshot is unavailable")
	}
	items := map[string]Installation{}
	for slot, item := range s.state.bySlot {
		items[slot] = item
	}
	for _, role := range roles {
		delete(items, role)
		slot := bindings[role]
		if slot == "" {
			continue
		}
		item, ok := s.state.bySlot[slot]
		if !ok {
			return Snapshot{}, fmt.Errorf("configured target %q does not exist", slot)
		}
		item.Slot = role
		items[role] = item
	}
	all := make([]Installation, 0, len(items))
	for _, item := range items {
		all = append(all, item)
	}
	return NewSnapshot(all)
}
