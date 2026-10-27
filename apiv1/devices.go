package apiv1

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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
	objects := make([]map[string]any, 0, len(r.Objects))
	for _, obj := range r.Objects {
		objects = append(objects, map[string]any{"id": obj.ID, "version": obj.Version, "instances": obj.Instances})
	}
	formats := make([]string, 0, len(r.ContentFormats))
	for _, f := range r.ContentFormats {
		name := map[string]string{"text/plain": "text", "application/link-format": "link-format", "application/octet-stream": "opaque", "application/cbor": "cbor", "application/senml+json": "senml-json", "application/senml+cbor": "senml-cbor", "application/senml-etch+json": "senml-etch-json", "application/senml-etch+cbor": "senml-etch-cbor", "application/vnd.oma.lwm2m+tlv": "tlv", "application/vnd.oma.lwm2m+json": "oma-json", "application/vnd.oma.lwm2m+cbor": "lwm2m-cbor"}[f.Canonical().String()]
		formats = append(formats, name)
	}
	mode := r.Identity.Mode.String()
	if r.Identity.Mode == server.ModeNoSec {
		mode = "none"
	}
	identity := map[string]any{"mode": mode}
	switch r.Identity.Mode {
	case server.ModePSK:
		identity["pskIdentity"] = r.Identity.PSKIdentity
	case server.ModeRPK:
		identity["publicKey"] = r.Identity.PublicKey
	case server.ModeX509:
		identity["certificateSubject"] = r.Identity.CertCN
	}
	address := ""
	if a := r.RemoteAddr(); a != nil {
		address = a.String()
	}
	transport := strings.TrimSuffix(r.Binding, "Q")
	if p := r.Peer(); p != nil {
		transport = p.Binding()
	}
	reg := map[string]any{"id": r.ID, "lwm2mVersion": r.Version, "lifetimeSeconds": int64(r.Lifetime / time.Second), "binding": r.Binding, "queueMode": r.QueueMode, "sms": r.SMS, "profileIds": r.ProfileIDs, "rootPath": r.RootPath, "objects": objects, "contentFormats": formats, "identity": identity, "transport": transport, "address": address, "awake": awake, "registeredAt": r.RegisteredAt.UTC(), "lastUpdateAt": at}
	if r.Lifetime > 0 {
		reg["expiresAt"] = r.LastUpdate.Add(r.Lifetime).UTC()
	} else {
		reg["expiresAt"] = nil
	}
	v.Registration = reg
	return v
}
func deviceJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
			limit := 50
			var err error
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
			items, next, err := o.Devices.List(tenant, store.DeviceFilter{Q: q.Get("q"), Status: status, StatusOf: func(d store.Device) string { return o.view(tenant, d).Status }}, q.Get("cursor"), limit)
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
	o.emitDevice(tenant, "lwm2m.device.deleted", d)
	w.WriteHeader(204)
}
