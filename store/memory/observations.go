package memory

import (
	"encoding/json"
	"github.com/fiumaralabs/lwm2m/store"
	"maps"
	"slices"
	"sort"
	"sync"
)

// Observations has a usable zero value.
type Observations struct {
	mu      sync.Mutex
	records map[string]map[string]store.Observation
}

func cloneObservation(v store.Observation) store.Observation {
	v.Paths = slices.Clone(v.Paths)
	v.Attributes = maps.Clone(v.Attributes)
	for k, x := range v.Attributes {
		v.Attributes[k] = slices.Clone(x)
	}
	if v.EstablishedAt != nil {
		at := *v.EstablishedAt
		v.EstablishedAt = &at
	}
	if v.LastNotificationAt != nil {
		at := *v.LastNotificationAt
		v.LastNotificationAt = &at
	}
	v.Error = slices.Clone(v.Error)
	return v
}

func (s *Observations) Create(tenant string, v store.Observation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.Tenant != tenant {
		return store.ErrTenant
	}
	if s.records[tenant] != nil {
		if _, ok := s.records[tenant][v.ID]; ok {
			return store.ErrExists
		}
	}
	if s.records == nil {
		s.records = make(map[string]map[string]store.Observation)
	}
	if s.records[tenant] == nil {
		s.records[tenant] = make(map[string]store.Observation)
	}
	s.records[tenant][v.ID] = cloneObservation(v)
	return nil
}
func (s *Observations) Get(tenant, id string) (store.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[tenant][id]
	if !ok {
		return v, store.ErrNotFound
	}
	return cloneObservation(v), nil
}
func (s *Observations) List(tenant, device string) ([]store.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []store.Observation{}
	for _, v := range s.records[tenant] {
		if v.Device == device {
			out = append(out, cloneObservation(v))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

var _ store.Observations = (*Observations)(nil)

func (s *Observations) UpdateAttributes(tenant, id string, a map[string]json.RawMessage) (store.Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[tenant][id]
	if !ok {
		return v, store.ErrNotFound
	}
	v.Attributes = a
	v = cloneObservation(v)
	s.records[tenant][id] = v
	return cloneObservation(v), nil
}
func (s *Observations) End(tenant, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.records[tenant][id]
	if !ok {
		return store.ErrNotFound
	}
	if v.Source == "profile" {
		return store.ErrObservationProfile
	}
	v.Status = "ended"
	s.records[tenant][id] = v
	return nil
}
