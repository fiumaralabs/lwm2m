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
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fiumaralabs/lwm2m/bootstrap"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func main() {
	coapAddr := flag.String("coap", ":5683", "CoAP/UDP (NoSec) address; empty disables")
	coaps := flag.String("coaps", ":5684", "CoAP/DTLS address; empty disables")
	tcp := flag.String("tcp", "", "CoAP/TCP address (RFC 8323); empty disables")
	httpAddr := flag.String("http", ":8080", "Leshan-compatible REST address; empty disables")
	prefix := flag.String("prefix", "/api", "REST mount point")
	bsCoap := flag.String("bs-coap", ":5783", "Bootstrap-Server CoAP/UDP address; empty disables")
	bsCoaps := flag.String("bs-coaps", ":5784", "Bootstrap-Server CoAP/DTLS address; empty disables")
	bsHTTP := flag.String("bs-http", ":8081", "Leshan-compatible bootstrap REST address; empty disables")
	cidLen := flag.Int("cid", 6, "DTLS Connection ID length (Leshan demo default 6); 0 disables")
	fwDir := flag.String("fw-dir", "", "serve the files in this directory for firmware pull (/5/0/1 = coap[s]://host:port/<name>)")
	fwCoap := flag.String("fw-coap", ":5693", "firmware CoAP/UDP address, with -fw-dir")
	fwCoaps := flag.String("fw-coaps", ":5694", "firmware CoAP/DTLS address (server PSKs), with -fw-dir")
	verbose := flag.Bool("v", false, "log registration, notification and bootstrap events")
	flag.Parse()

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
	srv := server.New(server.Config{OnEvent: onEvent, Schema: models.Schema})
	api.Attach(srv)
	cb := coap.New(srv)

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var servers []*http.Server
	serve := func(name, addr string, h http.Handler) {
		if addr == "" {
			return
		}
		// Request contexts derive from ctx, so open SSE streams end on a signal.
		hs := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second,
			BaseContext: func(net.Listener) context.Context { return ctx }}
		servers = append(servers, hs)
		go func() {
			log.Printf("%s listening on %s%s", name, addr, *prefix)
			if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("%s %s: %v", name, addr, err)
			}
		}()
	}
	serve("rest", *httpAddr, api)
	serve("bs rest", *bsHTTP, leshanapi.NewBootstrap(*prefix, configs, bs.Security()))

	<-ctx.Done()
	log.Print("shutting down")
	for _, hs := range servers {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := hs.Shutdown(sctx); err != nil {
			_ = hs.Close()
		}
		cancel()
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
			e.Registration.ID, e.Registration.Addr, e.Registration.Lifetime, e.Registration.Binding, e.Registration.QueueMode)
	case server.Updated:
		log.Printf("updated %s addr=%v lifetime=%v", e.Registration.Endpoint, e.Registration.Addr, e.Registration.Lifetime)
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
