package apiv1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

type createAuditKey struct{}
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
		var body any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		decodeErr := dec.Decode(&body)
		var extra any
		if decodeErr != nil || dec.Decode(&extra) != io.EOF {
			existing, lookupErr := s.GetDeviceCreate(r.Context(), tenant, id)
			if lookupErr == nil && existing.Reason == "fenced" {
				if skip, ok := r.Context().Value(createAuditKey{}).(*bool); ok {
					*skip = true
				}
				deviceJSON(w, http.StatusOK, outcomeJSON(existing))
				return
			}
			if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
				WriteProblem(w, 500, "internal_error", "Cannot process device create outcome")
				return
			}
			WriteProblem(w, 400, "invalid_json", "Expected one JSON value")
			return
		}
		canonical, e := json.Marshal(body)
		if e != nil {
			WriteProblem(w, 400, "invalid_json", "Invalid JSON")
			return
		}
		digest := sha256.Sum256(canonical)
		fingerprint := "sha256:" + hex.EncodeToString(digest[:])
		var input struct {
			Endpoint string            `json:"endpoint"`
			Name     string            `json:"name"`
			Labels   map[string]string `json:"labels"`
		}
		inputDecoder := json.NewDecoder(bytes.NewReader(canonical))
		inputDecoder.DisallowUnknownFields()
		var inserted bool
		if e := inputDecoder.Decode(&input); e != nil || strings.TrimSpace(input.Endpoint) == "" {
			result, inserted, err = s.RecordDeviceCreateNotCreated(r.Context(), tenant, id, fingerprint, "invalid")
		} else {
			result, inserted, err = s.CreateDeviceWithOutcome(r.Context(), tenant, id, fingerprint, store.Device{Endpoint: input.Endpoint, Name: input.Name, Labels: input.Labels})
			if inserted && err == nil && result.Status == "committed" {
				status = 201
				if d, e := o.Devices.Get(tenant, input.Endpoint); e == nil {
					o.emitDevice(tenant, "lwm2m.device.created", d)
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
