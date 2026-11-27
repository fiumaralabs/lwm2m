package apiv1

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

type createAuditKey struct{}

// validationOutcomeWriter defers credential validation errors to the outcome
// store, while preserving unsupported-mode and authorization responses.
type validationOutcomeWriter struct {
	http.ResponseWriter
	invalid bool
}

func (w *validationOutcomeWriter) WriteHeader(status int) {
	if status == http.StatusBadRequest {
		w.invalid = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *validationOutcomeWriter) Write(p []byte) (int, error) {
	if w.invalid {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

type createAuditStore struct {
	AuditStore
	skip *bool
}

func (s createAuditStore) Append(tenant string, e AuditEntry) error {
	if *s.skip {
		return nil
	}
	if e.Status == http.StatusCreated {
		e.Action = "device.create"
		e.Resource = "/v1/devices"
	}
	return s.AuditStore.Append(tenant, e)
}
func deviceCreateAudit(s AuditStore, next http.Handler) http.Handler {
	ordinary := recordAudit(s, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/v1/device-creates/") {
			ordinary.ServeHTTP(w, r)
			return
		}
		skip := false
		r = r.WithContext(context.WithValue(r.Context(), createAuditKey{}, &skip))
		recordAudit(createAuditStore{AuditStore: s, skip: &skip}, next).ServeHTTP(w, r)
	})
}

type createOutcomeJSON struct {
	OperationID string     `json:"operationId"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason,omitempty"`
	Endpoint    string     `json:"endpoint,omitempty"`
	Fingerprint *string    `json:"fingerprint"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt"`
}

func saltedCreateFingerprint(canonical []byte, salt []byte) string {
	h := sha256.New()
	_, _ = h.Write(salt)
	_, _ = h.Write(canonical)
	return "salted-sha256:" + hex.EncodeToString(salt) + ":" + hex.EncodeToString(h.Sum(nil))
}

// ambiguousCredentialKeys rejects spellings that encoding/json would fold onto
// Credentials and repeated exact keys before any secret-bearing fingerprint.
func ambiguousCredentialKeys(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := false
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return false // the normal JSON validation handles malformed input
		}
		key, ok := keyToken.(string)
		if !ok {
			return false
		}
		if strings.EqualFold(key, "credentials") {
			if key != "credentials" || seen {
				return true
			}
			seen = true
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return false
		}
	}
	return false
}

func outcomeJSON(o store.DeviceCreateOutcome) createOutcomeJSON {
	v := createOutcomeJSON{OperationID: o.OperationID, Status: o.Status, Reason: o.Reason, Endpoint: o.Endpoint, CreatedAt: o.CreatedAt}
	if o.Fingerprint != "" {
		v.Fingerprint = &o.Fingerprint
	}
	if !o.CompletedAt.IsZero() {
		v.CompletedAt = &o.CompletedAt
	}
	return v
}
func (o Options) deviceCreates(w http.ResponseWriter, r *http.Request) {
	scope := "devices:write"
	RequireScope(scope)(http.HandlerFunc(o.serveDeviceCreates)).ServeHTTP(w, r)
}
func (o Options) serveDeviceCreates(w http.ResponseWriter, r *http.Request) {
	id, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/device-creates/"))
	if err != nil || len(id) < 1 || len(id) > 128 || strings.Trim(id, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._~-") != "" {
		WriteProblem(w, 400, "invalid-argument", "Invalid operation ID")
		return
	}
	s := o.DeviceCreates
	if s == nil {
		WriteProblem(w, 500, "internal_error", "Device create store unavailable")
		return
	}
	tenant := TenantFrom(r.Context())
	// A slow client must not hold the tenant admission lock while sending data.
	var body any
	var raw json.RawMessage
	var invalidJSON bool
	if r.Method == http.MethodPut {
		dec := json.NewDecoder(r.Body)
		decodeErr := dec.Decode(&raw)
		var extra any
		invalidJSON = decodeErr != nil || dec.Decode(&extra) != io.EOF
		if !invalidJSON {
			bodyDecoder := json.NewDecoder(bytes.NewReader(raw))
			bodyDecoder.UseNumber()
			invalidJSON = bodyDecoder.Decode(&body) != nil
		}
	}
	// Serialize outcomes with normal device creation and outcome deletion so a
	// replay cannot become a new create after the capacity check is skipped.
	unlock := o.limits.lock(tenant)
	defer unlock()
	var result store.DeviceCreateOutcome
	status := 200
	switch r.Method {
	case http.MethodGet:
		result, err = s.GetOrFenceDeviceCreate(r.Context(), tenant, id)
	case http.MethodDelete:
		err = s.DeleteDeviceCreate(r.Context(), tenant, id)
		if errors.Is(err, store.ErrNotFound) {
			WriteProblem(w, 404, "not_found", "Outcome not found")
			return
		}
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	case http.MethodPut:
		existing, lookupErr := s.GetDeviceCreate(r.Context(), tenant, id)
		if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
			WriteProblem(w, 500, "internal_error", "Cannot process device create outcome")
			return
		}
		if lookupErr == nil && existing.Reason == "fenced" {
			if skip, ok := r.Context().Value(createAuditKey{}).(*bool); ok {
				*skip = true
			}
			deviceJSON(w, http.StatusOK, outcomeJSON(existing))
			return
		}
		if invalidJSON {
			WriteProblem(w, 400, "invalid_json", "Expected one JSON value")
			return
		}
		if ambiguousCredentialKeys(raw) {
			WriteProblem(w, 400, "invalid_json", "Ambiguous credentials field")
			return
		}
		credentialBody := false
		if object, ok := body.(map[string]any); ok {
			_, credentialBody = object["credentials"]
		}
		if (credentialBody || strings.HasPrefix(existing.Fingerprint, "salted-sha256:")) && !authorizeDeviceReplayCredentials(w, r) {
			return
		}
		canonical, e := json.Marshal(body)
		if e != nil {
			WriteProblem(w, 400, "invalid_json", "Invalid JSON")
			return
		}
		digest := sha256.Sum256(canonical)
		fingerprint := "sha256:" + hex.EncodeToString(digest[:])
		if credentialBody {
			var salt [16]byte
			parts := strings.Split(existing.Fingerprint, ":")
			if len(parts) == 3 && parts[0] == "salted-sha256" {
				decoded, err := hex.DecodeString(parts[1])
				if err != nil || len(decoded) != len(salt) {
					WriteProblem(w, 500, "internal_error", "Invalid stored fingerprint")
					return
				}
				copy(salt[:], decoded)
			} else if _, err := rand.Read(salt[:]); err != nil {
				WriteProblem(w, 500, "internal_error", "Cannot fingerprint create")
				return
			}
			fingerprint = saltedCreateFingerprint(canonical, salt[:])
		}
		var input struct {
			Endpoint    string            `json:"endpoint"`
			Name        string            `json:"name"`
			Labels      map[string]string `json:"labels"`
			Credentials json.RawMessage   `json:"credentials"`
		}
		inputDecoder := json.NewDecoder(bytes.NewReader(canonical))
		inputDecoder.DisallowUnknownFields()
		var inserted bool
		if e := inputDecoder.Decode(&input); e != nil || strings.TrimSpace(input.Endpoint) == "" {
			result, inserted, err = s.RecordDeviceCreateNotCreated(r.Context(), tenant, id, fingerprint, "invalid")
		} else {
			if lookupErr == nil {
				if existing.Reason != "fenced" && existing.Fingerprint != fingerprint {
					WriteProblem(w, 409, "operation-id-reused", "Operation ID already used")
					return
				}
				if skip, ok := r.Context().Value(createAuditKey{}).(*bool); ok {
					*skip = true
				}
				deviceJSON(w, http.StatusOK, outcomeJSON(existing))
				return
			}
			if errors.Is(lookupErr, store.ErrNotFound) {
				if t, ok := o.limits.tenants.Get(tenant); ok && t.Limits.Devices > 0 {
					reached, err := o.deviceLimitReached(tenant, t.Limits.Devices)
					if err != nil {
						WriteProblem(w, 500, "internal_error", "Cannot list devices")
						return
					}
					if reached {
						w.Header().Set("Retry-After", "60")
						WriteProblem(w, 429, "limit_exceeded", "Device limit exceeded")
						return
					}
				}
			}
			device := store.Device{Endpoint: input.Endpoint, Name: input.Name, Labels: input.Labels}
			if credentialBody {
				validation := &validationOutcomeWriter{ResponseWriter: w}
				security, ok := o.decodeInlineCredentials(validation, r, tenant, input.Endpoint, input.Credentials)
				if !ok && !validation.invalid {
					return
				}
				if validation.invalid {
					result, inserted, err = s.RecordDeviceCreateNotCreated(r.Context(), tenant, id, fingerprint, "invalid")
				} else {
					creator, ok := o.Devices.(store.DeviceSecurityCreateStore)
					owner, owned := o.Devices.(interface {
						OwnsSecurity(server.SecurityStore) bool
					})
					outcomes, hasOutcomes := o.Devices.(store.DeviceCreateStore)
					var live server.SecurityStore
					if o.Server != nil {
						live = o.Server.Security()
					}
					if wrapped, ok := live.(*coap.RevokingSecurityStore); ok && wrapped != nil {
						live = wrapped.SecurityStore
					}
					if !ok || !owned || !hasOutcomes || !reflect.TypeOf(outcomes).Comparable() || outcomes != s || live == nil || !owner.OwnsSecurity(live) {
						WriteProblem(w, 501, "inline_credentials_unavailable", "Atomic inline credentials are unavailable")
						return
					}
					result, inserted, err = creator.CreateDeviceWithSecurity(r.Context(), tenant, id, fingerprint, device, *security)
					if errors.Is(err, store.ErrCredentialExists) {
						WriteProblem(w, 409, "credential_exists", "Credential already exists")
						return
					}
				}
			} else {
				result, inserted, err = s.CreateDeviceWithOutcome(r.Context(), tenant, id, fingerprint, device)
			}
			if inserted && err == nil && result.Status == "committed" {
				status = 201
				if d, e := o.Devices.Get(tenant, input.Endpoint); e == nil {
					o.emitDevice(tenant, "lwm2m.device.created", d)
				}
			}
		}
		// Different server processes can race to choose a salt for the same ID.
		// Compare again using the winner's salt after the atomic store rejects it.
		if errors.Is(err, store.ErrOperationIDReused) {
			winner, readErr := s.GetDeviceCreate(r.Context(), tenant, id)
			if readErr == nil && strings.HasPrefix(winner.Fingerprint, "salted-sha256:") {
				if !credentialBody && !authorizeDeviceReplayCredentials(w, r) {
					return
				}
				parts := strings.Split(winner.Fingerprint, ":")
				if credentialBody && len(parts) == 3 {
					salt, decodeErr := hex.DecodeString(parts[1])
					if decodeErr == nil && len(salt) == 16 && saltedCreateFingerprint(canonical, salt) == winner.Fingerprint {
						result, inserted, err = winner, false, nil
					}
				}
			}
		}
		if !inserted && err == nil {
			if skip, ok := r.Context().Value(createAuditKey{}).(*bool); ok {
				*skip = true
			}
		}
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET, PUT or DELETE")
		return
	}
	if errors.Is(err, store.ErrOperationIDReused) {
		WriteProblem(w, 409, "operation-id-reused", "Operation ID already used")
		return
	}
	if err != nil {
		WriteProblem(w, 500, "internal_error", "Cannot process device create outcome")
		return
	}
	deviceJSON(w, status, outcomeJSON(result))
}
