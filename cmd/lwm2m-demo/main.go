// Command lwm2m-demo runs a public, anonymous LwM2M demo server: CoAP over
// UDP (NoSec) and DTLS-PSK with one published demo key, plus a read-only web
// page that lists the registered clients and can read a resource.
//
// It is lwm2md with the dangerous parts left out and limits added for an
// open Internet deployment:
//
//   - no Leshan REST API (it has no auth and can write, execute and delete),
//     no Bootstrap-Server, no firmware server;
//
//   - a cap on registrations, a per-IP cap and register rate limit (NoSec),
//     endpoint names limited to 64 printable characters;
//
//   - lifetimes clamped to -max-lifetime, so abandoned registrations go away;
//
//   - DTLS-PSK: any PSK identity is accepted with the demo key, and the
//     endpoint name must equal the identity (Zephyr/Leshan demo convention).
//     An endpoint seen over PSK cannot then register over NoSec (SEC-07);
//
//   - the web API only lists registrations, reads and observes (never /0 or
//     /21), with a global concurrency limit and a per-IP rate limit; live
//     values are shared Observes with per-IP, global and time limits.
//
//     go run ./cmd/lwm2m-demo -coap :5683 -coaps :5684 -http 127.0.0.1:8090
package main

import (
	"context"
	"embed"
	"encoding/hex"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"unicode"

	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

//go:embed web
var webFS embed.FS

func main() {
	coapAddr := flag.String("coap", ":5683", "CoAP/UDP (NoSec) address; empty disables")
	coaps := flag.String("coaps", ":5684", "CoAP/DTLS-PSK address; empty disables")
	httpAddr := flag.String("http", "127.0.0.1:8090", "web page and read-only API address")
	maxReg := flag.Int("max-registrations", 200, "maximum concurrent registrations")
	maxPerIP := flag.Int("max-per-ip", 10, "maximum concurrent NoSec registrations per source IP")
	regPerMin := flag.Int("registers-per-minute", 30, "NoSec Register requests per source IP per minute")
	maxLifetime := flag.Duration("max-lifetime", time.Hour, "lifetimes above this (and 0 = infinite) are clamped")
	maxPSK := flag.Int("max-psk-identities", 1000, "PSK identities remembered (oldest forgotten first)")
	pskHex := flag.String("psk-key", "000102030405060708090a0b0c0d0e0f", "the public demo PSK (hex)")
	readsPerMin := flag.Int("reads-per-minute", 20, "web reads per client IP per minute")
	trustProxy := flag.Bool("trust-proxy", true, "take the client IP from X-Forwarded-For when the peer is loopback")
	observeMax := flag.Int("observe-max", 32, "web Observe streams open at once (all visitors)")
	observePerIP := flag.Int("observe-per-ip", 2, "web Observe streams open at once per visitor IP")
	observeFor := flag.Duration("observe-for", 5*time.Minute, "a web Observe stream is closed after this long")
	flag.Parse()

	key, err := hex.DecodeString(*pskHex)
	if err != nil || len(key) == 0 {
		log.Fatalf("-psk-key: need hex")
	}

	models := server.NewModels(demoRegistry())
	store := &clampStore{Store: server.NewMemoryStore(), max: *maxLifetime}
	sec := newDemoSecurity(key, *maxPSK)
	lim := &regLimiter{max: *maxReg, perIP: *maxPerIP, rate: *regPerMin, store: store, hits: map[string][]time.Time{}}
	web := newWebAPI(models, webLimits{maxReg: *maxReg, maxLifetime: *maxLifetime, trustProxy: *trustProxy,
		readsPerMin: *readsPerMin, observeMax: *observeMax, observePerIP: *observePerIP, observeFor: *observeFor})
	srv := server.New(server.Config{
		Store:     store,
		Security:  sec,
		Schema:    models.Schema,
		Validator: models,
		Authorize: lim.authorize,
		OnEvent: func(e server.Event) {
			logEvent(e)
			web.onEvent(e)
		},
	})
	web.srv = srv
	cb := coap.New(srv)

	if *coapAddr != "" {
		a, err := cb.ListenUDP(*coapAddr)
		if err != nil {
			log.Fatalf("coap %s: %v", *coapAddr, err)
		}
		log.Printf("coap listening on %v", a)
	}
	if *coaps != "" {
		dc, err := cb.DTLSConfig(coap.CertificateModes{})
		if err != nil {
			log.Fatal(err)
		}
		dc.CIDLength = 6
		a, err := cb.ListenDTLS(*coaps, dc)
		if err != nil {
			log.Fatalf("coaps %s: %v", *coaps, err)
		}
		log.Printf("coaps (PSK) listening on %v", a)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hs := &http.Server{Addr: *httpAddr, Handler: web.routes(), ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		log.Printf("http listening on %s", *httpAddr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http %s: %v", *httpAddr, err)
		}
	}()

	<-ctx.Done()
	log.Print("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = hs.Shutdown(sctx)
	cancel()
	_ = cb.Close()
	_ = srv.Close()
}

// clampStore clamps every stored lifetime to max (0, infinite, included).
// The Registration handed to Add/Update is a fresh copy owned by the server.
type clampStore struct {
	server.Store
	max time.Duration
}

func (s *clampStore) clamp(r *server.Registration) {
	if r.Lifetime == 0 || r.Lifetime > s.max {
		r.Lifetime = s.max
	}
}

func (s *clampStore) Add(r *server.Registration) *server.Registration {
	s.clamp(r)
	return s.Store.Add(r)
}

func (s *clampStore) Update(r *server.Registration) bool {
	s.clamp(r)
	return s.Store.Update(r)
}

// regLimiter is the Authorize hook: endpoint name rules, the global cap,
// and for NoSec a per-IP cap and register rate.
type regLimiter struct {
	max, perIP, rate int
	store            server.Store

	mu   sync.Mutex
	hits map[string][]time.Time
}

func validEndpoint(ep string) bool {
	if ep == "" || len(ep) > 64 {
		return false
	}
	for _, r := range ep {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || r == '<' || r == '>' || r == '"' || r == '\'' || r == '&' {
			return false
		}
	}
	return true
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func (l *regLimiter) authorize(ep string, id server.Identity) bool {
	if !validEndpoint(ep) {
		log.Printf("refused: invalid endpoint name (%d bytes)", len(ep))
		return false
	}
	all := l.store.All()
	if _, again := l.store.ByEndpoint(ep); !again && len(all) >= l.max {
		log.Printf("refused %q: registration cap %d reached", ep, l.max)
		return false
	}
	if id.Mode != server.ModeNoSec {
		return true
	}
	ip := hostOf(id.Addr)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	recent := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.rate {
		l.hits[ip] = recent
		log.Printf("refused %q: register rate limit", ep)
		return false
	}
	l.hits[ip] = append(recent, now)
	if len(l.hits) > 10000 { // forget idle IPs
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > time.Minute {
				delete(l.hits, k)
			}
		}
	}
	n := 0
	for _, r := range all {
		if r.Identity.Mode == server.ModeNoSec && hostOf(r.Identity.Addr) == ip && r.Endpoint != ep {
			n++
		}
	}
	if n >= l.perIP {
		log.Printf("refused %q: per-IP cap %d", ep, l.perIP)
		return false
	}
	return true
}

// demoSecurity accepts any PSK identity with the one public demo key; the
// endpoint is the identity. Identities seen in a handshake are remembered
// (bounded) so that their endpoint names are then refused over NoSec.
type demoSecurity struct {
	key []byte
	max int

	mu    sync.Mutex
	seen  map[string]time.Time
	order []string
}

func newDemoSecurity(key []byte, max int) *demoSecurity {
	return &demoSecurity{key: key, max: max, seen: map[string]time.Time{}}
}

func (d *demoSecurity) info(id string) server.SecurityInfo {
	return server.SecurityInfo{Endpoint: id, PSKIdentity: id, PSKKey: d.key}
}

func (d *demoSecurity) ByPSKIdentity(id string) (server.SecurityInfo, bool) {
	if !validEndpoint(id) {
		return server.SecurityInfo{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[id]; !ok {
		d.order = append(d.order, id)
		for len(d.order) > d.max {
			delete(d.seen, d.order[0])
			d.order = d.order[1:]
		}
	}
	d.seen[id] = time.Now()
	return d.info(id), true
}

func (d *demoSecurity) ByEndpoint(ep string) (server.SecurityInfo, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[ep]; ok {
		return d.info(ep), true
	}
	return server.SecurityInfo{}, false
}

func (d *demoSecurity) Put(server.SecurityInfo) error {
	return errors.New("demo: read-only security store")
}
func (d *demoSecurity) Remove(string) (server.SecurityInfo, bool, error) {
	return server.SecurityInfo{}, false, nil
}
func (d *demoSecurity) All() []server.SecurityInfo { return nil }

func logEvent(e server.Event) {
	switch e := e.(type) {
	case server.Registered:
		r := e.Registration
		log.Printf("registered %q lwm2m=%s b=%s lt=%v mode=%v", r.Endpoint, r.Version, r.Binding, r.Lifetime, r.Identity.Mode)
	case server.Deregistered:
		log.Printf("deregistered %q: %s", e.Registration.Endpoint, e.Reason)
	}
}
