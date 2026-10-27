package apiv1

import (
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"net/http"
	"strings"
)

// Options configures the native API and its server information.
type Options struct {
	// When supplying Executor, also pass this store in its ExecutorOptions.State.
	State    store.State    `json:"-"`
	Settings store.Settings `json:"-"`
	EventOptions
	Webhooks     store.Webhooks       `json:"-"`
	Observations store.Observations   `json:"-"`
	Operations   store.Operations     `json:"-"`
	Executor     *operations.Executor `json:"-"`
	Devices      store.Devices        `json:"-"`
	Server       *server.Server       `json:"-"`
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
	if o.State == nil {
		o.State = &memory.State{}
	}
	if o.Observations == nil {
		o.Observations = &memory.Observations{}
	}
	if o.Settings == nil {
		o.Settings = &memory.Settings{}
	}
	if o.Webhooks == nil {
		o.Webhooks = &memory.Webhooks{}
	}
	if o.Devices == nil {
		o.Devices = &memory.Devices{}
	}
	if o.ServerTenant == "" {
		o.ServerTenant = "default"
	}

	events := o.eventHandler()
	if o.Operations == nil {
		o.Operations = memory.NewOperations()
	}
	if o.Executor == nil && o.Server != nil {
		o.Executor = operations.NewExecutor(o.Server, o.Operations, operations.ExecutorOptions{State: o.State})
	}
	ops := o.operationHandler()
	models := o.modelHandler()
	return Authenticate(o.Keys, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/settings" {
			o.settings(w, r)
			return
		}
		if r.URL.Path == "/v1/webhooks" || strings.HasPrefix(r.URL.Path, "/v1/webhooks/") {
			RequireScope("admin")(http.HandlerFunc(o.webhooks)).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/operations" || strings.HasPrefix(r.URL.Path, "/v1/operations/") || strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(r.URL.EscapedPath(), "/operations") && r.URL.EscapedPath() != "/v1/devices/operations" {
			ops.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/events" {
			events.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/observations/") || strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(r.URL.EscapedPath(), "/observations") && r.URL.EscapedPath() != "/v1/devices/observations" {
			o.observations(w, r)
			return
		}
		if r.URL.Path == "/v1/api-keys" || strings.HasPrefix(r.URL.Path, "/v1/api-keys/") {
			RequireScope("admin")(http.HandlerFunc(serveAPIKeys)).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/devices" || strings.HasPrefix(r.URL.Path, "/v1/devices/") {
			if strings.HasSuffix(r.URL.EscapedPath(), "/state") && r.URL.EscapedPath() != "/v1/devices/state" {
				o.state(w, r)
				return
			}
			if strings.HasSuffix(r.URL.EscapedPath(), "/model") && r.URL.EscapedPath() != "/v1/devices/model" {
				models.ServeHTTP(w, r)
				return
			}
			if strings.HasSuffix(r.URL.EscapedPath(), "/registration") && r.URL.EscapedPath() != "/v1/devices/registration" {
				o.registration(w, r)
				return
			}
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
