package memory

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

// State holds isolated snapshots in memory. Its zero value is usable.
type State struct {
	mu     sync.RWMutex
	values map[[3]string]map[string]store.StateValue
}

var _ store.State = (*State)(nil)

func under(path, root string) bool {
	return root == "/" || path == root || strings.HasPrefix(path, root+"/")
}

func (s *State) Record(o operations.Operation) error {
	if o.Status != operations.Succeeded || o.CompletedAt == nil {
		return nil
	}
	var result struct {
		Values []store.StateValue `json:"values"`
	}
	if o.Type != "read" && o.Type != "write" && o.Type != "delete" && o.Type != "notification" {
		return nil
	}
	if o.Type != "delete" {
		if err := json.Unmarshal(o.Result, &result); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[[3]string]map[string]store.StateValue{}
	}
	if o.Type == "delete" {
		var q struct{ Path string }
		if err := json.Unmarshal(o.Request, &q); err != nil {
			return err
		}
		for p := range s.values[[3]string{o.Tenant, o.Device, o.Prefix}] {
			if under(p, q.Path) {
				delete(s.values[[3]string{o.Tenant, o.Device, o.Prefix}], p)
			}
		}
		return nil
	}
	for _, v := range result.Values {
		if v.Type == "" || len(v.Value) == 0 {
			continue
		}
		if v.Prefix == "" {
			v.Prefix = o.Prefix
		}
		v.Source, v.ReceivedAt = o.Type, *o.CompletedAt
		key := [3]string{o.Tenant, o.Device, v.Prefix}
		if s.values[key] == nil {
			s.values[key] = map[string]store.StateValue{}
		}
		s.values[key][v.Path] = v
	}
	return nil
}

func (s *State) List(tenant, device, prefix string, paths []string) ([]store.StateValue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	values := []store.StateValue{}
	for _, v := range s.values[[3]string{tenant, device, prefix}] {
		match := len(paths) == 0
		for _, p := range paths {
			match = match || under(v.Path, p)
		}
		if !match {
			continue
		}
		v.Value = append(json.RawMessage(nil), v.Value...)
		if v.Timestamp != nil {
			at := *v.Timestamp
			v.Timestamp = &at
		}
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Path < values[j].Path })
	return values, nil
}
