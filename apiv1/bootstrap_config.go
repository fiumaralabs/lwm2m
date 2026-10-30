package apiv1

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/fiumaralabs/lwm2m/store"
)

// BootstrapConfigStore owns copies of complete JSON configurations per tenant and endpoint.
type BootstrapConfigStore interface {
	Put(tenant, endpoint string, config json.RawMessage)
	Get(tenant, endpoint string) (json.RawMessage, bool)
	Delete(tenant, endpoint string) bool
}

type memoryBootstrapConfigs struct {
	mu   sync.RWMutex
	data map[string]map[string]json.RawMessage
}

func NewMemoryBootstrapConfigStore() BootstrapConfigStore {
	return &memoryBootstrapConfigs{data: make(map[string]map[string]json.RawMessage)}
}
func (s *memoryBootstrapConfigs) Put(t, ep string, b json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data[t] == nil {
		s.data[t] = make(map[string]json.RawMessage)
	}
	s.data[t][ep] = bytes.Clone(b)
}
func (s *memoryBootstrapConfigs) Get(t, ep string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.data[t][ep]
	return bytes.Clone(b), ok
}
func (s *memoryBootstrapConfigs) Delete(t, ep string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[t][ep]
	delete(s.data[t], ep)
	return ok
}

type bootstrapInput struct {
	Servers []struct {
		ShortServerID    *uint16 `json:"shortServerId"`
		SecurityInstance *uint16 `json:"securityInstance"`
		ServerInstance   *uint16 `json:"serverInstance"`
		Security         struct {
			RPK json.RawMessage `json:"rpk"`
			PSK *struct {
				Key      string `json:"key"`
				Generate bool   `json:"generate"`
			} `json:"psk"`
		} `json:"security"`
	} `json:"servers"`
	ACLs []struct {
		Owner *uint16 `json:"owner"`
	} `json:"acls"`
}

func validateBootstrap(c bootstrapInput) (string, []FieldError) {
	ids, sec, srv := map[uint16]bool{}, map[uint16]bool{}, map[uint16]bool{}
	var errs []FieldError
	add := func(field, msg string) { errs = append(errs, FieldError{Field: field, Message: msg}) }
	for i, s := range c.Servers {
		p := fmt.Sprintf("servers[%d]", i)
		if len(s.Security.RPK) != 0 && string(s.Security.RPK) != "null" {
			return "rpk_unsupported", nil
		}
		if s.Security.PSK != nil && s.Security.PSK.Generate {
			return "psk_generate_unsupported", nil
		}
		for _, item := range []struct {
			v    *uint16
			seen map[uint16]bool
			name string
		}{{s.ShortServerID, ids, "shortServerId"}, {s.SecurityInstance, sec, "securityInstance"}, {s.ServerInstance, srv, "serverInstance"}} {
			if item.v == nil {
				if item.name == "shortServerId" {
					add(p+"."+item.name, "required")
				}
				continue
			}
			if item.seen[*item.v] {
				add(p+"."+item.name, "must be unique")
			}
			item.seen[*item.v] = true
		}
		if s.Security.PSK != nil {
			key := s.Security.PSK.Key
			var b []byte
			var err error
			switch {
			case strings.HasPrefix(key, "hex:"):
				b, err = hex.DecodeString(key[4:])
			case strings.HasPrefix(key, "base64:"):
				b, err = base64.StdEncoding.DecodeString(key[7:])
			default:
				err = errors.New("prefix required")
			}
			if err != nil || len(b) < 16 {
				add(p+".security.psk.key", "must be hex: or base64: encoded and at least 16 bytes")
			}
		}
	}
	for i, a := range c.ACLs {
		if a.Owner == nil || !ids[*a.Owner] {
			add(fmt.Sprintf("acls[%d].owner", i), "must reference a provisioned server")
		}
	}
	return "", errs
}

func redactBootstrap(v any) {
	redactBootstrapValue(v, false)
}

func redactBootstrapValue(v any, sensitive bool) {
	switch x := v.(type) {
	case map[string]any:
		if path, ok := x["path"].(string); ok {
			for _, root := range []string{"/0", "/21"} {
				if path == root || strings.HasPrefix(path, root+"/") {
					sensitive = true
				}
			}
		}
		for k, child := range x {
			if strings.EqualFold(k, "key") || strings.EqualFold(k, "privateKey") || strings.EqualFold(k, "masterSecret") || (sensitive && k == "value") {
				delete(x, k)
			} else {
				redactBootstrapValue(child, sensitive)
			}
		}
	case []any:
		for _, child := range x {
			redactBootstrapValue(child, sensitive)
		}
	}
}
func (o Options) bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "PUT" && r.Method != "DELETE" {
		w.Header().Set("Allow", "GET, PUT, DELETE")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET, PUT or DELETE")
		return
	}
	scope := "devices:read"
	if r.Method != "GET" {
		scope = "bootstrap:write"
	}
	RequireScope(scope)(http.HandlerFunc(o.serveBootstrap)).ServeHTTP(w, r)
}
func (o Options) serveBootstrap(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/bootstrap")
	ep, err := url.PathUnescape(raw)
	if err != nil || ep == "" || strings.Contains(raw, "/") {
		WriteProblem(w, 404, "not_found", "Unknown device path")
		return
	}
	tenant := TenantFrom(r.Context())
	if _, err = o.Devices.Get(tenant, ep); err != nil {
		if errors.Is(err, store.ErrDeviceNotFound) {
			WriteProblem(w, 404, "not_found", "Device not found")
		} else {
			WriteProblem(w, 500, "internal_error", "Cannot get device")
		}
		return
	}
	switch r.Method {
	case "PUT":
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		var input bootstrapInput
		if err != nil || json.Unmarshal(b, &input) != nil || len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '{' {
			WriteProblem(w, 400, "invalid_json", "Invalid bootstrap config")
			return
		}
		code, fields := validateBootstrap(input)
		if code != "" {
			WriteProblem(w, 501, code, "Bootstrap security mode is not supported")
			return
		}
		if len(fields) > 0 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(422)
			_ = json.NewEncoder(w).Encode(Problem{Type: "about:blank", Title: http.StatusText(422), Status: 422, Detail: "Invalid bootstrap config", Code: "validation_error", Errors: fields})
			return
		}
		o.BootstrapConfigs.Put(tenant, ep, b)
		w.WriteHeader(204)
	case "GET":
		b, ok := o.BootstrapConfigs.Get(tenant, ep)
		if !ok {
			WriteProblem(w, 404, "not_found", "Bootstrap config not found")
			return
		}
		var v any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if d.Decode(&v) != nil {
			WriteProblem(w, 500, "internal_error", "Invalid stored bootstrap config")
			return
		}
		redactBootstrap(v)
		deviceJSON(w, 200, v)
	case "DELETE":
		if !o.BootstrapConfigs.Delete(tenant, ep) {
			WriteProblem(w, 404, "not_found", "Bootstrap config not found")
			return
		}
		w.WriteHeader(204)
	}
}
