package apiv1

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/store"
)

type deviceModelObject struct {
	ID         uint16           `json:"id"`
	Version    string           `json:"version"`
	Instances  []uint16         `json:"instances"`
	Definition *modelDefinition `json:"definition"`
}

type modelDefinition struct {
	ID           uint16          `json:"id"`
	Version      string          `json:"version"`
	Name         string          `json:"name"`
	URN          string          `json:"urn"`
	LwM2MVersion string          `json:"lwm2mVersion"`
	Multiple     bool            `json:"multiple"`
	Mandatory    bool            `json:"mandatory"`
	Description  string          `json:"description"`
	Resources    []modelResource `json:"resources"`
}

type modelResource struct {
	ID          uint16 `json:"id"`
	Name        string `json:"name"`
	Operations  string `json:"operations"`
	Type        string `json:"type"`
	Multiple    bool   `json:"multiple"`
	Mandatory   bool   `json:"mandatory"`
	Units       string `json:"units"`
	Range       string `json:"range"`
	Description string `json:"description"`
}

// modelHandler returns 404 not_registered for absent or expired sessions,
// including tenants not served by the native server; it never exposes their models.
func (o Options) modelHandler() http.Handler {
	registry := model.Default()
	serve := RequireScope("devices:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/model")
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
		if o.Server == nil || tenant != o.ServerTenant {
			WriteProblem(w, 404, "not_registered", "Device is not registered")
			return
		}
		reg, ok := o.Server.Store().ByEndpoint(ep)
		if !ok || reg.Expired(time.Now()) {
			WriteProblem(w, 404, "not_registered", "Device is not registered")
			return
		}
		objects := make([]deviceModelObject, 0, len(reg.Objects))
		for _, obj := range reg.Objects {
			entry := deviceModelObject{ID: obj.ID, Version: obj.Version, Instances: obj.Instances}
			version, err := model.ResolveVersion(reg.Version, obj.ID, obj.Version)
			if entry.Version == "" {
				entry.Version = version.String()
			}
			if entry.Instances == nil {
				entry.Instances = []uint16{}
			}
			if err == nil {
				if def, ok := registry.Get(obj.ID, version); ok {
					entry.Definition = definitionView(def)
				}
			}
			objects = append(objects, entry)
		}
		deviceJSON(w, http.StatusOK, struct {
			Objects []deviceModelObject `json:"objects"`
		}{objects})
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET")
			return
		}
		serve.ServeHTTP(w, r)
	})
}

func definitionView(def *model.Object) *modelDefinition {
	resources := make([]modelResource, 0, len(def.Resources))
	for _, r := range def.Resources {
		typ := r.Type.String()
		if r.Executable() {
			typ = "none"
		}
		resources = append(resources, modelResource{r.ID, r.Name, r.Operations.String(), typ, r.Multiple, r.Mandatory, r.Units, r.Range, r.Description})
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].ID < resources[j].ID })
	return &modelDefinition{def.ID, def.Version.String(), def.Name, def.URN, def.LwM2MVersion, def.Multiple, def.Mandatory, def.Description, resources}
}
