package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fiumaralabs/lwm2m/store"
)

type deviceEventCursor struct {
	Tenant, Endpoint, Type, Before string
}

func (o Options) deviceEvents(w http.ResponseWriter, r *http.Request) {
	RequireScope("devices:read")(http.HandlerFunc(o.serveDeviceEvents)).ServeHTTP(w, r)
}
func (o Options) serveDeviceEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET")
		return
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/events")
	ep, err := url.PathUnescape(raw)
	if err != nil || ep == "" || strings.Contains(raw, "/") {
		WriteProblem(w, 404, "not_found", "Unknown device path")
		return
	}
	tenant := TenantFrom(r.Context())
	if _, err := o.Devices.Get(tenant, ep); err != nil {
		if errors.Is(err, store.ErrDeviceNotFound) {
			WriteProblem(w, 404, "not_found", "Device not found")
		} else {
			WriteProblem(w, 500, "internal_error", "Cannot get device")
		}
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		WriteProblem(w, 400, "invalid_query", "Invalid event query")
		return
	}
	for _, key := range []string{"limit", "cursor", "type"} {
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
	cursor := deviceEventCursor{Tenant: tenant, Endpoint: ep, Type: q.Get("type")}
	if q.Has("cursor") {
		var c deviceEventCursor
		b, err := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		if err != nil || json.Unmarshal(b, &c) != nil || !store.ValidSequence(c.Before) || c.Tenant != tenant || c.Endpoint != ep || c.Type != cursor.Type {
			WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
			return
		}
		cursor = c
	}
	if q.Has("type") {
		for _, pattern := range strings.Split(cursor.Type, ",") {
			base := strings.TrimSuffix(pattern, "*")
			invalid := base == ""
			for _, c := range base {
				if c <= ' ' || c >= 127 || c == '*' {
					invalid = true
				}
			}
			if invalid {
				WriteProblem(w, 400, "invalid_query", "Invalid event type")
				return
			}
		}
	}
	items, _, err := o.Events.List(tenant, store.EventFilter{Device: ep, Type: cursor.Type, Before: cursor.Before, Newest: true, Limit: limit + 1})
	if err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot list events")
		return
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		cursor.Before = items[len(items)-1].Sequence
		b, _ := json.Marshal(cursor)
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	if items == nil {
		items = []store.Event{}
	}
	deviceJSON(w, 200, struct {
		Items []store.Event `json:"items"`
		Next  string        `json:"nextCursor,omitempty"`
	}{items, next})
}

// emitDevice records a snapshot after a successful mutation. Event failures are
// logged, not returned: retrying an already-committed mutation is unsafe.
func (o Options) emitDevice(tenant, kind string, d store.Device) {
	data, err := json.Marshal(struct {
		Device deviceView `json:"device"`
	}{o.view(tenant, d)})
	if err == nil {
		_, err = o.Events.Append(tenant, kind, d.Endpoint, data)
	}
	if err != nil {
		slog.Error("Cannot append device event", "type", kind, "error", err)
	}
}
