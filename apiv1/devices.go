package apiv1

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/selector"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/transport/coap"
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

// decodeInlineCredentials authorizes and validates optional inline PSK credentials
// without mutating either store. Callers must invoke it before creating a device
// or committing a durable create outcome. A nil result means no credentials.
func (o Options) decodeInlineCredentials(w http.ResponseWriter, r *http.Request, tenant, endpoint string, raw json.RawMessage) (*server.SecurityInfo, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	authorized := false
	RequireScope("credentials:write")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { authorized = true })).ServeHTTP(w, r)
	if !authorized {
		return nil, false
	}
	if o.Server == nil || tenant != o.ServerTenant {
		WriteProblem(w, 404, "not_found", "Credential service not available")
		return nil, false
	}
	credentialRequest := r.Clone(r.Context())
	credentialRequest.Body = io.NopCloser(bytes.NewReader(raw))
	identity, _, key, generated, ok := decodePSKCredential(w, credentialRequest)
	if !ok {
		return nil, false
	}
	if generated {
		WriteProblem(w, 400, "invalid_credential", "Generated credentials require the credentials endpoint")
		return nil, false
	}
	return &server.SecurityInfo{Endpoint: endpoint, PSKIdentity: identity, PSKKey: key}, true
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
			if key := r.Header.Get("Idempotency-Key"); key != "" {
				o.replayDeviceCreate(w, r, tenant, key)
				return
			}
			var input struct {
				Endpoint    string            `json:"endpoint"`
				Name        string            `json:"name"`
				Profile     string            `json:"profile"`
				Labels      map[string]string `json:"labels"`
				Credentials json.RawMessage   `json:"credentials"`
				Bootstrap   json.RawMessage   `json:"bootstrap"`
			}
			if !DecodeJSON(w, r, &input) {
				return
			}
			if strings.TrimSpace(input.Endpoint) == "" {
				WriteProblem(w, 400, "invalid_endpoint", "endpoint is required")
				return
			}
			if input.Profile != "" {
				if _, ok := o.DeviceProfiles.Get(tenant, input.Profile); !ok {
					WriteProblem(w, 400, "invalid_profile", "Unknown device profile")
					return
				}
			}
			security, ok := o.decodeInlineCredentials(w, r, tenant, input.Endpoint, input.Credentials)
			if !ok {
				return
			}
			if len(input.Bootstrap) != 0 {
				authorized := false
				RequireScope("bootstrap:write")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { authorized = true })).ServeHTTP(w, r)
				if !authorized {
					return
				}
				var config bootstrapInput
				b := bytes.TrimSpace(input.Bootstrap)
				if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &config) != nil || !unambiguousBootstrapJSON(b) {
					WriteProblem(w, 400, "invalid_json", "Invalid bootstrap config")
					return
				}
				code, fields := validateBootstrap(config)
				if code != "" {
					WriteProblem(w, 501, code, "Bootstrap security mode is not supported")
					return
				}
				if len(fields) != 0 {
					WriteProblem(w, 400, "validation_error", "Invalid bootstrap config")
					return
				}
				// Generated keys must be materialized before the one atomic write.
				var doc map[string]any
				dec := json.NewDecoder(bytes.NewReader(b))
				dec.UseNumber()
				if dec.Decode(&doc) != nil {
					WriteProblem(w, 400, "invalid_json", "Invalid bootstrap config")
					return
				}
				for i, entry := range config.Servers {
					if entry.Security.PSK == nil || !entry.Security.PSK.Generate {
						continue
					}
					if entry.Server == "self" && security != nil {
						WriteProblem(w, 400, "invalid_credential", "Conflicting self credentials")
						return
					}
					var random [48]byte
					if _, err := rand.Read(random[:]); err != nil {
						WriteProblem(w, 500, "internal_error", "Cannot generate credentials")
						return
					}
					identity := hex.EncodeToString(random[:16])
					key := random[16:]
					servers := doc["servers"].([]any)
					servers[i].(map[string]any)["security"].(map[string]any)["psk"] = map[string]any{"identity": identity, "key": "base64:" + base64.StdEncoding.EncodeToString(key)}
					if entry.Server == "self" {
						if o.Server == nil || tenant != o.ServerTenant {
							WriteProblem(w, 404, "not_found", "Credential service not available")
							return
						}
						if !authorizeDeviceReplayCredentials(w, r) {
							return
						}
						security = &server.SecurityInfo{Endpoint: input.Endpoint, PSKIdentity: identity, PSKKey: key}
					}
				}
				input.Bootstrap, _ = json.Marshal(doc)
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
			unlockLifecycle := bootstrapLifecycle.lock(tenant)
			defer unlockLifecycle()
			var d store.Device
			if security != nil || len(input.Bootstrap) != 0 {
				creator, ok := o.Devices.(store.DeviceSecurityCreateStore)
				owner, owned := o.Devices.(interface {
					OwnsSecurity(server.SecurityStore) bool
				})
				outcomes, hasOutcomes := o.Devices.(store.DeviceCreateStore)
				var liveSecurity server.SecurityStore
				if o.Server != nil {
					liveSecurity = o.Server.Security()
					// Only unwrap the known transparent daemon decorator; arbitrary
					// wrappers cannot attest that their lookup store is the same.
					if wrapped, isRevoking := liveSecurity.(*coap.RevokingSecurityStore); isRevoking && wrapped != nil {
						liveSecurity = wrapped.SecurityStore
					}
				}
				if !ok || !hasOutcomes || (security != nil && (!owned || liveSecurity == nil || !owner.OwnsSecurity(liveSecurity))) {
					WriteProblem(w, 501, "inline_credentials_unavailable", "Atomic inline credentials are unavailable")
					return
				}
				var nonce [16]byte
				if _, err := rand.Read(nonce[:]); err != nil {
					WriteProblem(w, 500, "internal_error", "Cannot create operation ID")
					return
				}
				id := hex.EncodeToString(nonce[:])
				encoded, err := json.Marshal(input)
				if err != nil {
					WriteProblem(w, 500, "internal_error", "Cannot fingerprint create")
					return
				}
				digest := sha256.Sum256(encoded)
				ctx := store.WithDeviceCreateCapture(r.Context(), func(committed store.Device) { d = committed })
				var result store.DeviceCreateOutcome
				device := store.Device{Endpoint: input.Endpoint, Name: input.Name, Profile: input.Profile, Labels: input.Labels}
				if len(input.Bootstrap) != 0 {
					bootstrapCreator, supported := o.Devices.(store.DeviceBootstrapCreateStore)
					shared, sharedOK := o.BootstrapConfigs.(sharedBootstrapConfigs)
					if !supported || !sharedOK || shared.DeviceBootstrapCreateStore != bootstrapCreator {
						WriteProblem(w, 501, "inline_bootstrap_unavailable", "Atomic inline bootstrap is unavailable")
						return
					}
					_, exists, readErr := bootstrapCreator.GetBootstrapConfig(r.Context(), tenant, input.Endpoint)
					if readErr != nil {
						WriteProblem(w, 500, "internal_error", "Cannot read bootstrap config")
						return
					}
					if exists {
						WriteProblem(w, 409, "bootstrap_exists", "Bootstrap config already exists")
						return
					}
					if liveSecurity != nil {
						if _, exists := liveSecurity.ByEndpoint(input.Endpoint); exists {
							WriteProblem(w, 409, "credential_exists", "Credential already exists")
							return
						}
					}
					var sec server.SecurityInfo
					if security != nil {
						sec = *security
					}
					result, _, err = bootstrapCreator.CreateDeviceWithSecurityAndBootstrap(ctx, tenant, id, hex.EncodeToString(digest[:]), device, sec, input.Bootstrap)
				} else {
					result, _, err = creator.CreateDeviceWithSecurity(ctx, tenant, id, hex.EncodeToString(digest[:]), device, *security)
				}
				if err != nil {
					if errors.Is(err, store.ErrBootstrapConfigExists) {
						WriteProblem(w, 409, "bootstrap_exists", "Bootstrap config already exists")
					} else if errors.Is(err, store.ErrCredentialExists) {
						WriteProblem(w, 409, "credential_exists", "Credential already exists")
					} else if errors.Is(err, server.ErrDuplicatePSKIdentity) {
						WriteProblem(w, 409, "duplicate_psk_identity", "PSK identity already exists")
					} else {
						WriteProblem(w, 500, "internal_error", "Cannot create device with credential")
					}
					return
				}
				// This internal outcome has no public operation ID to acknowledge.
				_ = outcomes.DeleteDeviceCreate(r.Context(), tenant, id)
				if result.Status != "committed" {
					WriteProblem(w, 409, "device_exists", "Device already exists")
					return
				}

			} else if store.DeviceCreateReplayFrom(r.Context()) != nil {
				creator, ok := o.Devices.(store.DeviceCreateStore)
				if !ok {
					WriteProblem(w, 501, "not_implemented", "Atomic device create replay is not supported by this store")
					return
				}
				var nonce [16]byte
				if _, err := rand.Read(nonce[:]); err != nil {
					WriteProblem(w, 500, "internal_error", "Cannot create operation ID")
					return
				}
				id := hex.EncodeToString(nonce[:])
				result, _, err := creator.CreateDeviceWithOutcome(r.Context(), tenant, id, "", store.Device{Endpoint: input.Endpoint, Name: input.Name, Profile: input.Profile, Labels: input.Labels})
				if err != nil {
					WriteProblem(w, 500, "internal_error", "Cannot create device")
					return
				}
				_ = creator.DeleteDeviceCreate(r.Context(), tenant, id)
				if result.Status != "committed" {
					WriteProblem(w, 409, "device_exists", "Device already exists")
					return
				}
				d, err = o.Devices.Get(tenant, input.Endpoint)
				if err != nil {
					WriteProblem(w, 500, "internal_error", "Cannot read created device")
					return
				}
			} else {
				var err error
				d, err = o.Devices.Create(tenant, store.Device{Endpoint: input.Endpoint, Name: input.Name, Profile: input.Profile, Labels: input.Labels})
				if err != nil {
					if errors.Is(err, store.ErrDeviceExists) {
						WriteProblem(w, 409, "device_exists", "Device already exists")
					} else {
						WriteProblem(w, 500, "internal_error", "Cannot create device")
					}
					return
				}
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
	if r.Method != "GET" && r.Method != "DELETE" && r.Method != "PATCH" {
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET, PATCH or DELETE")
		return
	}
	if r.Method == "DELETE" {
		unlock := o.limits.lock(tenant)
		defer unlock()
		unlockLifecycle := bootstrapLifecycle.lock(tenant)
		defer unlockLifecycle()
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
	if r.Method == "PATCH" {
		var input struct {
			Name    json.RawMessage `json:"name"`
			Labels  json.RawMessage `json:"labels"`
			Profile json.RawMessage `json:"profile"`
		}
		if !DecodeJSON(w, r, &input) {
			return
		}
		var patch store.DevicePatch
		if input.Name != nil {
			var name string
			if err := json.Unmarshal(input.Name, &name); err != nil {
				WriteProblem(w, 400, "invalid_name", "name must be a string or null")
				return
			}
			patch.Name = &name
		}
		if input.Labels != nil {
			labels := map[string]string{}
			if string(input.Labels) != "null" {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(input.Labels, &raw); err != nil || raw == nil {
					WriteProblem(w, 400, "invalid_labels", "labels must be an object or null")
					return
				}
				for key, value := range raw {
					var v string
					if string(value) == "null" || json.Unmarshal(value, &v) != nil {
						WriteProblem(w, 400, "invalid_labels", "label values must be strings")
						return
					}
					match, err := selector.Parse(key + "=" + v)
					if err != nil || !match.Matches(map[string]string{key: v}) {
						WriteProblem(w, 400, "invalid_labels", "invalid label")
						return
					}
					labels[key] = v
				}
			}
			patch.Labels = &labels
		}
		if input.Profile != nil {
			var profile string
			if err := json.Unmarshal(input.Profile, &profile); err != nil {
				WriteProblem(w, 400, "invalid_profile", "profile must be a string or null")
				return
			}
			if profile != "" {
				if _, ok := o.DeviceProfiles.Get(tenant, profile); !ok {
					WriteProblem(w, 400, "invalid_profile", "Unknown device profile")
					return
				}
			}
			patch.Profile = &profile
		}
		if patch.Name == nil && patch.Labels == nil && patch.Profile == nil {
			deviceJSON(w, 200, o.view(tenant, d))
			return
		}
		writer, ok := o.Devices.(store.DeviceMetadataWriter)
		if !ok {
			if patch.Name == nil && patch.Labels == nil {
				if old, ok := o.Devices.(store.DeviceProfileWriter); ok {
					d, err = old.SetProfile(tenant, ep, *patch.Profile)
				} else {
					WriteProblem(w, 501, "not_implemented", "Device updates are not supported by this store")
					return
				}
			} else {
				WriteProblem(w, 501, "not_implemented", "Device updates are not supported by this store")
				return
			}
		} else {
			d, err = writer.PatchDevice(tenant, ep, patch)
		}

		if errors.Is(err, store.ErrDeviceNotFound) {
			WriteProblem(w, 404, "not_found", "Device not found")
			return
		}
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot update device")
			return
		}
		deviceJSON(w, 200, o.view(tenant, d))
		return
	}
	config, hadConfig, err := o.BootstrapConfigs.Get(tenant, ep)
	if err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot read bootstrap config")
		return
	}
	if _, err := o.BootstrapConfigs.Delete(tenant, ep); err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot delete bootstrap config")
		return
	}
	if err := o.Devices.Delete(tenant, ep); err != nil {
		if hadConfig {
			if err := o.BootstrapConfigs.Put(tenant, ep, config); err != nil {
				WriteProblem(w, 500, "internal_error", "Cannot restore bootstrap config")
				return
			}
		}
		WriteProblem(w, 500, "internal_error", "Cannot delete device")
		return
	}
	if o.Server != nil && tenant == o.ServerTenant {
		o.Server.RemoveRegistration(ep)
	}
	o.emitDevice(tenant, "lwm2m.device.deleted", d)
	w.WriteHeader(204)
}
