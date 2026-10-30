package apiv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/attr"
	"github.com/fiumaralabs/lwm2m/internal/id"
)

// profileCreateInput omits the server-assigned ID.
type profileCreateInput struct {
	Name              string          `json:"name"`
	Observations      json.RawMessage `json:"observations"`
	Attributes        json.RawMessage `json:"attributes"`
	OnRegister        json.RawMessage `json:"onRegister"`
	Timeseries        json.RawMessage `json:"timeseries"`
	BootstrapTemplate json.RawMessage `json:"bootstrapTemplate"`
}

func (o Options) createDeviceProfile(w http.ResponseWriter, r *http.Request) {
	tenant, key := TenantFrom(r.Context()), r.Header.Get("Idempotency-Key")
	respond := func(p DeviceProfile) {
		b, err := json.Marshal(p)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Unable to encode profile")
			return
		}
		sum := sha256.Sum256(b)
		w.Header().Set("ETag", fmt.Sprintf("\"profile-v1-%x\"", sum))
		w.Header().Set("Location", "/v1/device-profiles/"+p.ID)
		deviceJSON(w, 201, p)
	}
	if p, ok := o.DeviceProfiles.Replay(tenant, key); ok {
		respond(p)
		return
	}
	var in *profileCreateInput
	if !DecodeJSON(w, r, &in) {
		return
	}
	if in == nil || strings.TrimSpace(in.Name) == "" || !validProfileInput(in) {
		WriteProblem(w, 400, "invalid_profile", "Invalid device profile")
		return
	}
	p := DeviceProfile{ID: id.New("dp"), Name: in.Name, Observations: in.Observations, Attributes: in.Attributes, OnRegister: in.OnRegister, Timeseries: in.Timeseries, BootstrapTemplate: in.BootstrapTemplate}
	p, err := o.DeviceProfiles.CreateIdempotent(tenant, p, key)
	if err != nil {
		WriteProblem(w, 500, "internal_error", "Unable to create profile")
		return
	}
	respond(p)
}

func profileDecode(raw json.RawMessage, target any) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(target) == nil
}
func validProfileInput(in *profileCreateInput) bool {
	if len(in.Observations) > 0 {
		var obs []struct {
			Paths      []string                   `json:"paths"`
			Attributes map[string]json.RawMessage `json:"attributes"`
			Persistent *bool                      `json:"persistent"`
			Prefix     string                     `json:"prefix"`
			Format     string                     `json:"format"`
			Accept     string                     `json:"accept"`
		}
		if string(in.Observations) == "null" || !profileDecode(in.Observations, &obs) {
			return false
		}
		for _, x := range obs {
			if validateObservation(x.Paths, x.Attributes) != nil {
				return false
			}
		}
	}
	if len(in.Attributes) > 0 {
		var attrs []struct {
			Path       string                     `json:"path"`
			Attributes map[string]json.RawMessage `json:"attributes"`
		}
		if string(in.Attributes) == "null" || !profileDecode(in.Attributes, &attrs) {
			return false
		}
		for _, x := range attrs {
			if validateObservation([]string{x.Path}, x.Attributes) != nil {
				return false
			}
		}
	}
	if len(in.OnRegister) > 0 {
		var ops []json.RawMessage
		if string(in.OnRegister) == "null" || json.Unmarshal(in.OnRegister, &ops) != nil {
			return false
		}
		for _, op := range ops {
			if !validProfileOperation(op) {
				return false
			}
		}
	}
	if len(in.Timeseries) > 0 {
		var ts struct {
			RetentionDays *int  `json:"retentionDays"`
			FromReads     *bool `json:"fromReads"`
		}
		if string(in.Timeseries) == "null" || !profileDecode(in.Timeseries, &ts) || ts.RetentionDays != nil && *ts.RetentionDays < 0 {
			return false
		}
	}
	if len(in.BootstrapTemplate) > 0 {
		var config map[string]any
		if !validProfileBootstrap(in.BootstrapTemplate) || json.Unmarshal(in.BootstrapTemplate, &config) != nil || config == nil {
			return false
		}
		before, err := json.Marshal(config)
		if err != nil {
			return false
		}
		redactBootstrap(config)
		after, err := json.Marshal(config)
		if err != nil || !bytes.Equal(before, after) {
			return false
		}
	}
	return true
}
func validProfileOperation(raw json.RawMessage) bool {
	var op struct {
		Type            string                     `json:"type"`
		Path            string                     `json:"path"`
		Paths           []string                   `json:"paths"`
		Values          []json.RawMessage          `json:"values"`
		Mode            string                     `json:"mode"`
		Arguments       string                     `json:"arguments"`
		Depth           *int                       `json:"depth"`
		Attributes      map[string]json.RawMessage `json:"attributes"`
		ACLInstance     *int                       `json:"aclInstance"`
		ShortServerID   *int                       `json:"shortServerId"`
		Rights          []string                   `json:"rights"`
		LifetimeSeconds *int                       `json:"lifetimeSeconds"`
		Binding         string                     `json:"binding"`
		Suite           *int                       `json:"suite"`
		ViaPackage      *bool                      `json:"viaPackage"`
		Method          string                     `json:"method"`
		Query           []string                   `json:"query"`
		Format          string                     `json:"format"`
		Accept          string                     `json:"accept"`
		Payload         string                     `json:"payload"`
		TTLSeconds      *int                       `json:"ttlSeconds"`
		WhenOffline     string                     `json:"whenOffline"`
		Prefix          string                     `json:"prefix"`
	}
	if !profileDecode(raw, &op) {
		return false
	}
	validPath := func(s string) bool {
		p, err := lwm2m.ParsePath(s)
		return err == nil && strings.HasPrefix(s, "/") && !p.IsRoot()
	}
	switch op.Type {
	case "read":
		if len(op.Paths) == 0 {
			return false
		}
		for _, p := range op.Paths {
			if !validPath(p) {
				return false
			}
		}
	case "write", "create":
		if len(op.Values) == 0 || op.Type == "create" && !validPath(op.Path) || op.Path != "" && !validPath(op.Path) {
			return false
		}
		if op.Type == "create" {
			path, _ := lwm2m.ParsePath(op.Path)
			if !path.IsObject() {
				return false
			}
		}
		for _, rawValue := range op.Values {
			var value struct {
				Path      string          `json:"path"`
				Type      string          `json:"type"`
				Value     json.RawMessage `json:"value"`
				Prefix    string          `json:"prefix"`
				Timestamp *time.Time      `json:"timestamp"`
			}
			if !profileDecode(rawValue, &value) || !validPath(value.Path) {
				return false
			}
			path, _ := lwm2m.ParsePath(value.Path)
			if len(value.Value) == 0 {
				// An untyped empty instance or multi-instance resource is legal (§7.1).
				if value.Type != "" || !(path.IsInstance() || path.IsResource()) {
					return false
				}
			} else if !(path.IsResource() || path.IsResourceInstance()) || string(value.Value) == "null" || value.Value[0] == '{' || value.Value[0] == '[' || !validBootstrapValue(value.Type, value.Value) {
				return false
			}
		}
		if op.Mode != "" && op.Mode != "partial" && op.Mode != "replace" {
			return false
		}
	case "execute", "discover":
		if !validPath(op.Path) {
			return false
		}
	case "delete":
		p, err := lwm2m.ParsePath(op.Path)
		if err != nil || !strings.HasPrefix(op.Path, "/") || !p.IsInstance() {
			return false
		}
	case "write-attributes":
		if op.Attributes == nil {
			return false
		}
		attrs := make(map[string]json.RawMessage)
		for k, v := range op.Attributes {
			if !attr.IsNotification(k) {
				return false
			}
			if strings.TrimSpace(string(v)) != "null" {
				attrs[k] = v
			}
		}
		if validateObservation([]string{op.Path}, attrs) != nil {
			return false
		}
	case "raw":
		if !validPath(op.Path) {
			return false
		}
		switch op.Method {
		case "GET", "PUT", "POST", "DELETE", "FETCH", "iPATCH":
		default:
			return false
		}
	case "set-rights":
		if op.ACLInstance == nil || op.ShortServerID == nil || op.Rights == nil {
			return false
		}
		for _, right := range op.Rights {
			switch right {
			case "read", "write", "execute", "delete", "create":
			default:
				return false
			}
		}
	case "set-lifetime":
		if op.LifetimeSeconds == nil || *op.LifetimeSeconds < 0 {
			return false
		}
	case "set-binding", "set-preferred-transport":
		if op.Binding == "" {
			return false
		}
	case "set-profile-hash-algorithm":
		if op.Suite == nil {
			return false
		}
	case "read-acl", "advertise-versions", "trigger-update", "trigger-bootstrap", "disable", "sync-clock", "firmware-reset":
	default:
		return false
	}
	return true
}
