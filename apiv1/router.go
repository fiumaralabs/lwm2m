package apiv1

import (
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"net/http"
	"strings"
)

// Options configures the native API and its server information.
type Options struct {
	EventOptions
	Devices store.Devices  `json:"-"`
	Server  *server.Server `json:"-"`
	// ServerTenant identifies the sole tenant served by Server (default: default).
	ServerTenant  string                       `json:"-"`
	Keys          Keys                         `json:"-"`
	Version       string                       `json:"version"`
	LwM2MVersions []string                     `json:"lwm2mVersions"`
	Capabilities  map[string]bool              `json:"capabilities"`
	Endpoints     map[string]map[string]string `json:"endpoints"`
}

// New returns the authenticated native API handler.
func New(o Options) http.Handler {
	if o.Devices == nil {
		o.Devices = &memory.Devices{}
	}
	if o.ServerTenant == "" {
		o.ServerTenant = "default"
	}

	events := o.eventHandler()
	return Authenticate(o.Keys, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" {
			events.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/api-keys" || strings.HasPrefix(r.URL.Path, "/v1/api-keys/") {
			RequireScope("admin")(http.HandlerFunc(serveAPIKeys)).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/devices" || strings.HasPrefix(r.URL.Path, "/v1/devices/") {
			if strings.HasSuffix(r.URL.EscapedPath(), "/events") && r.URL.EscapedPath() != "/v1/devices/events" {
				o.deviceEvents(w, r)
				return
			}
			o.devices(w, r)
			return
		}
		if r.URL.Path == "/v1/info" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				WriteProblem(w, 405, "method_not_allowed", "Expected GET")
				return
			}
			o.info(w, r)
			return
		}
		WriteProblem(w, 404, "not_found", "Unknown API path")
	}))
}
