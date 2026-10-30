package apiv1

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

// DeviceProfile contains the resource representation. Nested JSON is stored as
// owned snapshots so callers can safely mutate their copies.
type DeviceProfile struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Observations      json.RawMessage `json:"observations,omitempty"`
	Attributes        json.RawMessage `json:"attributes,omitempty"`
	OnRegister        json.RawMessage `json:"onRegister,omitempty"`
	Timeseries        json.RawMessage `json:"timeseries,omitempty"`
	BootstrapTemplate json.RawMessage `json:"bootstrapTemplate,omitempty"`
}

// DeviceProfileStore provides tenant-isolated profile metadata operations.
// Replace must only succeed for an existing resource in the given tenant.
// Delete returns inUse when a device references the profile.
type DeviceProfileStore interface {
	Create(tenant string, profile DeviceProfile) error
	Replay(tenant, key string) (DeviceProfile, bool)
	CreateIdempotent(tenant string, profile DeviceProfile, key string) (DeviceProfile, error)
	List(tenant string) []DeviceProfile
	Get(tenant, id string) (DeviceProfile, bool)
	Replace(tenant string, profile DeviceProfile) bool
	Delete(tenant, id string) (found, inUse bool)
}

type memoryDeviceProfileStore struct {
	mu      sync.RWMutex
	items   map[string]map[string]DeviceProfile
	replays map[[2]string]profileReplay
}

type profileReplay struct {
	profile DeviceProfile
	expires time.Time
}

// NewMemoryDeviceProfileStore creates an ephemeral store. In-use detection is
// deferred until devices can reference profiles.
func NewMemoryDeviceProfileStore() DeviceProfileStore {
	return &memoryDeviceProfileStore{items: make(map[string]map[string]DeviceProfile), replays: make(map[[2]string]profileReplay)}
}

func cloneDeviceProfile(p DeviceProfile) DeviceProfile {
	p.Observations = append(json.RawMessage(nil), p.Observations...)
	p.Attributes = append(json.RawMessage(nil), p.Attributes...)
	p.OnRegister = append(json.RawMessage(nil), p.OnRegister...)
	p.Timeseries = append(json.RawMessage(nil), p.Timeseries...)
	p.BootstrapTemplate = append(json.RawMessage(nil), p.BootstrapTemplate...)
	return p
}

func (s *memoryDeviceProfileStore) Create(tenant string, p DeviceProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[tenant] == nil {
		s.items[tenant] = make(map[string]DeviceProfile)
	}
	if _, exists := s.items[tenant][p.ID]; exists {
		return errors.New("device profile already exists")
	}
	s.items[tenant][p.ID] = cloneDeviceProfile(p)
	return nil
}
func (s *memoryDeviceProfileStore) Replay(tenant, key string) (DeviceProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.replays[[2]string{tenant, key}]
	if key == "" || !ok || !time.Now().Before(r.expires) {
		return DeviceProfile{}, false
	}
	return cloneDeviceProfile(r.profile), true
}
func (s *memoryDeviceProfileStore) CreateIdempotent(tenant string, p DeviceProfile, key string) (DeviceProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	k := [2]string{tenant, key}
	if r, ok := s.replays[k]; key != "" && ok && now.Before(r.expires) {
		return cloneDeviceProfile(r.profile), nil
	}
	if s.items[tenant] == nil {
		s.items[tenant] = make(map[string]DeviceProfile)
	}
	if _, ok := s.items[tenant][p.ID]; ok {
		return DeviceProfile{}, errors.New("device profile already exists")
	}
	p = cloneDeviceProfile(p)
	s.items[tenant][p.ID] = p
	for k, r := range s.replays {
		if !now.Before(r.expires) {
			delete(s.replays, k)
		}
	}
	if key != "" {
		s.replays[k] = profileReplay{cloneDeviceProfile(p), now.Add(24 * time.Hour)}
	}
	return cloneDeviceProfile(p), nil
}
func (s *memoryDeviceProfileStore) Get(tenant, id string) (DeviceProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.items[tenant][id]
	return cloneDeviceProfile(p), ok
}
func (s *memoryDeviceProfileStore) List(tenant string) []DeviceProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DeviceProfile, 0, len(s.items[tenant]))
	for _, p := range s.items[tenant] {
		out = append(out, cloneDeviceProfile(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *memoryDeviceProfileStore) Replace(tenant string, p DeviceProfile) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[tenant][p.ID]; !ok {
		return false
	}
	s.items[tenant][p.ID] = cloneDeviceProfile(p)
	return true
}
func (s *memoryDeviceProfileStore) Delete(tenant, id string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[tenant][id]; !ok {
		return false, false
	}
	delete(s.items[tenant], id)
	return true, false
}
