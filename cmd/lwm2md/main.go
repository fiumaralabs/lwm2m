// Command lwm2md runs the LwM2M server: CoAP over UDP (NoSec) and DTLS
// (PSK/RPK/X.509 per the security store, Connection ID on), optionally CoAP
// over TCP, and the Leshan-compatible REST API (package compat), so the
// Zephyr interop harness can drive it as it drives the Leshan demo server.
//
//	go run ./cmd/lwm2md -coap :5683 -coaps :5684 -http :8080
//
// The bootstrap server REST (Leshan's :8081, zephyr-interop.md §4.2) is not
// served yet: when the bootstrap package lands, run it here with its own
// compat.API (security store) and pass its handler as
// compat.Options.Bootstrap.
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
	"syscall"
	"time"

	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/compat"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
)

func main() {
	coap := flag.String("coap", ":5683", "CoAP/UDP (NoSec) address; empty disables")
	coaps := flag.String("coaps", ":5684", "CoAP/DTLS address; empty disables")
	tcp := flag.String("tcp", "", "CoAP/TCP address (RFC 8323); empty disables")
	httpAddr := flag.String("http", ":8080", "Leshan-compatible REST address; empty disables")
	prefix := flag.String("prefix", "/api", "REST mount point")
	cidLen := flag.Int("cid", 6, "DTLS Connection ID length (Leshan demo default 6); 0 disables")
	flag.Parse()

	models := server.NewModels(model.Default())
	api := compat.New(compat.Options{Prefix: *prefix, Schema: models.Schema})
	// Schema only, no Validator: like Leshan, writes go to the device and
	// its answer (e.g. 4.05 on a read-only resource, int-256) is reported.
	srv := server.New(server.Config{OnEvent: api.OnEvent, Schema: models.Schema})
	api.Attach(srv)

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
	listen("coap", *coap, func(a string) (net.Addr, error) { return srv.ListenUDP(a) })
	listen("coaps", *coaps, func(a string) (net.Addr, error) {
		return srv.ListenDTLS(a, server.DTLSConfig{CIDLength: *cidLen, DisableCID: *cidLen == 0})
	})
	listen("coap+tcp", *tcp, func(a string) (net.Addr, error) { return srv.ListenTCP(a) })

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var hs *http.Server
	if *httpAddr != "" {
		// Request contexts derive from ctx, so open SSE streams end on a signal.
		hs = &http.Server{Addr: *httpAddr, Handler: api, ReadHeaderTimeout: 10 * time.Second,
			BaseContext: func(net.Listener) context.Context { return ctx }}
		go func() {
			log.Printf("rest listening on %s%s", *httpAddr, *prefix)
			if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("rest %s: %v", *httpAddr, err)
			}
		}()
	}

	<-ctx.Done()
	log.Print("shutting down")
	if hs != nil {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := hs.Shutdown(sctx); err != nil {
			_ = hs.Close()
		}
		cancel()
	}
	if err := srv.Close(); err != nil {
		log.Print(err)
	}
}
