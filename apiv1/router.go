package apiv1

import (
	"errors"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"net/http"
	"net/url"
	"strings"
)

// Options configures the native API and its server information.
type Options struct {
	limits               *tenantLimiter
	TrustAnchors         TrustAnchorStore         `json:"-"`
	BootstrapCredentials BootstrapCredentialStore `json:"-"`
	BootstrapSessions    BootstrapSessionStore    `json:"-"`
	Models               ModelStore               `json:"-"`
	Jobs                 JobStore                 `json:"-"`
	// JobRunner, when provided, shares this handler's operation admission.
	// Configure it before starting concurrent ticks.
	JobRunner        *JobRunner           `json:"-"`
	Firmware         FirmwareStore        `json:"-"`
	FirmwareUpdates  FirmwareUpdateStore  `json:"-"`
	Gateway          GatewayReader        `json:"-"`
	DeviceProfiles   DeviceProfileStore   `json:"-"`
	BootstrapConfigs BootstrapConfigStore `json:"-"`
	// When supplying Executor, also pass this store in its ExecutorOptions.State.
	State    store.State    `json:"-"`
	Settings store.Settings `json:"-"`
	EventOptions
	AuditOptions
	Webhooks     store.Webhooks     `json:"-"`
	Observations store.Observations `json:"-"`
	TimeSeries   TimeSeriesStore    `json:"-"`
	// When Executor is supplied, its store is authoritative and Operations is ignored.
	Operations store.Operations     `json:"-"`
	Executor   *operations.Executor `json:"-"`
	Devices    store.Devices        `json:"-"`
	Server     *server.Server       `json:"-"`
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
	o.limits = &tenantLimiter{tenants: newTenantStore()}
	if o.TrustAnchors == nil {
		o.TrustAnchors = NewMemoryTrustAnchorStore(0)
	}
	if o.BootstrapCredentials == nil {
		o.BootstrapCredentials = NewMemoryBootstrapCredentialStore()
	}
	if o.BootstrapSessions == nil {
		o.BootstrapSessions = NewMemoryBootstrapSessionStore()
	}
	if o.BootstrapConfigs == nil {
		o.BootstrapConfigs = NewMemoryBootstrapConfigStore()
	}
	if o.DeviceProfiles == nil {
		o.DeviceProfiles = NewMemoryDeviceProfileStore()
	}
	if o.State == nil {
		o.State = &memory.State{}
	}
	if o.Observations == nil {
		o.Observations = &memory.Observations{}
	}
	if delivery, ok := o.Observations.(*ObservationDelivery); ok {
		delivery.mu.Lock()
		delivery.series = o.TimeSeries
		delivery.mu.Unlock()
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

	if o.Models == nil {
		o.Models = &memoryModelStore{}
	}
	if o.Jobs == nil {
		o.Jobs = NewMemoryJobStore()
	}
	if o.Firmware == nil {
		o.Firmware = NewMemoryFirmwareStore()
	}
	if o.FirmwareUpdates == nil {
		o.FirmwareUpdates = NewMemoryFirmwareUpdateStore()
	}

	events := o.eventHandler()
	if o.Executor != nil {
		o.Operations = o.Executor.Store()
	} else if o.Operations == nil {
		o.Operations = memory.NewOperations()
	}
	if o.Executor == nil && o.Server != nil {
		o.Executor = operations.NewExecutor(o.Server, o.Operations, operations.ExecutorOptions{State: o.State})
	}
	if o.JobRunner != nil && o.Executor != nil {
		o.JobRunner.Jobs = o.Jobs
		o.JobRunner.Devices = o.Devices
		o.JobRunner.Executor = o.Executor
		o.JobRunner.Operations = o.Operations
		o.JobRunner.ServerTenant = o.ServerTenant
		o.JobRunner.submit = o.submitOperation
	}
	ops := o.operationHandler()
	models := o.modelHandler()
	jobs := NewJobsHandler(o.Jobs)
	updates := NewFirmwareUpdatesHandler(o.FirmwareUpdates, o.Firmware)
	anchors := NewTrustAnchorHandler(o.TrustAnchors)
	bootstrapCredentials := NewBootstrapCredentialsHandler(o.Devices, o.BootstrapCredentials)
	if o.Audit == nil {
		o.Audit = &MemoryAuditStore{}
	}
	return Authenticate(o.Keys, recordAudit(o.Audit, tenantMiddleware(o, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/trust-anchors" || strings.HasPrefix(r.URL.Path, "/v1/trust-anchors/") {
			anchors.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/device-profiles" || strings.HasPrefix(r.URL.Path, "/v1/device-profiles/") {
			scope := "devices:read"
			if r.Method != http.MethodGet {
				scope = "devices:write"
			}
			RequireScope(scope)(http.HandlerFunc(o.deviceProfiles)).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/jobs" || strings.HasPrefix(r.URL.Path, "/v1/jobs/") {
			jobs.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/firmware-updates/") || strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(r.URL.EscapedPath(), "/firmware-updates") && r.URL.EscapedPath() != "/v1/devices/firmware-updates" {
			if strings.HasPrefix(r.URL.Path, "/v1/devices/") && (r.Method == http.MethodGet || r.Method == http.MethodPost) {
				scope := "devices:read"
				if r.Method == http.MethodPost {
					scope = "firmware:write"
				}
				RequireScope(scope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/firmware-updates")
					ep, err := url.PathUnescape(raw)
					if err != nil || ep == "" || strings.Contains(raw, "/") {
						WriteProblem(w, 404, "not_found", "Unknown device path")
						return
					}
					if _, err = o.Devices.Get(TenantFrom(r.Context()), ep); err != nil {
						if errors.Is(err, store.ErrDeviceNotFound) {
							WriteProblem(w, 404, "not_found", "Device not found")
						} else {
							WriteProblem(w, 500, "internal_error", "Cannot get device")
						}
						return
					}
					updates.ServeHTTP(w, r)
				})).ServeHTTP(w, r)
			} else {
				updates.ServeHTTP(w, r)
			}
			return
		}
		if r.URL.Path == "/v1/firmware" || strings.HasPrefix(r.URL.Path, "/v1/firmware/") {
			serveFirmware(o.Firmware, w, r)
			return
		}
		if r.URL.Path == "/v1/audit-log" {
			RequireScope("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveAudit(o.Audit, w, r) })).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/models" || strings.HasPrefix(r.URL.Path, "/v1/models/") {
			serveModels(o.Models, w, r)
			return
		}
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
		if strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(r.URL.EscapedPath(), "/timeseries") && r.URL.EscapedPath() != "/v1/devices/timeseries" {
			RequireScope("devices:read")(http.HandlerFunc(o.timeSeries)).ServeHTTP(w, r)
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
		if strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(r.URL.EscapedPath(), "/end-devices") && r.URL.EscapedPath() != "/v1/devices/end-devices" && o.Gateway != nil {
			endDevicesHandler(o.Devices, o.Gateway).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1/devices" || strings.HasPrefix(r.URL.Path, "/v1/devices/") {
			if strings.HasSuffix(r.URL.EscapedPath(), "/bootstrap/credentials") {
				bootstrapCredentials.ServeHTTP(w, r)
				return
			}
			if strings.HasSuffix(r.URL.EscapedPath(), "/bootstrap/sessions") {
				RequireScope("devices:read")(http.HandlerFunc(o.serveBootstrapSessions)).ServeHTTP(w, r)
				return
			}
			if strings.HasSuffix(r.URL.EscapedPath(), "/credentials") && r.URL.EscapedPath() != "/v1/devices/credentials" {
				o.credentials(w, r)
				return
			}
			if strings.HasSuffix(r.URL.EscapedPath(), "/bootstrap") && r.URL.EscapedPath() != "/v1/devices/bootstrap" {
				o.bootstrap(w, r)
				return
			}
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
	}))))
}
