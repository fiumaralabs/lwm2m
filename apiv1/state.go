package apiv1

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/store"
)

func (o Options) state(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET")
		return
	}
	RequireScope("devices:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/state")
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
		var paths []string
		if raw, ok := r.URL.Query()["paths"]; ok {
			paths = strings.Split(strings.Join(raw, ","), ",")
			for _, p := range paths {
				if _, err := lwm2m.ParsePath(p); err != nil {
					WriteProblem(w, 400, "invalid_path", "Invalid state path")
					return
				}
			}
		}
		values, err := o.State.List(tenant, ep, r.URL.Query().Get("prefix"), paths)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot get state")
			return
		}
		deviceJSON(w, 200, struct {
			Values []store.StateValue `json:"values"`
		}{values})
	})).ServeHTTP(w, r)
}
