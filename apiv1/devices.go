package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/selector"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

// DeviceEvents must be composed with server.Config.OnEvent for the server's
// owning tenant. It records presence even when no HTTP request observes a session.
// Unknown endpoints are not provisioned: autoCreateDevices defaults to false.
func DeviceEvents(devices store.Devices, tenant string) func(server.Event) {
	return func(e server.Event) {
		var r *server.Registration
		switch e := e.(type) {
		case server.Registered:
			r = e.Registration
		case server.Updated:
			r = e.Registration
		case server.Deregistered:
			r = e.Registration
		}
		if r != nil {
			devices.Seen(tenant, r.Endpoint, r.LastUpdate)
		}
	}
}

type deviceView struct {
	store.Device
	Status       string `json:"status"`
	Registration any    `json:"registration"`
}

func (o Options) view(tenant string, d store.Device) deviceView {
	v := deviceView{Device: d, Status: "never-seen"}
	if d.LastSeenAt != nil {
		v.Status = "offline"
	}
	if o.Server == nil || tenant != o.ServerTenant {
		return v
	}
	r, ok := o.Server.Store().ByEndpoint(d.Endpoint)
	if !ok || r.Expired(time.Now()) {
		return v
	}
	at := r.LastUpdate.UTC()
	v.LastSeenAt = &at
	v.Status = "registered"
	awake := o.Server.Awake(d.Endpoint)
	if !awake {
		v.Status = "sleeping"
	}
	v.Registration = registrationView(r, awake)
	return v
}
func deviceJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// selectorCursor resumes within a bounded batch, keeping store cursors opaque.
type selectorCursor struct {
	Selector, Tenant, Q, Status, Cursor string
	Offset                              int
}

func (o Options) listSelectedDevices(r *http.Request, tenant string, filter store.DeviceFilter, match selector.Selector, limit int) ([]store.Device, string, error) {
	query, cursor := r.URL.Query().Get("selector"), r.URL.Query().Get("cursor")
	if query == "" {
		return o.Devices.List(tenant, filter, cursor, limit)
	}
	c := selectorCursor{Selector: query, Tenant: tenant, Q: filter.Q, Status: filter.Status}
	if cursor != "" {
		var decoded selectorCursor
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, "sel."))
		if !strings.HasPrefix(cursor, "sel.") || err != nil || json.Unmarshal(b, &decoded) != nil || decoded.Selector != query || decoded.Tenant != tenant || decoded.Q != filter.Q || decoded.Status != filter.Status || decoded.Offset < 0 || decoded.Offset >= 500 {
			return nil, "", store.ErrDeviceCursor
		}
		c = decoded
	}
	items := make([]store.Device, 0, limit)
	// Scan at the store's maximum page size, independent of selector density.
	// A continuation replays at most one batch, not the entire tenant.
	for {
		if err := r.Context().Err(); err != nil {
			return nil, "", err
		}
		batch, next, err := o.Devices.List(tenant, filter, c.Cursor, 500)
		if err != nil {
			return nil, "", err
		}
		for i := c.Offset; i < len(batch); i++ {
			d := batch[i]
			if !match.Matches(d.Labels) {
				continue
			}
			if len(items) == limit {
				c.Offset = i
				b, _ := json.Marshal(c)
				return items, "sel." + base64.RawURLEncoding.EncodeToString(b), nil
			}
			items = append(items, d)
		}
		if next == "" {
			return items, "", nil
		}
		c.Cursor, c.Offset = next, 0
	}
}

func (o Options) devices(w http.ResponseWriter, r *http.Request) {
	scope := "devices:read"
	if r.Method != "GET" {
		scope = "devices:write"
	}
	RequireScope(scope)(http.HandlerFunc(o.serveDevices)).ServeHTTP(w, r)
}
func (o Options) serveDevices(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	collection := r.URL.Path == "/v1/devices"
	if collection {
		switch r.Method {
		case "POST":
			var input struct {
				Endpoint string            `json:"endpoint"`
				Name     string            `json:"name"`
				Labels   map[string]string `json:"labels"`
			}
			if !DecodeJSON(w, r, &input) {
				return
			}
			if strings.TrimSpace(input.Endpoint) == "" {
				WriteProblem(w, 400, "invalid_endpoint", "endpoint is required")
				return
			}
			unlock := o.limits.lock(tenant)
			defer unlock()
			if t, ok := o.limits.tenants.Get(tenant); ok && t.Limits.Devices > 0 {
				reached, err := o.deviceLimitReached(tenant, t.Limits.Devices)
				if err != nil {
					WriteProblem(w, 500, "internal_error", "Cannot list devices")
					return
				}
				if reached {
					// Capacity has no expiry; suggest a backoff, not guaranteed availability.
					w.Header().Set("Retry-After", "60")
					WriteProblem(w, 429, "limit_exceeded", "Device limit exceeded")
					return
				}
			}
			d, err := o.Devices.Create(tenant, store.Device{Endpoint: input.Endpoint, Name: input.Name, Labels: input.Labels})
			if err != nil {
				if errors.Is(err, store.ErrDeviceExists) {
					WriteProblem(w, 409, "device_exists", "Device already exists")
				} else {
					WriteProblem(w, 500, "internal_error", "Cannot create device")
				}
				return
			}
			w.Header().Set("Location", "/v1/devices/"+url.PathEscape(d.Endpoint))
			o.emitDevice(tenant, "lwm2m.device.created", d)
			deviceJSON(w, 201, o.view(tenant, d))
		case "GET":
			q := r.URL.Query()
			match, err := selector.Parse(q.Get("selector"))
			if err != nil {
				WriteProblem(w, 400, "invalid_selector", "Invalid label selector")
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
			status := q.Get("status")
			if status != "" && status != "never-seen" && status != "registered" && status != "sleeping" && status != "offline" {
				WriteProblem(w, 400, "invalid_status", "Unknown device status")
				return
			}
			filter := store.DeviceFilter{Q: q.Get("q"), Status: status, StatusOf: func(d store.Device) string { return o.view(tenant, d).Status }}
			items, next, err := o.listSelectedDevices(r, tenant, filter, match, limit)
			if err != nil {
				if errors.Is(err, store.ErrDeviceCursor) {
					WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
				} else {
					WriteProblem(w, 500, "internal_error", "Cannot list devices")
				}
				return
			}
			views := make([]deviceView, 0, len(items))
			for _, d := range items {
				views = append(views, o.view(tenant, d))
			}
			deviceJSON(w, 200, struct {
				Items []deviceView `json:"items"`
				Next  string       `json:"nextCursor,omitempty"`
			}{views, next})
		default:
			w.Header().Set("Allow", "GET, POST")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET or POST")
		}
		return
	}
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/")
	ep, err := url.PathUnescape(raw)
	if err != nil || ep == "" || strings.Contains(raw, "/") {
		WriteProblem(w, 404, "not_found", "Unknown device path")
		return
	}
	if r.Method != "GET" && r.Method != "DELETE" {
		w.Header().Set("Allow", "GET, DELETE")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET or DELETE")
		return
	}
	d, err := o.Devices.Get(tenant, ep)
	if err != nil {
		if errors.Is(err, store.ErrDeviceNotFound) {
			WriteProblem(w, 404, "not_found", "Device not found")
		} else {
			WriteProblem(w, 500, "internal_error", "Cannot get device")
		}
		return
	}
	if r.Method == "GET" {
		deviceJSON(w, 200, o.view(tenant, d))
		return
	}
	if o.Server != nil && tenant == o.ServerTenant {
		o.Server.RemoveRegistration(ep)
	}
	if err := o.Devices.Delete(tenant, ep); err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot delete device")
		return
	}
	o.BootstrapConfigs.Delete(tenant, ep)
	o.emitDevice(tenant, "lwm2m.device.deleted", d)
	w.WriteHeader(204)
}
