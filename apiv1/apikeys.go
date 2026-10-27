package apiv1

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/fiumaralabs/lwm2m/store"
	"net/http"
	"sort"
	"strings"
	"time"
)

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
		err := s.Delete(tenant, id)
		if errors.Is(err, store.ErrAPIKeyNotFound) {
			WriteProblem(w, 404, "not_found", "Unknown API key")
			return
		}
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot revoke key")
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
		if err := s.Create(store.APIKey{KeyMetadata: meta, Hash: sha256.Sum256([]byte(token)), Tenant: tenant}); err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot create key")
			return
		}
		w.Header().Set("Location", "/v1/api-keys/"+id)
		deviceJSON(w, 201, struct {
			keyMetadata
			Token string `json:"token"`
		}{meta, token})
	case "GET":
		items, err := s.List(tenant)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot list keys")
			return
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		deviceJSON(w, 200, map[string]any{"items": items})
	}
}
