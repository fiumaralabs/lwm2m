package apiv1

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// BootstrapCredential is independent of the server's device credential.
type BootstrapCredential struct {
	Identity  string
	Key       []byte
	UpdatedAt time.Time
}

// BootstrapCredentialStore must isolate tenants and atomically enforce unique identities.
type BootstrapCredentialStore interface {
	Get(tenant, endpoint string) (BootstrapCredential, bool)
	Put(tenant, endpoint string, credential BootstrapCredential) (existed, duplicate bool)
	Delete(tenant, endpoint string) bool
}

type memoryBootstrapCredentials struct {
	mu      sync.RWMutex
	entries map[string]map[string]BootstrapCredential
}

func NewMemoryBootstrapCredentialStore() BootstrapCredentialStore {
	return &memoryBootstrapCredentials{entries: make(map[string]map[string]BootstrapCredential)}
}
func (s *memoryBootstrapCredentials) Get(tenant, endpoint string) (BootstrapCredential, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.entries[tenant][endpoint]
	c.Key = bytes.Clone(c.Key)
	return c, ok
}
func (s *memoryBootstrapCredentials) Put(tenant, endpoint string, c BootstrapCredential) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for t, devices := range s.entries {
		for ep, other := range devices {
			if (t != tenant || ep != endpoint) && other.Identity == c.Identity {
				return false, true
			}
		}
	}
	if s.entries[tenant] == nil {
		s.entries[tenant] = make(map[string]BootstrapCredential)
	}
	_, existed := s.entries[tenant][endpoint]
	c.Key = bytes.Clone(c.Key)
	c.UpdatedAt = time.Now().UTC()
	s.entries[tenant][endpoint] = c
	return existed, false
}
func (s *memoryBootstrapCredentials) Delete(tenant, endpoint string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.entries[tenant][endpoint]
	delete(s.entries[tenant], endpoint)
	return ok
}

// NewBootstrapCredentialsHandler creates the handler without wiring a route.
// Authentication must wrap this handler as it does the other v1 handlers.
func NewBootstrapCredentialsHandler(devices store.Devices, credentials BootstrapCredentialStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := "devices:read"
		if r.Method != http.MethodGet {
			scope = "bootstrap:write"
		}
		RequireScope(scope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			const prefix = "/v1/devices/"
			const suffix = "/bootstrap/credentials"
			path := r.URL.EscapedPath()
			if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
				WriteProblem(w, 404, "not_found", "Unknown device path")
				return
			}
			raw := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
			ep, err := url.PathUnescape(raw)
			if err != nil || ep == "" || strings.Contains(raw, "/") {
				WriteProblem(w, 404, "not_found", "Unknown device path")
				return
			}
			tenant := TenantFrom(r.Context())
			unlock := bootstrapLifecycle.lock(tenant)
			defer unlock()
			if _, err := devices.Get(tenant, ep); err != nil {
				if errors.Is(err, store.ErrDeviceNotFound) {
					WriteProblem(w, 404, "not_found", "Device not found")
				} else {
					WriteProblem(w, 500, "internal_error", "Cannot get device")
				}
				return
			}
			switch r.Method {
			case http.MethodGet:
				c, ok := credentials.Get(tenant, ep)
				if !ok {
					WriteProblem(w, 404, "not_found", "Credential not found")
					return
				}
				var response credentialResponse
				response.PSK.Identity = c.Identity
				response.UpdatedAt = c.UpdatedAt
				deviceJSON(w, 200, response)
			case http.MethodPut:
				identity, encoded, key, generated, ok := decodePSKCredential(w, r)
				if !ok {
					return
				}
				existed, duplicate := credentials.Put(tenant, ep, BootstrapCredential{Identity: identity, Key: key})
				if duplicate {
					WriteProblem(w, 409, "duplicate_psk_identity", "PSK identity already exists")
					return
				}
				c, _ := credentials.Get(tenant, ep)
				var response credentialResponse
				response.PSK.Identity = c.Identity
				response.UpdatedAt = c.UpdatedAt
				if generated {
					response.PSK.Key = encoded
					w.Header().Set("Cache-Control", "no-store")
				}
				status := 201
				if existed {
					status = 200
				}
				deviceJSON(w, status, response)
			case http.MethodDelete:
				if !credentials.Delete(tenant, ep) {
					WriteProblem(w, 404, "not_found", "Credential not found")
					return
				}
				w.WriteHeader(204)
			default:
				w.Header().Set("Allow", "GET, PUT, DELETE")
				WriteProblem(w, 405, "method_not_allowed", "Expected GET, PUT or DELETE")
			}
		})).ServeHTTP(w, r)
	})
}
