// Command lwm2md runs the LwM2M server: CoAP over UDP (NoSec) and DTLS
// (PSK/X.509 per the security store, Connection ID on), optionally CoAP
// over TCP, and the Leshan-compatible REST API (package leshanapi), so the
// Zephyr interop harness can drive it as it drives the Leshan demo server.
//
// It also runs a Bootstrap-Server (package bootstrap) with Leshan's
// bootstrap demo REST (zephyr-interop.md §4.2) on its own port, as
// leshan-bsserver-demo does.
//
//	go run ./cmd/lwm2md -coap :5683 -coaps :5684 -http :8080 \
//		-bs-coap :5783 -bs-coaps :5784 -bs-http :8081
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/internal/obs"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/fiumaralabs/lwm2m/webhooks"
)

func newDaemon(cfg server.Config, api *leshanapi.API, keys apiv1.Keys, stores ...store.Devices) (*server.Server, http.Handler, *operations.Executor, *webhooks.Dispatcher) {
	return newDaemonWithTimeSeries(cfg, api, keys, true, stores...)
}

func newDaemonWithTimeSeries(cfg server.Config, api *leshanapi.API, keys apiv1.Keys, enabled bool, stores ...store.Devices) (*server.Server, http.Handler, *operations.Executor, *webhooks.Dispatcher) {
	var devices store.Devices = &memory.Devices{}
	if len(stores) > 0 {
		devices = stores[0]
	}
	var auditStore apiv1.AuditStore = &apiv1.MemoryAuditStore{}
	var operationStore operations.Operations = memory.NewOperations()
	var events store.Events = &memory.Events{}
	var observationStore apiv1.ObservationDeliveryStore = &memory.Observations{}
	var firmwareStore apiv1.FirmwareStore
	if durable, ok := devices.(*sqlite.Devices); ok {
		keys = keys.WithStore(durable.APIKeys())
		auditStore = durable.Audit()
		operationStore = durable.Operations()
		events = durable.Events()
		observationStore = durable.Observations()
		firmwareStore = durable.Firmware()
	}
	var executor *operations.Executor
	var observations *apiv1.ObservationDelivery
	state := &memory.State{}
	hooks := &memory.Webhooks{}
	onQueued := cfg.OnQueued
	cfg.OnQueued = func(r *server.Registration) {
		executor.HandleQueued(r)
		if onQueued != nil {
			onQueued(r)
		}
	}
	onEvent := cfg.OnEvent
	deviceEvents := apiv1.DeviceEvents(devices, "default")
	cfg.OnEvent = func(e server.Event) {
		deviceEvents(e)
		observations.HandleEvent(e)
		registrationEvent(events, e)
		executor.HandleEvent(e)
		if onEvent != nil {
			onEvent(e)
		}
	}
	srv := server.New(cfg)
	observations = apiv1.NewObservationDelivery(srv, observationStore, state, events, "default")
	executor = operations.NewExecutor(srv, operationStore, operations.ExecutorOptions{State: state})
	api.Attach(srv)
	var series apiv1.TimeSeriesStore
	if enabled {
		series = apiv1.NewMemoryTimeSeriesStore()
		if durable, ok := devices.(*sqlite.Devices); ok {
			series = durable.TimeSeries()
		}
	}
	native := apiv1.New(apiv1.Options{Firmware: firmwareStore, TimeSeries: series, AuditOptions: apiv1.AuditOptions{Audit: auditStore}, Observations: observations, State: state, EventOptions: apiv1.EventOptions{Events: events}, Webhooks: hooks, Operations: operationStore, Executor: executor, Devices: devices, Server: srv, ServerTenant: "default", Keys: keys, Version: "dev",
		LwM2MVersions: []string{"1.0", "1.1", "1.2"}, Capabilities: map[string]bool{},
		Endpoints: map[string]map[string]string{}})
	return srv, native, executor, webhooks.New("default", events, hooks, devices, webhooks.Options{})
}

// Expiration is a Deregistered event with ReasonExpired, not a distinct type.
func registrationEvent(events store.Events, e server.Event) {
	view := func(r *server.Registration, awake bool) map[string]any {
		if r == nil {
			return nil
		}
		return apiv1.RegistrationView(r, awake)
	}
	var r *server.Registration
	var kind string
	var data map[string]any
	switch e := e.(type) {
	case server.Registered:
		r, kind = e.Registration, "created"
		// Register and Update mark the session awake before emitting the event.
		data = map[string]any{"registration": view(r, true), "replaced": view(e.Replaced, false)}
	case server.Updated:
		r, kind = e.Registration, "updated"
		current, previous := view(r, true), view(&e.Previous, true)
		changed := []string{}
		for _, key := range []string{"lwm2mVersion", "lifetimeSeconds", "binding", "queueMode", "sms", "profileIds", "rootPath", "objects", "contentFormats", "identity", "transport", "address"} {
			if !reflect.DeepEqual(current[key], previous[key]) {
				changed = append(changed, key)
			}
		}
		data = map[string]any{"registration": current, "changed": changed}
	case server.Deregistered:
		r, kind = e.Registration, "deleted"
		data = map[string]any{"registration": view(r, false), "reason": e.Reason.String()}
	default:
		return
	}
	if body, err := json.Marshal(data); err == nil {
		if _, err := events.Append("default", "lwm2m.registration."+kind, r.Endpoint, body); err != nil {
			log.Printf("append registration event: %v", err)
		}
	}
}

func main() {
	coapAddr := flag.String("coap", ":5683", "CoAP/UDP (NoSec) address; empty disables")
	coaps := flag.String("coaps", ":5684", "CoAP/DTLS address; empty disables")
	tcp := flag.String("tcp", "", "CoAP/TCP address (RFC 8323); empty disables")
	httpAddr := flag.String("http", ":8080", "Leshan-compatible REST address; empty disables")
	metrics := flag.Bool("metrics", true, "expose Prometheus metrics on /metrics")
	timeSeries := flag.Bool("timeseries", true, "enable time series (persistent with SQLite)")
	storeFlag := flag.String("store", "memory", "device metadata and operations store: memory or sqlite:<path>")
	prefix := flag.String("prefix", "/api", "REST mount point")
	keyFile := flag.String("api-keys", "", "native /v1 API key file; empty denies all requests")
	bsCoap := flag.String("bs-coap", ":5783", "Bootstrap-Server CoAP/UDP address; empty disables")
	bsCoaps := flag.String("bs-coaps", ":5784", "Bootstrap-Server CoAP/DTLS address; empty disables")
	bsHTTP := flag.String("bs-http", ":8081", "Leshan-compatible bootstrap REST address; empty disables")
	cidLen := flag.Int("cid", 6, "DTLS Connection ID length (Leshan demo default 6); 0 disables")
	fwDir := flag.String("fw-dir", "", "serve the files in this directory for firmware pull (/5/0/1 = coap[s]://host:port/<name>)")
	fwCoap := flag.String("fw-coap", ":5693", "firmware CoAP/UDP address, with -fw-dir")
	fwCoaps := flag.String("fw-coaps", ":5694", "firmware CoAP/DTLS address (server PSKs), with -fw-dir")
	verbose := flag.Bool("v", false, "log registration, notification and bootstrap events")
	flag.Parse()
	var devices store.Devices = &memory.Devices{}
	if strings.HasPrefix(*storeFlag, "sqlite:") && strings.TrimPrefix(*storeFlag, "sqlite:") != "" {
		durable, err := sqlite.Open(strings.TrimPrefix(*storeFlag, "sqlite:"))
		if err != nil {
			log.Fatal(err)
		}
		defer durable.Close()
		if err := recoverObservations(strings.TrimPrefix(*storeFlag, "sqlite:")); err != nil {
			log.Fatal(err)
		}
		devices = durable
	} else if *storeFlag != "memory" {
		log.Fatal("invalid -store: expected memory or sqlite:<path>")
	}
	var keys apiv1.Keys
	if *keyFile != "" {
		f, err := os.Open(*keyFile)
		if err != nil {
			log.Fatal("cannot open API key file")
		}
		keys, err = apiv1.LoadKeys(f)
		_ = f.Close()
		if err != nil {
			log.Fatal(err)
		}
	}

	models := server.NewModels(model.Default())
	api := leshanapi.New(leshanapi.Options{Prefix: *prefix, Schema: models.Schema})
	// Schema only, no Validator: like Leshan, writes go to the device and
	// its answer (e.g. 4.05 on a read-only resource, int-256) is reported.
	onEvent := api.OnEvent
	if *verbose {
		onEvent = func(e server.Event) {
			logEvent(e)
			api.OnEvent(e)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var cb *coap.Binding
	var securityStore server.SecurityStore = server.NewMemorySecurityStore()
	if durable, ok := devices.(*sqlite.Devices); ok {
		securityStore = durable.Security()
	}
	security := coap.NewRevokingSecurityStore(securityStore, func(id string) { cb.RevokePSKIdentity(id) })
	srv, native, executor, dispatcher := newDaemonWithTimeSeries(server.Config{OnEvent: onEvent, Schema: models.Schema, Security: security}, api, keys, *timeSeries, devices)
	cb = coap.New(srv)
	if err := dispatcher.Start(ctx); err != nil {
		log.Fatal(err)
	}
	if err := executor.Start(ctx); err != nil {
		log.Fatal(err)
	}

	listen := func(name, addr string, f func(string) (net.Addr, error)) {
		if addr == "" {
			return
		}
		a, err := f(addr)
		if err != nil {
			log.Fatalf("%s %s: %v", name, addr, err)
		}
		log.Printf("%s listening on %v", name, a)
	}
	listen("coap", *coapAddr, func(a string) (net.Addr, error) { return cb.ListenUDP(a) })
	listen("coaps", *coaps, func(a string) (net.Addr, error) {
		// DTLSConfig adds session resumption (SEC-11) to the PSK lookup:
		// devices with session caching resume instead of a full handshake.
		dc, err := cb.DTLSConfig(coap.CertificateModes{})
		if err != nil {
			return nil, err
		}
		dc.CIDLength, dc.DisableCID = *cidLen, *cidLen == 0
		return cb.ListenDTLS(a, dc)
	})
	listen("coap+tcp", *tcp, func(a string) (net.Addr, error) { return cb.ListenTCP(a) })

	if *fwDir != "" {
		fs := fota.NewFileServer()
		defer fs.Close()
		entries, err := os.ReadDir(*fwDir)
		if err != nil {
			log.Fatal(err)
		}
		for _, e := range entries {
			if b, err := os.ReadFile(filepath.Join(*fwDir, e.Name())); err == nil && e.Type().IsRegular() {
				fs.Add(e.Name(), b)
				log.Printf("firmware /%s (%d bytes)", e.Name(), len(b))
			}
		}
		listen("fw coap", *fwCoap, fs.ListenUDP)
		listen("fw coaps", *fwCoaps, func(a string) (net.Addr, error) {
			dc, err := cb.DTLSConfig(coap.CertificateModes{})
			if err != nil {
				return nil, err
			}
			return fs.ListenDTLS(a, dc.Options...)
		})
	}

	configs := bootstrap.NewMemoryConfigStore()
	bs := bootstrap.New(bootstrap.Config{Configs: configs, OnSession: func(r bootstrap.Result) {
		if *verbose || r.Err != nil {
			log.Printf("bootstrap %s: steps=%v err=%v", r.Endpoint, r.Steps, r.Err)
		}
	}})
	listen("bs coap", *bsCoap, func(a string) (net.Addr, error) { return bs.ListenUDP(a) })
	listen("bs coaps", *bsCoaps, func(a string) (net.Addr, error) {
		return bs.ListenDTLS(a, bootstrap.DTLSConfig{CIDLength: *cidLen, DisableCID: *cidLen == 0})
	})

	probes := obs.New(nil, func() int { return len(srv.Store().All()) }, *metrics)
	var servers []*http.Server
	serve := func(name, addr string, h http.Handler) {
		if addr == "" {
			return
		}
		// Request contexts derive from ctx, so open SSE streams end on a signal.
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("%s %s: %v", name, addr, err)
		}
		hs := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second,
			BaseContext: func(net.Listener) context.Context { return ctx }}
		servers = append(servers, hs)
		go func() {
			log.Printf("%s listening on %s%s", name, addr, *prefix)
			if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("%s %s: %v", name, addr, err)
			}
		}()
	}
	serve("rest", *httpAddr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz", "/metrics":
			probes.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/") {
			native.ServeHTTP(w, r)
			return
		}
		api.ServeHTTP(w, r)
	}))
	serve("bs rest", *bsHTTP, leshanapi.NewBootstrap(*prefix, configs, bs.Security()))
	probes.SetReady(true)

	<-ctx.Done()
	probes.SetReady(false)
	log.Print("shutting down")
	for _, hs := range servers {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := hs.Shutdown(sctx); err != nil {
			_ = hs.Close()
		}
		cancel()
	}
	if err := dispatcher.Close(); err != nil {
		log.Print(err)
	}
	if err := executor.Close(); err != nil {
		log.Print(err)
	}
	if err := bs.Close(); err != nil {
		log.Print(err)
	}
	if err := cb.Close(); err != nil {
		log.Print(err)
	}
	if err := srv.Close(); err != nil {
		log.Print(err)
	}
}

func logEvent(e server.Event) {
	switch e := e.(type) {
	case server.Registered:
		log.Printf("registered %s id=%s addr=%v lifetime=%v binding=%s queue=%v", e.Registration.Endpoint,
			e.Registration.ID, e.Registration.RemoteAddr(), e.Registration.Lifetime, e.Registration.Binding, e.Registration.QueueMode)
	case server.Updated:
		log.Printf("updated %s addr=%v lifetime=%v", e.Registration.Endpoint, e.Registration.RemoteAddr(), e.Registration.Lifetime)
	case server.Deregistered:
		log.Printf("deregistered %s: %s", e.Registration.Endpoint, e.Reason)
	case server.Notification:
		log.Printf("notify %s %v", e.Registration.Endpoint, e.Observation.Paths)
	case server.SendReceived:
		log.Printf("send %s %d nodes", e.Registration.Endpoint, len(e.Nodes))
	case server.Awake:
		log.Printf("awake %s", e.Registration.Endpoint)
	}
}
