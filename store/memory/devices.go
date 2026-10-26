// Package memory provides in-memory native API stores.
package memory

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// Devices stores tenant-isolated metadata. Its zero value is usable.
type Devices struct {
	mu      sync.RWMutex
	records map[string]map[string]store.Device
}

var _ store.Devices = (*Devices)(nil)

func clone(d store.Device) store.Device {
	d.Labels = maps.Clone(d.Labels)
	if d.LastSeenAt != nil {
		at := *d.LastSeenAt
		d.LastSeenAt = &at
	}
	return d
}
func (s *Devices) Create(tenant string, d store.Device) (store.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = map[string]map[string]store.Device{}
	}
	if s.records[tenant] == nil {
		s.records[tenant] = map[string]store.Device{}
	}
	if _, ok := s.records[tenant][d.Endpoint]; ok {
		return store.Device{}, store.ErrDeviceExists
	}
	d.CreatedAt = time.Now().UTC()
	d.UpdatedAt = d.CreatedAt
	d.LastSeenAt = nil
	if d.Labels == nil {
		d.Labels = map[string]string{}
	}
	s.records[tenant][d.Endpoint] = clone(d)
	return clone(d), nil
}
func (s *Devices) Get(tenant, ep string) (store.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.records[tenant][ep]
	if !ok {
		return d, store.ErrDeviceNotFound
	}
	return clone(d), nil
}
func (s *Devices) Delete(tenant, ep string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[tenant][ep]; !ok {
		return store.ErrDeviceNotFound
	}
	delete(s.records[tenant], ep)
	return nil
}
func (s *Devices) Seen(tenant, ep string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.records[tenant][ep]
	if ok && (d.LastSeenAt == nil || at.After(*d.LastSeenAt)) {
		at = at.UTC()
		d.LastSeenAt = &at
		s.records[tenant][ep] = d
	}
}

type deviceCursor struct{ Tenant, Q, Status, After string }

func (s *Devices) List(tenant string, f store.DeviceFilter, cursor string, limit int) ([]store.Device, string, error) {
	c := deviceCursor{Tenant: tenant, Q: f.Q, Status: f.Status}
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.Q != f.Q || c.Status != f.Status || c.After == "" {
			return nil, "", store.ErrDeviceCursor
		}
	}
	if limit < 1 || limit > 500 {
		return nil, "", store.ErrDeviceCursor
	}
	s.mu.RLock()
	items := make([]store.Device, 0, len(s.records[tenant]))
	for _, d := range s.records[tenant] {
		items = append(items, clone(d))
	}
	s.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].Endpoint < items[j].Endpoint })
	out := make([]store.Device, 0)
	next := ""
	for _, d := range items {
		if d.Endpoint <= c.After || (!strings.Contains(d.Endpoint, f.Q) && !strings.Contains(d.Name, f.Q)) || (f.Status != "" && (f.StatusOf == nil || f.StatusOf(d) != f.Status)) {
			continue
		}
		if len(out) == limit {
			c.After = out[len(out)-1].Endpoint
			b, _ := json.Marshal(c)
			next = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		out = append(out, d)
	}
	return out, next, nil
}
