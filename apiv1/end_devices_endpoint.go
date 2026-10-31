package apiv1

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/gateway"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/store"
)

// GatewayReader supplies a registration snapshot and its decoded /25 instances.
// Implementations must scope both reads to tenant and return owned snapshots.
type GatewayReader interface {
	ReadGateway(ctx context.Context, tenant, endpoint string) ([]link.Link, []gateway.Device, error)
}

func prefixedObjects(links []link.Link) map[string][]link.Object {
	result := make(map[string][]link.Object)
	for _, l := range links {
		prefix, path, err := lwm2m.ParsePrefixedPath(l.URI)
		if err != nil || prefix == "" || path.Len() > 2 {
			continue
		}
		l.URI = path.String()
		registration, err := link.ParseRegistration([]link.Link{l})
		if err != nil || len(registration.Objects) != 1 {
			continue
		}
		obj := registration.Objects[0]
		objects := result[prefix]
		i := slices.IndexFunc(objects, func(o link.Object) bool { return o.ID == obj.ID })
		if i < 0 {
			objects = append(objects, link.Object{ID: obj.ID, Instances: []uint16{}})
			i = len(objects) - 1
		}
		if obj.Version != "" {
			objects[i].Version = obj.Version
		}
		for _, instance := range obj.Instances {
			if !slices.Contains(objects[i].Instances, instance) {
				objects[i].Instances = append(objects[i].Instances, instance)
			}
		}
		result[prefix] = objects
	}
	return result
}

type endDeviceView struct {
	Prefix   string            `json:"prefix"`
	ID       string            `json:"id"`
	Instance uint16            `json:"instance"`
	Objects  []endDeviceObject `json:"objects"`
}
type endDeviceObject struct {
	ID        uint16   `json:"id"`
	Version   string   `json:"version,omitempty"`
	Instances []uint16 `json:"instances"`
}

// endDevicesHandler is intentionally not routed here. The router supplies it later.
func endDevicesHandler(devices store.Devices, source GatewayReader) http.Handler {
	return RequireScope("devices:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET")
			return
		}
		raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/end-devices")
		ep, err := url.PathUnescape(raw)
		if err != nil || ep == "" || strings.Contains(raw, "/") || !strings.HasPrefix(r.URL.EscapedPath(), "/v1/devices/") || !strings.HasSuffix(r.URL.EscapedPath(), "/end-devices") {
			WriteProblem(w, 404, "not_found", "Unknown device path")
			return
		}
		tenant := TenantFrom(r.Context())
		if _, err := devices.Get(tenant, ep); err != nil {
			if errors.Is(err, store.ErrDeviceNotFound) {
				WriteProblem(w, 404, "not_found", "Device not found")
			} else {
				WriteProblem(w, 500, "internal_error", "Cannot get device")
			}
			return
		}
		links, registry, err := source.ReadGateway(r.Context(), tenant, ep)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot get gateway data")
			return
		}
		items := []endDeviceView{}
		isGateway := false
		for _, l := range links {
			p, e := lwm2m.ParsePath(l.URI)
			if e == nil && p.Len() <= 2 && p.Object() == gateway.ObjectGateway {
				isGateway = true
				break
			}
		}
		if isGateway {
			objects := prefixedObjects(links)
			for _, d := range registry {
				item := endDeviceView{Prefix: d.Prefix, ID: d.ID, Instance: d.Instance, Objects: []endDeviceObject{}}
				for _, o := range objects[d.Prefix] {
					item.Objects = append(item.Objects, endDeviceObject{ID: o.ID, Version: o.Version, Instances: o.Instances})
				}
				items = append(items, item)
			}
		}
		deviceJSON(w, http.StatusOK, map[string]any{"items": items})
	}))
}
