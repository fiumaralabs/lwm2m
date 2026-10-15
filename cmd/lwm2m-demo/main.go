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
//   - the web API only lists registrations and reads (never /0 or /21), with
//     a global concurrency limit and a per-IP rate limit.
//
//     go run ./cmd/lwm2m-demo -coap :5683 -coaps :5684 -http 127.0.0.1:8090
package main

import (
	"context"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/model"
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
	flag.Parse()

	key, err := hex.DecodeString(*pskHex)
	if err != nil || len(key) == 0 {
		log.Fatalf("-psk-key: need hex")
	}

	models := server.NewModels(model.Default())
	store := &clampStore{Store: server.NewMemoryStore(), max: *maxLifetime}
	sec := newDemoSecurity(key, *maxPSK)
	lim := &regLimiter{max: *maxReg, perIP: *maxPerIP, rate: *regPerMin, store: store, hits: map[string][]time.Time{}}
	srv := server.New(server.Config{
		Store:     store,
		Security:  sec,
		Schema:    models.Schema,
		Validator: models,
		Authorize: lim.authorize,
		OnEvent:   logEvent,
	})
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

	web := &webAPI{srv: srv, max: *maxReg, maxLifetime: *maxLifetime, trustProxy: *trustProxy,
		rate: *readsPerMin, hits: map[string][]time.Time{}, sem: make(chan struct{}, 4)}
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
func (d *demoSecurity) Remove(string) (server.SecurityInfo, bool) {
	return server.SecurityInfo{}, false
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

// webAPI serves the page and the read-only JSON API.
type webAPI struct {
	srv         *server.Server
	max         int
	maxLifetime time.Duration
	trustProxy  bool
	rate        int
	sem         chan struct{}

	mu   sync.Mutex
	hits map[string][]time.Time
}

type clientJSON struct {
	Endpoint     string    `json:"endpoint"`
	Version      string    `json:"version"`
	Binding      string    `json:"binding"`
	Queue        bool      `json:"queue"`
	Security     string    `json:"security"`
	LifetimeS    int64     `json:"lifetime_s"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeen     time.Time `json:"last_seen"`
	ObjectLinks  string    `json:"object_links"`
}

func (w *webAPI) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) { _, _ = rw.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/clients", w.clients)
	mux.HandleFunc("GET /api/clients/{ep}/read", w.read)
	return securityHeaders(mux)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		hd := rw.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'")
		h.ServeHTTP(rw, r)
	})
}

func writeJSON(rw http.ResponseWriter, code int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(v)
}

func (w *webAPI) clients(rw http.ResponseWriter, _ *http.Request) {
	regs := w.srv.Store().All()
	out := make([]clientJSON, 0, len(regs))
	for _, r := range regs {
		links := r.RawLinks
		if len(links) > 2048 {
			links = links[:2048] + "…"
		}
		sec := "NoSec"
		if r.Identity.Mode == server.ModePSK {
			sec = "PSK"
		}
		out = append(out, clientJSON{Endpoint: r.Endpoint, Version: r.Version, Binding: r.Binding, Queue: r.QueueMode,
			Security: sec, LifetimeS: int64(r.Lifetime / time.Second), RegisteredAt: r.RegisteredAt.UTC(),
			LastSeen: r.LastUpdate.UTC(), ObjectLinks: links})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RegisteredAt.After(out[j].RegisteredAt) })
	writeJSON(rw, 200, map[string]any{"clients": out, "max_registrations": w.max,
		"max_lifetime_s": int64(w.maxLifetime / time.Second), "now": time.Now().UTC()})
}

func (w *webAPI) clientIP(r *http.Request) string {
	ip := hostOf(r.RemoteAddr)
	if w.trustProxy && net.ParseIP(ip) != nil && net.ParseIP(ip).IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return ip
}

func (w *webAPI) allow(ip string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	recent := w.hits[ip][:0]
	for _, t := range w.hits[ip] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= w.rate {
		w.hits[ip] = recent
		return false
	}
	w.hits[ip] = append(recent, now)
	if len(w.hits) > 10000 {
		for k, v := range w.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > time.Minute {
				delete(w.hits, k)
			}
		}
	}
	return true
}

type nodeJSON struct {
	Path  string `json:"path"`
	Type  string `json:"type,omitempty"`
	Value string `json:"value"`
}

func (w *webAPI) read(rw http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	p, err := lwm2m.ParsePath(r.URL.Query().Get("path"))
	if err != nil || p.Len() == 0 {
		writeJSON(rw, 400, map[string]string{"error": "path must be /object[/instance[/resource]]"})
		return
	}
	if oid := p.Object(); oid == 0 || oid == 21 { // Security, OSCORE: credentials
		writeJSON(rw, 403, map[string]string{"error": "reading credentials objects is not allowed"})
		return
	}
	if !w.allow(w.clientIP(r)) {
		writeJSON(rw, 429, map[string]string{"error": "rate limited, try again in a minute"})
		return
	}
	select {
	case w.sem <- struct{}{}:
		defer func() { <-w.sem }()
	default:
		writeJSON(rw, 503, map[string]string{"error": "busy, try again"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp, err := w.srv.Read(ctx, ep, p, server.ReadOptions{})
	if err != nil {
		writeJSON(rw, 502, map[string]string{"error": err.Error()})
		return
	}
	out := map[string]any{"code": server.CodeString(resp.Code)}
	if resp.HasFormat {
		out["content_format"] = int(resp.ContentFormat)
	}
	nodes := make([]nodeJSON, 0, len(resp.Nodes))
	for i, n := range resp.Nodes {
		if i == 200 {
			break
		}
		if n.Kind != lwm2m.KindValue {
			nodes = append(nodes, nodeJSON{Path: n.PathString(), Value: "(empty)"})
			continue
		}
		v := n.Value.String()
		if len(v) > 512 {
			v = v[:512] + "…"
		}
		nodes = append(nodes, nodeJSON{Path: n.PathString(), Type: n.Value.Type.String(), Value: v})
	}
	out["nodes"] = nodes
	if resp.DecodeErr != nil {
		out["decode_error"] = resp.DecodeErr.Error()
	}
	writeJSON(rw, 200, out)
}
