package apiv1

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
)

type credentialInput struct {
	PSK *struct {
		Identity string `json:"identity"`
		Key      string `json:"key"`
		Generate bool   `json:"generate"`
	} `json:"psk"`
	RPK    json.RawMessage `json:"rpk"`
	X509   json.RawMessage `json:"x509"`
	OSCORE json.RawMessage `json:"oscore"`
}
type credentialResponse struct {
	PSK struct {
		Identity string `json:"identity"`
		Key      string `json:"key,omitempty"`
	} `json:"psk"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (o Options) credentials(w http.ResponseWriter, r *http.Request) {
	scope := "devices:read"
	if r.Method != http.MethodGet {
		scope = "devices:write"
	}
	RequireScope(scope)(http.HandlerFunc(o.serveCredentials)).ServeHTTP(w, r)
}

func (o Options) serveCredentials(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/credentials")
	ep, err := url.PathUnescape(raw)
	if err != nil || ep == "" || strings.Contains(raw, "/") {
		WriteProblem(w, 404, "not_found", "Unknown device path")
		return
	}
	tenant := TenantFrom(r.Context())
	unlock := bootstrapLifecycle.lock(tenant)
	defer unlock()
	if _, err := o.Devices.Get(tenant, ep); err != nil {
		if errors.Is(err, store.ErrDeviceNotFound) {
			WriteProblem(w, 404, "not_found", "Device not found")
		} else {
			WriteProblem(w, 500, "internal_error", "Cannot get device")
		}
		return
	}
	// The server security store is global and belongs only to ServerTenant.
	if o.Server == nil || tenant != o.ServerTenant {
		WriteProblem(w, 404, "not_found", "Device not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		info, ok := o.Server.Security().ByEndpoint(ep)
		if !ok || info.PSKIdentity == "" {
			WriteProblem(w, 404, "not_found", "Credential not found")
			return
		}
		var response credentialResponse
		response.PSK.Identity = info.PSKIdentity
		response.UpdatedAt = info.UpdatedAt
		deviceJSON(w, 200, response)
	case http.MethodPut:
		identity, encoded, key, generated, ok := decodePSKCredential(w, r)
		if !ok {
			return
		}
		_, existed := o.Server.Security().ByEndpoint(ep)
		err = o.Server.Security().Put(server.SecurityInfo{Endpoint: ep, PSKIdentity: identity, PSKKey: key})
		if errors.Is(err, server.ErrDuplicatePSKIdentity) {
			WriteProblem(w, 409, "duplicate_psk_identity", "PSK identity already exists")
			return
		}
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot store credential")
			return
		}
		if existed {
			o.Server.RemoveRegistration(ep)
		}
		info, _ := o.Server.Security().ByEndpoint(ep)
		var response credentialResponse
		response.PSK.Identity = identity
		response.UpdatedAt = info.UpdatedAt
		if generated {
			response.PSK.Key = encoded
			w.Header().Set("Cache-Control", "no-store")
		}
		status := 200
		if !existed {
			status = 201
		}
		deviceJSON(w, status, response)
	case http.MethodDelete:
		_, ok, err := o.Server.RevokeSecurity(ep)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot revoke credential")
			return
		}
		if !ok {
			WriteProblem(w, 404, "not_found", "Credential not found")
			return
		}
		data, _ := json.Marshal(struct {
			Endpoint string `json:"endpoint"`
		}{ep})
		if _, err := o.Events.Append(tenant, "lwm2m.credentials.revoked", ep, data); err != nil {
			slog.Error("Cannot append credential event", "error", err)
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET, PUT or DELETE")
	}
}

func decodePSKCredential(w http.ResponseWriter, r *http.Request) (string, string, []byte, bool, bool) {
	var input credentialInput
	if !DecodeJSON(w, r, &input) {
		return "", "", nil, false, false
	}
	if len(input.RPK) > 0 {
		WriteProblem(w, 501, "rpk_unsupported", "RPK is not supported")
		return "", "", nil, false, false
	}
	if len(input.X509) > 0 || len(input.OSCORE) > 0 {
		WriteProblem(w, 501, "security_mode_unsupported", "Security mode is not supported")
		return "", "", nil, false, false
	}
	if input.PSK == nil {
		WriteProblem(w, 400, "invalid_credential", "PSK is required")
		return "", "", nil, false, false
	}
	identity, encoded := input.PSK.Identity, input.PSK.Key
	var key []byte
	var err error
	if input.PSK.Generate {
		if identity != "" || encoded != "" {
			WriteProblem(w, 400, "invalid_credential", "Generated credentials must not specify identity or key")
			return "", "", nil, false, false
		}
		var random [48]byte
		if _, err := rand.Read(random[:]); err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot generate credentials")
			return "", "", nil, false, false
		}
		identity = hex.EncodeToString(random[:16])
		key = random[16:]
		encoded = "base64:" + base64.StdEncoding.EncodeToString(key)
	} else {
		switch {
		case strings.HasPrefix(encoded, "hex:"):
			key, err = hex.DecodeString(strings.TrimPrefix(encoded, "hex:"))
		case strings.HasPrefix(encoded, "base64:"):
			key, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "base64:"))
		default:
			err = errors.New("invalid key encoding")
		}
		if err != nil || len(key) < 16 || strings.TrimSpace(identity) == "" {
			WriteProblem(w, 400, "invalid_credential", "Invalid PSK identity or key")
			return "", "", nil, false, false
		}
	}
	return identity, encoded, key, input.PSK.Generate, true
}
