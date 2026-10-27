package apiv1

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

func (o Options) registration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, DELETE")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET or DELETE")
		return
	}
	scope := "devices:read"
	if r.Method == http.MethodDelete {
		scope = "devices:write"
	}
	RequireScope(scope)(http.HandlerFunc(o.serveRegistration)).ServeHTTP(w, r)
}

func (o Options) serveRegistration(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/registration")
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
	notRegistered := func() { WriteProblem(w, 404, "not_registered", "Device is not registered") }
	if o.Server == nil || tenant != o.ServerTenant {
		notRegistered()
		return
	}
	reg, ok := o.Server.Store().ByEndpoint(ep)
	if !ok || reg.Expired(time.Now()) {
		notRegistered()
		return
	}
	if r.Method == http.MethodDelete {
		if !o.Server.RemoveRegistration(ep) {
			notRegistered()
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	deviceJSON(w, http.StatusOK, registrationView(reg, o.Server.Awake(ep)))
}

// registrationView is shared by device summaries and the current-session resource.
func registrationView(r *server.Registration, awake bool) map[string]any {
	objects := make([]map[string]any, 0, len(r.Objects))
	for _, obj := range r.Objects {
		instances := obj.Instances
		if instances == nil {
			instances = []uint16{}
		}
		entry := map[string]any{"id": obj.ID, "instances": instances}
		if obj.Version != "" {
			entry["version"] = obj.Version
		}
		objects = append(objects, entry)
	}
	formats := make([]string, 0, len(r.ContentFormats))
	for _, f := range r.ContentFormats {
		name := map[string]string{"text/plain": "text", "application/link-format": "link-format", "application/octet-stream": "opaque", "application/cbor": "cbor", "application/senml+json": "senml-json", "application/senml+cbor": "senml-cbor", "application/senml-etch+json": "senml-etch-json", "application/senml-etch+cbor": "senml-etch-cbor", "application/vnd.oma.lwm2m+tlv": "tlv", "application/vnd.oma.lwm2m+json": "oma-json", "application/vnd.oma.lwm2m+cbor": "lwm2m-cbor"}[f.Canonical().String()]
		if name != "" {
			formats = append(formats, name)
		}
	}
	reg := map[string]any{"lifetimeSeconds": int64(r.Lifetime / time.Second), "queueMode": r.QueueMode, "rootPath": r.RootPath, "objects": objects, "contentFormats": formats, "awake": awake}
	transport := strings.TrimSuffix(r.Binding, "Q")
	if p := r.Peer(); p != nil {
		transport = p.Binding()
	}
	for key, value := range map[string]string{"id": r.ID, "lwm2mVersion": r.Version, "binding": r.Binding, "sms": r.SMS, "transport": transport} {
		if value != "" {
			reg[key] = value
		}
	}
	if r.ProfileIDs != nil {
		reg["profileIds"] = r.ProfileIDs
	}
	if a := r.RemoteAddr(); a != nil && a.String() != "" {
		reg["address"] = a.String()
	}
	identity := map[string]any{}
	switch r.Identity.Mode {
	case server.ModeNoSec:
		identity["mode"] = "none"
	case server.ModePSK:
		identity["mode"] = "psk"
		if r.Identity.PSKIdentity != "" {
			identity["pskIdentity"] = r.Identity.PSKIdentity
		}
	case server.ModeRPK:
		identity["mode"] = "rpk"
		if len(r.Identity.PublicKey) > 0 {
			identity["publicKey"] = r.Identity.PublicKey
		}
	case server.ModeX509:
		identity["mode"] = "x509"
		if r.Identity.CertCN != "" {
			identity["certificateSubject"] = r.Identity.CertCN
		}
	}
	if len(identity) > 0 {
		reg["identity"] = identity
	}
	if !r.RegisteredAt.IsZero() {
		reg["registeredAt"] = r.RegisteredAt.UTC()
	}
	if !r.LastUpdate.IsZero() {
		reg["lastUpdateAt"] = r.LastUpdate.UTC()
		if r.Lifetime > 0 {
			reg["expiresAt"] = r.LastUpdate.Add(r.Lifetime).UTC()
		}
	}
	return reg
}
