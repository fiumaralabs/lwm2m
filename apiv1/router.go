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
	ESTConfigs           ESTConfigStore           `json:"-"`
	ESTIssuerRotator     ESTIssuerRotator         `json:"-"`
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
	WebhookDeliveries store.WebhookDeliveries `json:"-"`
	Webhooks          store.Webhooks          `json:"-"`
	Observations      store.Observations      `json:"-"`
	TimeSeries        TimeSeriesStore         `json:"-"`
	// When Executor is supplied, its store is authoritative and Operations is ignored.
	Operations    store.Operations        `json:"-"`
	Executor      *operations.Executor    `json:"-"`
	Devices       store.Devices           `json:"-"`
	DeviceCreates store.DeviceCreateStore `json:"-"`
	Server        *server.Server          `json:"-"`
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
	if o.ESTConfigs == nil {
		o.ESTConfigs = NewMemoryESTConfigStore()
	}
	if o.ESTIssuerRotator == nil {
		_, memoryConfigs := o.ESTConfigs.(*memoryESTConfigStore)
		_, memoryAnchors := o.TrustAnchors.(*memoryTrustAnchorStore)
		if memoryConfigs && memoryAnchors {
			o.ESTIssuerRotator = NewMemoryESTIssuerRotator(o.ESTConfigs, o.TrustAnchors)
		}
	}
	if o.BootstrapCredentials == nil {
		o.BootstrapCredentials = NewMemoryBootstrapCredentialStore()
	}
	if o.BootstrapSessions == nil {
		o.BootstrapSessions = NewMemoryBootstrapSessionStore()
	}
	if o.BootstrapConfigs == nil {
		if shared, ok := o.Devices.(store.DeviceBootstrapCreateStore); ok {
			o.BootstrapConfigs = sharedBootstrapConfigs{shared}
		} else {
			o.BootstrapConfigs = NewMemoryBootstrapConfigStore()
		}
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
	if o.WebhookDeliveries == nil {
		o.Webhooks, o.WebhookDeliveries = memory.NewWebhookDeliveryStores(o.Webhooks)
	}
	if o.Devices == nil {
		o.Devices = &memory.Devices{}
	}
	if o.DeviceCreates == nil {
		o.DeviceCreates, _ = o.Devices.(store.DeviceCreateStore)
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
		o.JobRunner.DeviceProfiles = o.DeviceProfiles
		o.JobRunner.Observations = o.Observations
		o.JobRunner.Executor = o.Executor
		o.JobRunner.Operations = o.Operations
		o.JobRunner.ServerTenant = o.ServerTenant
		o.JobRunner.submit = o.submitOperation
	}
	ops := o.operationHandler()
	models := o.modelHandler()
	jobs := newJobsHandler(o.Jobs, o.Executor, o.JobRunner)
	updates := NewFirmwareUpdatesHandler(o.FirmwareUpdates, o.Firmware)
	anchors := NewTrustAnchorHandler(o.TrustAnchors)
	est := NewESTConfigHandler(o.ESTConfigs, o.TrustAnchors, o.ESTIssuerRotator)
	bootstrapCredentials := NewBootstrapCredentialsHandler(o.Devices, o.BootstrapCredentials)
	if o.Audit == nil {
		o.Audit = &MemoryAuditStore{}
	}
	secured := Authenticate(o.Keys, deviceCreateAudit(o.Audit, tenantMiddleware(o, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Route on decoded segments without turning escaped slashes into separators.
		// Downstream handlers still receive the original request and endpoint ID.
		original := r
		parts := strings.Split(r.URL.EscapedPath(), "/")
		for i, part := range parts {
			decoded, err := url.PathUnescape(part)
			if err != nil {
				WriteProblem(w, http.StatusNotFound, "not_found", "Unknown API path")
				return
			}
			parts[i] = strings.ReplaceAll(decoded, "/", "%2F")
		}
		r = r.Clone(r.Context())
		u := *r.URL
		u.Path = strings.Join(parts, "/")
		u.RawPath = ""
		r.URL = &u
		if strings.HasPrefix(r.URL.Path, "/v1/device-creates/") {
			o.deviceCreates(w, original)
			return
		}
		if r.URL.Path == "/v1/est" {
			est.ServeHTTP(w, original)
			return
		}
		if r.URL.Path == "/v1/trust-anchors" || strings.HasPrefix(r.URL.Path, "/v1/trust-anchors/") {
			anchors.ServeHTTP(w, original)
			return
		}
		if r.URL.Path == "/v1/device-profiles" || strings.HasPrefix(r.URL.Path, "/v1/device-profiles/") {
			scope := "devices:read"
			if r.Method != http.MethodGet {
				scope = "devices:write"
			}
			RequireScope(scope)(http.HandlerFunc(o.deviceProfiles)).ServeHTTP(w, original)
			return
		}
		if r.URL.Path == "/v1/jobs" || strings.HasPrefix(r.URL.Path, "/v1/jobs/") {
			jobs.ServeHTTP(w, original)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/firmware-updates/") || strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(original.URL.EscapedPath(), "/firmware-updates") && original.URL.EscapedPath() != "/v1/devices/firmware-updates" {
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
				})).ServeHTTP(w, original)
			} else {
				updates.ServeHTTP(w, original)
			}
			return
		}
		if r.URL.Path == "/v1/firmware" || strings.HasPrefix(r.URL.Path, "/v1/firmware/") {
			serveFirmware(o.Firmware, w, original)
			return
		}
		if r.URL.Path == "/v1/audit-log" {
			RequireScope("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveAudit(o.Audit, w, r) })).ServeHTTP(w, original)
			return
		}
		if r.URL.Path == "/v1/models" || strings.HasPrefix(r.URL.Path, "/v1/models/") {
			serveModels(o.Models, w, original)
			return
		}
		if r.URL.Path == "/v1/settings" {
			o.settings(w, original)
			return
		}
		if r.URL.Path == "/v1/webhooks" || strings.HasPrefix(r.URL.Path, "/v1/webhooks/") {
			RequireScope("admin")(http.HandlerFunc(o.webhooks)).ServeHTTP(w, original)
			return
		}
		if r.URL.Path == "/v1/operations" || strings.HasPrefix(r.URL.Path, "/v1/operations/") || strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(original.URL.EscapedPath(), "/operations") && original.URL.EscapedPath() != "/v1/devices/operations" {
			ops.ServeHTTP(w, original)
			return
		}
		if r.URL.Path == "/v1/events" {
			events.ServeHTTP(w, original)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(original.URL.EscapedPath(), "/timeseries") && original.URL.EscapedPath() != "/v1/devices/timeseries" {
			RequireScope("devices:read")(http.HandlerFunc(o.timeSeries)).ServeHTTP(w, original)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/observations/") || strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(original.URL.EscapedPath(), "/observations") && original.URL.EscapedPath() != "/v1/devices/observations" {
			o.observations(w, original)
			return
		}
		if r.URL.Path == "/v1/api-keys" || strings.HasPrefix(r.URL.Path, "/v1/api-keys/") {
			RequireScope("admin")(http.HandlerFunc(serveAPIKeys)).ServeHTTP(w, original)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/devices/") && strings.HasSuffix(original.URL.EscapedPath(), "/end-devices") && original.URL.EscapedPath() != "/v1/devices/end-devices" && o.Gateway != nil {
			endDevicesHandler(o.Devices, o.Gateway).ServeHTTP(w, original)
			return
		}
		if r.URL.Path == "/v1/devices" || strings.HasPrefix(r.URL.Path, "/v1/devices/") {
			if strings.HasSuffix(original.URL.EscapedPath(), "/bootstrap/credentials") {
				bootstrapCredentials.ServeHTTP(w, original)
				return
			}
			if strings.HasSuffix(original.URL.EscapedPath(), "/bootstrap/sessions") {
				RequireScope("devices:read")(http.HandlerFunc(o.serveBootstrapSessions)).ServeHTTP(w, original)
				return
			}
			if strings.HasSuffix(original.URL.EscapedPath(), "/credentials") && original.URL.EscapedPath() != "/v1/devices/credentials" {
				o.credentials(w, original)
				return
			}
			if strings.HasSuffix(original.URL.EscapedPath(), "/bootstrap") && original.URL.EscapedPath() != "/v1/devices/bootstrap" {
				o.bootstrap(w, original)
				return
			}
			if strings.HasSuffix(original.URL.EscapedPath(), "/state") && original.URL.EscapedPath() != "/v1/devices/state" {
				o.state(w, original)
				return
			}
			if strings.HasSuffix(original.URL.EscapedPath(), "/model") && original.URL.EscapedPath() != "/v1/devices/model" {
				models.ServeHTTP(w, original)
				return
			}
			if strings.HasSuffix(original.URL.EscapedPath(), "/registration") && original.URL.EscapedPath() != "/v1/devices/registration" {
				o.registration(w, original)
				return
			}
			if strings.HasSuffix(original.URL.EscapedPath(), "/events") && original.URL.EscapedPath() != "/v1/devices/events" {
				o.deviceEvents(w, original)
				return
			}
			o.devices(w, original)
			return
		}
		if r.URL.Path == "/v1/info" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				WriteProblem(w, 405, "method_not_allowed", "Expected GET")
				return
			}
			o.info(w, original)
			return
		}
		WriteProblem(w, 404, "not_found", "Unknown API path")
	}))))
	return openAPIGate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/openapi.json" {
			serveOpenAPI(w, r)
			return
		}
		secured.ServeHTTP(w, r)
	}))
}
