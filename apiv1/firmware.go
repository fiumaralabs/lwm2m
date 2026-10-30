package apiv1

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Firmware describes an artifact whose image may be uploaded later.
type Firmware struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Status      string            `json:"status"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// FirmwarePatch contains only mutable fields.
type FirmwarePatch struct {
	Name        *string            `json:"name"`
	Description *string            `json:"description"`
	Labels      *map[string]string `json:"labels"`
}

// FirmwareStore provides tenant-isolated metadata operations. Delete returns
// inUse when an update or job references the artifact.
type FirmwareStore interface {
	Create(tenant string, artifact Firmware) error
	List(tenant string) []Firmware
	Get(tenant, id string) (Firmware, bool)
	Patch(tenant, id string, patch FirmwarePatch) (Firmware, bool)
	Delete(tenant, id string) (found, inUse bool)
}

type memoryFirmwareStore struct {
	mu    sync.RWMutex
	items map[string]map[string]Firmware
}

// NewMemoryFirmwareStore returns an ephemeral tenant-scoped firmware store.
// Its in-use result is always false until update/job storage is implemented.
func NewMemoryFirmwareStore() FirmwareStore {
	return &memoryFirmwareStore{items: make(map[string]map[string]Firmware)}
}
func cloneFirmware(f Firmware) Firmware {
	if f.Labels != nil {
		m := make(map[string]string, len(f.Labels))
		for k, v := range f.Labels {
			m[k] = v
		}
		f.Labels = m
	}
	return f
}
func (s *memoryFirmwareStore) Create(tenant string, f Firmware) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[tenant] == nil {
		s.items[tenant] = make(map[string]Firmware)
	}
	s.items[tenant][f.ID] = cloneFirmware(f)
	return nil
}
func (s *memoryFirmwareStore) List(tenant string) []Firmware {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Firmware, 0, len(s.items[tenant]))
	for _, f := range s.items[tenant] {
		out = append(out, cloneFirmware(f))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *memoryFirmwareStore) Get(tenant, id string) (Firmware, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.items[tenant][id]
	return cloneFirmware(f), ok
}
func (s *memoryFirmwareStore) Patch(tenant, id string, p FirmwarePatch) (Firmware, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.items[tenant][id]
	if !ok {
		return Firmware{}, false
	}
	if p.Name != nil {
		f.Name = *p.Name
	}
	if p.Description != nil {
		f.Description = *p.Description
	}
	if p.Labels != nil {
		f.Labels = *p.Labels
	}
	s.items[tenant][id] = cloneFirmware(f)
	return cloneFirmware(f), true
}
func (s *memoryFirmwareStore) Delete(tenant, id string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.items[tenant][id]
	if ok {
		delete(s.items[tenant], id)
	}
	return ok, false
}

func serveFirmware(s FirmwareStore, w http.ResponseWriter, r *http.Request) {
	collection := r.URL.Path == "/v1/firmware"
	id := strings.TrimPrefix(r.URL.Path, "/v1/firmware/")
	if !collection && (id == "" || strings.Contains(id, "/")) {
		WriteProblem(w, 404, "not_found", "Unknown firmware artifact")
		return
	}
	allowed := collection && (r.Method == http.MethodGet || r.Method == http.MethodPost) || !collection && (r.Method == http.MethodGet || r.Method == http.MethodPatch || r.Method == http.MethodDelete)
	if !allowed {
		if collection {
			w.Header().Set("Allow", "GET, POST")
		} else {
			w.Header().Set("Allow", "GET, PATCH, DELETE")
		}
		WriteProblem(w, 405, "method_not_allowed", "Method not allowed")
		return
	}
	scope := "firmware:write"
	if r.Method == http.MethodGet {
		scope = "devices:read"
	}
	RequireScope(scope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := TenantFrom(r.Context())
		switch r.Method {
		case http.MethodPost:
			var req struct {
				Name        string            `json:"name"`
				Version     string            `json:"version"`
				Description string            `json:"description"`
				Labels      map[string]string `json:"labels"`
			}
			if !DecodeJSON(w, r, &req) {
				return
			}
			if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Version) == "" {
				WriteProblem(w, 400, "invalid_request", "name and version are required")
				return
			}
			b := make([]byte, 16)
			if _, err := rand.Read(b); err != nil {
				WriteProblem(w, 500, "internal_error", "Cannot generate firmware ID")
				return
			}
			f := Firmware{ID: "fw_" + hex.EncodeToString(b), Name: req.Name, Version: req.Version, Description: req.Description, Labels: req.Labels, Status: "empty", CreatedAt: time.Now().UTC()}
			if err := s.Create(tenant, f); err != nil {
				WriteProblem(w, 500, "internal_error", "Cannot store firmware")
				return
			}
			deviceJSON(w, 201, f)
		case http.MethodGet:
			if collection {
				deviceJSON(w, 200, struct {
					Items []Firmware `json:"items"`
				}{s.List(tenant)})
				return
			}
			f, ok := s.Get(tenant, id)
			if !ok {
				WriteProblem(w, 404, "not_found", "Unknown firmware artifact")
				return
			}
			deviceJSON(w, 200, f)
		case http.MethodPatch:
			if _, ok := s.Get(tenant, id); !ok {
				WriteProblem(w, 404, "not_found", "Unknown firmware artifact")
				return
			}
			var p FirmwarePatch
			if !DecodeJSON(w, r, &p) {
				return
			}
			if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
				WriteProblem(w, 400, "invalid_request", "name cannot be empty")
				return
			}
			f, ok := s.Patch(tenant, id, p)
			if !ok {
				WriteProblem(w, 404, "not_found", "Unknown firmware artifact")
				return
			}
			deviceJSON(w, 200, f)
		case http.MethodDelete:
			found, inUse := s.Delete(tenant, id)
			if !found {
				WriteProblem(w, 404, "not_found", "Unknown firmware artifact")
				return
			}
			if inUse {
				WriteProblem(w, 409, "in_use", "Firmware artifact is in use")
				return
			}
			w.WriteHeader(204)
		}
	})).ServeHTTP(w, r)
}
