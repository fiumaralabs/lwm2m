package apiv1

import (
	"net/http"
	"sort"
	"strings"
	"sync"
)

// TenantLimits contains the configured, non-negative resource limits.
type TenantLimits struct {
	Devices                 int64 `json:"devices"`
	OperationsPerMinute     int64 `json:"operationsPerMinute"`
	EventsPerSecond         int64 `json:"eventsPerSecond"`
	FirmwareStorageBytes    int64 `json:"firmwareStorageBytes"`
	TimeseriesRetentionDays int64 `json:"timeseriesRetentionDays"`
}

type Tenant struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Status string       `json:"status"`
	Limits TenantLimits `json:"limits"`
}

// TenantStore provides atomic tenant mutations and snapshots.
type TenantStore interface {
	Get(string) (Tenant, bool)
	List() []Tenant
	Create(Tenant) bool
	Update(string, func(*Tenant) bool) (Tenant, bool, bool)
	Delete(string) bool
}

type memoryTenantStore struct {
	mu    sync.RWMutex
	items map[string]Tenant
}

func newTenantStore() *memoryTenantStore {
	return &memoryTenantStore{items: map[string]Tenant{"default": {ID: "default", Name: "default", Status: "active"}}}
}
func (s *memoryTenantStore) Get(id string) (Tenant, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.items[id]
	return t, ok
}
func (s *memoryTenantStore) List() []Tenant {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ts := make([]Tenant, 0, len(s.items))
	for _, t := range s.items {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
	return ts
}
func (s *memoryTenantStore) Create(t Tenant) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[t.ID]; ok {
		return false
	}
	s.items[t.ID] = t
	return true
}
func (s *memoryTenantStore) Update(id string, f func(*Tenant) bool) (Tenant, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.items[id]
	if !ok {
		return t, false, false
	}
	if !f(&t) {
		return t, true, false
	}
	s.items[id] = t
	return t, true, true
}
func (s *memoryTenantStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return false
	}
	delete(s.items, id)
	return true
}
func validTenantLimits(l TenantLimits) bool {
	return l.Devices >= 0 && l.OperationsPerMinute >= 0 && l.EventsPerSecond >= 0 && l.FirmwareStorageBytes >= 0 && l.TimeseriesRetentionDays >= 0
}

func tenantMiddleware(next http.Handler) http.Handler {
	var s TenantStore = newTenantStore()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tenant, ok := s.Get(TenantFrom(r.Context())); ok && tenant.Status == "suspended" {
			WriteProblem(w, 403, "tenant_suspended", "Tenant is suspended")
			return
		}
		if r.URL.Path == "/v1/tenants" || strings.HasPrefix(r.URL.Path, "/v1/tenants/") {
			RequireScope("platform")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveTenants(s, w, r) })).ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func serveTenants(s TenantStore, w http.ResponseWriter, r *http.Request) {
	collection := r.URL.Path == "/v1/tenants"
	id := strings.TrimPrefix(r.URL.Path, "/v1/tenants/")
	if !collection && (id == "" || strings.Contains(id, "/")) {
		WriteProblem(w, 404, "not_found", "Unknown tenant")
		return
	}
	if collection {
		switch r.Method {
		case http.MethodGet:
			deviceJSON(w, 200, map[string]any{"items": s.List()})
		case http.MethodPost:
			var t Tenant
			if !DecodeJSON(w, r, &t) {
				return
			}
			if t.ID == "" || t.Name == "" || strings.ContainsAny(t.ID, "/ ") || (t.Status != "" && t.Status != "active") {
				WriteProblem(w, 400, "invalid_tenant", "Invalid tenant")
				return
			}
			if !validTenantLimits(t.Limits) {
				WriteProblem(w, 422, "invalid_limits", "Limits must be non-negative")
				return
			}
			t.Status = "active"
			if !s.Create(t) {
				WriteProblem(w, 409, "conflict", "Tenant already exists")
				return
			}
			w.Header().Set("Location", "/v1/tenants/"+t.ID)
			deviceJSON(w, 201, t)
		default:
			w.Header().Set("Allow", "GET, POST")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET or POST")
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		t, ok := s.Get(id)
		if !ok {
			WriteProblem(w, 404, "not_found", "Unknown tenant")
			return
		}
		deviceJSON(w, 200, t)
	case http.MethodPatch:
		var p struct {
			Name   *string       `json:"name"`
			Status *string       `json:"status"`
			Limits *TenantLimits `json:"limits"`
		}
		if !DecodeJSON(w, r, &p) {
			return
		}
		if p.Limits != nil && !validTenantLimits(*p.Limits) {
			WriteProblem(w, 422, "invalid_limits", "Limits must be non-negative")
			return
		}
		t, ok, valid := s.Update(id, func(t *Tenant) bool {
			if p.Name != nil {
				if *p.Name == "" {
					return false
				}
				t.Name = *p.Name
			}
			if p.Status != nil {
				if *p.Status != "active" && *p.Status != "suspended" {
					return false
				}
				t.Status = *p.Status
			}
			if p.Limits != nil {
				t.Limits = *p.Limits
			}
			return true
		})
		if !ok {
			WriteProblem(w, 404, "not_found", "Unknown tenant")
			return
		}
		if !valid {
			WriteProblem(w, 400, "invalid_tenant", "Invalid tenant")
			return
		}
		deviceJSON(w, 200, t)
	case http.MethodDelete:
		if id == "default" {
			WriteProblem(w, 403, "forbidden", "Cannot delete default tenant")
			return
		}
		if !s.Delete(id) {
			WriteProblem(w, 404, "not_found", "Unknown tenant")
			return
		}
		w.WriteHeader(204)
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET, PATCH or DELETE")
	}
}
