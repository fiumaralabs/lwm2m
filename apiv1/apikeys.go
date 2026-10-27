package apiv1

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type keyMetadata struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}
type keyStore struct {
	mu       sync.Mutex
	entries  map[string]credential
	metadata map[string]keyMetadata
}
type keyStoreContext struct{}

func newKeyStore(entries map[string]credential) *keyStore {
	s := &keyStore{entries: make(map[string]credential), metadata: make(map[string]keyMetadata)}
	if entries != nil {
		s.entries = entries
	}
	for id, c := range entries {
		s.metadata[id] = keyMetadata{ID: id, Name: id, Scopes: strings.Split(strings.Trim(c.scopes, ","), ","), CreatedAt: time.Now().UTC()}
	}
	return s
}
func serveAPIKeys(w http.ResponseWriter, r *http.Request) {
	s := r.Context().Value(keyStoreContext{}).(*keyStore)
	tenant := TenantFrom(r.Context())
	collection := r.URL.Path == "/v1/api-keys"
	id := strings.TrimPrefix(r.URL.Path, "/v1/api-keys/")
	if !collection && (id == "" || strings.Contains(id, "/")) {
		WriteProblem(w, 404, "not_found", "Unknown API path")
		return
	}
	allow := "GET, POST"
	if !collection {
		allow = "DELETE"
	}
	if (collection && r.Method != "GET" && r.Method != "POST") || (!collection && r.Method != "DELETE") {
		w.Header().Set("Allow", allow)
		WriteProblem(w, 405, "method_not_allowed", "Unsupported method")
		return
	}
	switch r.Method {
	case "DELETE":
		s.mu.Lock()
		entry, ok := s.entries[id]
		if ok && entry.tenant == tenant {
			delete(s.entries, id)
			delete(s.metadata, id)
		}
		s.mu.Unlock()
		if !ok || entry.tenant != tenant {
			WriteProblem(w, 404, "not_found", "Unknown API key")
			return
		}
		w.WriteHeader(204)
	case "POST":
		var input struct {
			Name      string     `json:"name"`
			Scopes    []string   `json:"scopes"`
			ExpiresAt *time.Time `json:"expiresAt"`
		}
		if !DecodeJSON(w, r, &input) {
			return
		}
		for _, scope := range input.Scopes {
			if scope == "" || strings.Contains(scope, ",") || !strings.Contains(scopes, ","+scope+",") {
				WriteProblem(w, 400, "invalid_scope", "Unknown scope")
				return
			}
			caller := r.Context().Value(credentialKey{}).(credential)
			if scope == "platform" && !strings.Contains(caller.scopes, ",platform,") {
				WriteProblem(w, 403, "forbidden", "Operator scope required")
				return
			}
		}
		if input.ExpiresAt != nil && !input.ExpiresAt.After(time.Now()) {
			WriteProblem(w, 400, "invalid_expiry", "Expiry must be in the future")
			return
		}
		var random [40]byte
		if _, err := rand.Read(random[:]); err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot create key")
			return
		}
		id := hex.EncodeToString(random[:8])
		token := "lwm_" + id + "_" + hex.EncodeToString(random[8:])
		meta := keyMetadata{ID: id, Name: input.Name, Scopes: input.Scopes, CreatedAt: time.Now().UTC(), ExpiresAt: input.ExpiresAt}
		s.mu.Lock()
		s.entries[id] = credential{hash: sha256.Sum256([]byte(token)), scopes: "," + strings.Join(input.Scopes, ",") + ",", tenant: tenant}
		s.metadata[id] = meta
		s.mu.Unlock()
		w.Header().Set("Location", "/v1/api-keys/"+id)
		deviceJSON(w, 201, struct {
			keyMetadata
			Token string `json:"token"`
		}{meta, token})
	case "GET":
		items := make([]keyMetadata, 0)
		s.mu.Lock()
		for id, meta := range s.metadata {
			if s.entries[id].tenant == tenant {
				items = append(items, meta)
			}
		}
		s.mu.Unlock()
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		deviceJSON(w, 200, map[string]any{"items": items})
	}
}
