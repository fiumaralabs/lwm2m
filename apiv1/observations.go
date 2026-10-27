package apiv1

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/attr"
	"github.com/fiumaralabs/lwm2m/store"
)

type observationInput struct {
	Paths      []string                   `json:"paths"`
	Attributes map[string]json.RawMessage `json:"attributes"`
	Persistent *bool                      `json:"persistent"`
	Prefix     string                     `json:"prefix"`
	Format     string                     `json:"format"`
	Accept     string                     `json:"accept"`
}

func (o Options) observations(w http.ResponseWriter, r *http.Request) {
	scope := "operations:write"
	if r.Method == "GET" {
		scope = "devices:read"
	}
	RequireScope(scope)(http.HandlerFunc(o.serveObservations)).ServeHTTP(w, r)
}
func (o Options) serveObservations(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	raw := r.URL.EscapedPath()
	if strings.HasPrefix(raw, "/v1/devices/") {
		epRaw := strings.TrimSuffix(strings.TrimPrefix(raw, "/v1/devices/"), "/observations")
		ep, err := url.PathUnescape(epRaw)
		if err != nil || ep == "" || strings.Contains(epRaw, "/") {
			WriteProblem(w, 404, "not_found", "Unknown device path")
			return
		}
		if _, err = o.Devices.Get(tenant, ep); err != nil {
			observationError(w, err)
			return
		}
		switch r.Method {
		case "GET":
			items, err := o.Observations.List(tenant, ep)
			if err != nil {
				observationError(w, err)
				return
			}
			deviceJSON(w, 200, map[string]any{"items": items})
		case "POST":
			var in observationInput
			if !DecodeJSON(w, r, &in) {
				return
			}
			if err := validateObservation(in.Paths, in.Attributes); err != nil {
				WriteProblem(w, 400, "invalid_observation", err.Error())
				return
			}
			persistent := true
			if in.Persistent != nil {
				persistent = *in.Persistent
			}
			if in.Attributes == nil {
				in.Attributes = map[string]json.RawMessage{}
			}
			v := store.Observation{Tenant: tenant, ID: "obs_" + rand.Text(), Device: ep, Paths: in.Paths, Attributes: in.Attributes, Persistent: persistent, Status: "pending", Source: "api", Prefix: in.Prefix, Format: in.Format, Accept: in.Accept}
			if err := o.Observations.Create(tenant, v); err != nil {
				observationError(w, err)
				return
			}
			w.Header().Set("Location", "/v1/observations/"+v.ID)
			deviceJSON(w, 201, v)
		default:
			observationMethod(w, "GET, POST")
		}
		return
	}
	id := strings.TrimPrefix(raw, "/v1/observations/")
	if id == raw || id == "" || strings.Contains(id, "/") {
		WriteProblem(w, 404, "not_found", "Unknown observation path")
		return
	}
	v, err := o.Observations.Get(tenant, id)
	if err != nil {
		observationError(w, err)
		return
	}
	switch r.Method {
	case "GET":
		deviceJSON(w, 200, v)
	case "PATCH":
		var in struct {
			Attributes map[string]json.RawMessage `json:"attributes"`
		}
		if !DecodeJSON(w, r, &in) {
			return
		}
		if in.Attributes == nil {
			WriteProblem(w, 400, "invalid_observation", "attributes must be an object")
			return
		}
		if err := validateObservation(v.Paths, in.Attributes); err != nil {
			WriteProblem(w, 400, "invalid_observation", err.Error())
			return
		}
		updated, err := o.Observations.UpdateAttributes(tenant, id, in.Attributes)
		if err != nil {
			observationError(w, err)
			return
		}
		deviceJSON(w, 200, updated)
	case "DELETE":
		mode := r.URL.Query().Get("mode")
		if mode != "" && mode != "active" && mode != "passive" {
			WriteProblem(w, 400, "invalid_mode", "Expected active or passive mode")
			return
		}
		if err := o.Observations.End(tenant, id); err != nil {
			observationError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		observationMethod(w, "GET, PATCH, DELETE")
	}
}
func validateObservation(paths []string, attributes map[string]json.RawMessage) error {
	if len(paths) == 0 {
		return fmt.Errorf("paths must not be empty")
	}
	query := []string{}
	for name, value := range attributes {
		raw := strings.TrimSpace(string(value))
		if !attr.IsNotification(name) || raw == "" || raw[0] == '"' {
			return fmt.Errorf("invalid numeric attribute %s", name)
		}
		var n json.Number
		if err := json.Unmarshal(value, &n); err != nil || n == "" {
			return fmt.Errorf("invalid numeric attribute %s", name)
		}
		query = append(query, name+"="+string(n))
	}
	var attrs attr.Attrs
	if len(query) > 0 {
		var err error
		attrs, err = attr.ParseQuery(strings.Join(query, "&"))
		if err != nil {
			return err
		}
	}
	seen := map[lwm2m.Path]bool{}
	for _, raw := range paths {
		p, err := lwm2m.ParsePath(raw)
		if err != nil || !strings.HasPrefix(raw, "/") || p.IsRoot() {
			return fmt.Errorf("invalid absolute observation path %q", raw)
		}
		if seen[p] {
			return fmt.Errorf("duplicate observation path %q", raw)
		}
		seen[p] = true
		if err := attr.Validate(p, lwm2m.TypeNone, attrs); err != nil {
			return err
		}
	}
	return nil
}

func observationMethod(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	WriteProblem(w, 405, "method_not_allowed", "Unsupported observation method")
}
func observationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrDeviceNotFound), errors.Is(err, store.ErrTenant):
		WriteProblem(w, 404, "not_found", "Observation or device not found")
	case errors.Is(err, store.ErrObservationProfile):
		WriteProblem(w, 403, "forbidden", "Profile observations cannot be deleted individually")
	default:
		WriteProblem(w, 500, "internal_error", "Observation store failed")
	}
}
