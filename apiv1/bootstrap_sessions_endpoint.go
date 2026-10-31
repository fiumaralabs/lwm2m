package apiv1

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/fiumaralabs/lwm2m/store"
)

func (o Options) serveBootstrapSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET")
		return
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/bootstrap/sessions")
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
	if err != nil || len(q["limit"]) > 1 || len(q["cursor"]) > 1 {
		WriteProblem(w, 400, "invalid_query", "Invalid query")
		return
	}
	limit := 50
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
	}
	if err != nil || limit < 1 || limit > 500 {
		WriteProblem(w, 400, "invalid_limit", "limit must be between 1 and 500")
		return
	}
	if q.Has("cursor") && q.Get("cursor") == "" {
		WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
		return
	}
	items, next, err := o.BootstrapSessions.ListForDevice(tenant, ep, q.Get("cursor"), limit)
	if errors.Is(err, ErrBootstrapSessionCursor) {
		WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
		return
	}
	if err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot list sessions")
		return
	}
	if items == nil {
		items = []BootstrapSession{}
	}
	deviceJSON(w, 200, struct {
		Items []BootstrapSession `json:"items"`
		Next  string             `json:"nextCursor,omitempty"`
	}{items, next})
}
