package apiv1

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AuditEntry is a secret-free record of an authenticated mutation attempt.
type AuditEntry struct {
	ID       string            `json:"id"`
	Time     time.Time         `json:"time"`
	Actor    AuditActor        `json:"actor"`
	Action   string            `json:"action"`
	Resource string            `json:"resource"`
	Status   int               `json:"status"`
	IP       string            `json:"ip"`
	Summary  map[string]string `json:"summary"`
}
type AuditActor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AuditStore separates tenants and returns records in insertion order.
type AuditStore interface {
	Append(tenant string, entry AuditEntry) error
	List(tenant string) ([]AuditEntry, error)
}
type MemoryAuditStore struct {
	mu      sync.RWMutex
	entries map[string][]AuditEntry
}

func (s *MemoryAuditStore) Append(tenant string, e AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string][]AuditEntry)
	}
	e.Summary = maps.Clone(e.Summary)
	s.entries[tenant] = append(s.entries[tenant], e)
	return nil
}
func (s *MemoryAuditStore) List(tenant string) ([]AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]AuditEntry(nil), s.entries[tenant]...)
	for i := range out {
		out[i].Summary = maps.Clone(out[i].Summary)
	}
	return out, nil
}

// AuditOptions allows a caller to replace the default in-memory audit store.
type AuditOptions struct {
	Audit AuditStore `json:"-"`
}

type auditWriter struct {
	http.ResponseWriter
	status int
}

func (w *auditWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *auditWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}
func (w *auditWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Failed paths can contain credentials in any segment, including unknown routes.
// Only successful, recognized resource shapes are safe to preserve as actionable paths.
func auditResource(path string, status int) string {
	if status < 200 || status >= 300 {
		return "/[redacted]"
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[0] != "" || parts[1] != "v1" {
		return "/[redacted]"
	}
	switch parts[2] {
	case "devices", "api-keys", "webhooks", "operations", "observations", "settings":
	default:
		return "/[redacted]"
	}
	if len(parts) == 3 || len(parts) == 4 && parts[3] != "" {
		return path
	}
	if len(parts) == 5 && parts[3] != "" {
		if parts[2] == "devices" && (parts[4] == "operations" || parts[4] == "observations" || parts[4] == "registration") || parts[2] == "webhooks" && parts[4] == "rotate-secret" {
			return path
		}
	}
	return "/[redacted]"
}

func auditAction(r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	resource := "request"
	if len(parts) > 1 {
		resource = map[string]string{"devices": "device", "api-keys": "api_key", "webhooks": "webhook", "operations": "operation", "observations": "observation", "settings": "settings"}[parts[1]]
		if resource == "" {
			resource = "request"
		}
	}
	if len(parts) > 3 {
		switch parts[3] {
		case "operations":
			resource = "operation"
		case "observations":
			resource = "observation"
		}
	}
	if r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "webhooks" && parts[3] == "rotate-secret" {
		return "webhook.rotate_secret"
	}
	verb := map[string]string{"POST": "create", "PUT": "update", "PATCH": "update", "DELETE": "delete"}[r.Method]
	return resource + "." + verb
}
func recordAudit(s AuditStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" && r.Method != "PUT" && r.Method != "PATCH" && r.Method != "DELETE" {
			next.ServeHTTP(w, r)
			return
		}
		id := strings.SplitN(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer lwm_"), "_", 2)[0]
		actor := AuditActor{Kind: "apikey", ID: id}
		if keys, ok := r.Context().Value(keyStoreContext{}).(*keyStore); ok {
			if key, err := keys.Get(id); err == nil {
				actor.Name = key.Name
			}
		}
		tenant := TenantFrom(r.Context())
		aw := &auditWriter{ResponseWriter: w}
		next.ServeHTTP(aw, r)
		if aw.status == 0 {
			aw.status = http.StatusOK
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			slog.Error("Cannot create audit ID", "error", err)
			return
		}
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}
		// Never copy request or response data into summaries: even innocent-looking fields may contain secrets.
		e := AuditEntry{ID: "aud_" + hex.EncodeToString(random[:]), Time: time.Now().UTC(), Actor: actor, Action: auditAction(r), Resource: auditResource(r.URL.EscapedPath(), aw.status), Status: aw.status, IP: ip, Summary: map[string]string{}}
		if err := s.Append(tenant, e); err != nil {
			slog.Error("Cannot append audit entry", "error", err)
		}
	})
}

type auditCursor struct{ Tenant, Actor, Action, Resource, From, To, Before string }

func serveAudit(s AuditStore, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		WriteProblem(w, 400, "invalid_query", "Invalid query")
		return
	}
	for _, key := range []string{"actor", "action", "resource", "from", "to", "cursor", "limit"} {
		if len(q[key]) > 1 {
			WriteProblem(w, 400, "invalid_query", "Repeated query parameter")
			return
		}
	}
	limit := 50
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
	}
	if err != nil || limit < 1 || limit > 500 {
		WriteProblem(w, 400, "invalid_limit", "limit must be between 1 and 500")
		return
	}
	c := auditCursor{Tenant: TenantFrom(r.Context()), Actor: q.Get("actor"), Action: q.Get("action"), Resource: q.Get("resource"), From: q.Get("from"), To: q.Get("to")}
	var from, to time.Time
	if c.From != "" {
		from, err = time.Parse(time.RFC3339Nano, c.From)
		if err != nil {
			WriteProblem(w, 400, "invalid_query", "Invalid from timestamp")
			return
		}
	}
	if c.To != "" {
		to, err = time.Parse(time.RFC3339Nano, c.To)
		if err != nil {
			WriteProblem(w, 400, "invalid_query", "Invalid to timestamp")
			return
		}
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		WriteProblem(w, 400, "invalid_query", "Invalid time range")
		return
	}
	if q.Has("cursor") {
		var prior auditCursor
		b, e := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		if e != nil || json.Unmarshal(b, &prior) != nil || prior.Before == "" {
			WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
			return
		}
		before := prior.Before
		prior.Before = ""
		if prior != c {
			WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
			return
		}
		c.Before = before
	}
	entries, err := s.List(c.Tenant)
	if err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot list audit entries")
		return
	}
	items := make([]AuditEntry, 0, limit+1)
	past := c.Before == ""
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !past {
			if e.ID == c.Before {
				past = true
			}
			continue
		}
		if c.Actor != "" && e.Actor.ID != c.Actor || c.Action != "" && e.Action != c.Action || c.Resource != "" && e.Resource != c.Resource || !from.IsZero() && e.Time.Before(from) || !to.IsZero() && e.Time.After(to) {
			continue
		}
		items = append(items, e)
		if len(items) > limit {
			break
		}
	}
	if !past {
		WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		c.Before = items[len(items)-1].ID
		b, _ := json.Marshal(c)
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	deviceJSON(w, 200, struct {
		Items []AuditEntry `json:"items"`
		Next  string       `json:"nextCursor,omitempty"`
	}{items, next})
}
