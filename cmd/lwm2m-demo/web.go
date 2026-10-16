package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"math"
	"mime"
	"net"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
)

// webLimits are the knobs of the public page and API.
type webLimits struct {
	maxReg       int
	maxLifetime  time.Duration
	trustProxy   bool
	readsPerMin  int
	observeMax   int
	observePerIP int
	observeFor   time.Duration
}

// webAPI serves the page and the read-only JSON API. It never writes,
// executes, creates or deletes on a client, and never reads /0 or /21.
type webAPI struct {
	srv    *server.Server
	models *server.Models
	lim    webLimits
	sem    chan struct{} // concurrent reads
	hub    *obsHub
	static map[string]staticFile

	mu   sync.Mutex
	hits map[string][]time.Time

	sendMu sync.Mutex
	sends  map[string]sendJSON // registration ID -> last Send
}

func newWebAPI(models *server.Models, lim webLimits) *webAPI {
	w := &webAPI{models: models, lim: lim, sem: make(chan struct{}, 4),
		hits: map[string][]time.Time{}, sends: map[string]sendJSON{}}
	w.hub = newObsHub(w, lim.observeMax, lim.observePerIP, lim.observeFor)
	w.static = loadStatic()
	return w
}

// onEvent is called from the server's event hook; it must not block.
func (w *webAPI) onEvent(e server.Event) {
	switch e := e.(type) {
	case server.SendReceived:
		s := sendJSON{At: time.Now().UTC(), ContentFormat: e.ContentFormat.String(), Nodes: nodesJSON(e.Nodes)}
		w.sendMu.Lock()
		w.sends[e.Registration.ID] = s
		w.sendMu.Unlock()
	case server.Registered:
		if e.Replaced != nil {
			w.forget(e.Replaced.ID)
		}
	case server.Deregistered:
		w.forget(e.Registration.ID)
	}
	w.hub.onEvent(e)
}

func (w *webAPI) forget(regID string) {
	w.sendMu.Lock()
	delete(w.sends, regID)
	w.sendMu.Unlock()
}

type objectSummary struct {
	ID        uint16 `json:"id"`
	Name      string `json:"name,omitempty"`
	Instances int    `json:"instances"`
}

type clientJSON struct {
	Endpoint       string          `json:"endpoint"`
	Version        string          `json:"version"`
	Binding        string          `json:"binding"`
	Queue          bool            `json:"queue"`
	Security       string          `json:"security"`
	LifetimeS      int64           `json:"lifetime_s"`
	RegisteredAt   time.Time       `json:"registered_at"`
	LastSeen       time.Time       `json:"last_seen"`
	ExpiresAt      time.Time       `json:"expires_at"`
	ObjectLinks    string          `json:"object_links"`
	RootPath       string          `json:"root_path,omitempty"`
	ContentFormats []string        `json:"content_formats,omitempty"`
	Objects        []objectSummary `json:"objects"`
}

type sendJSON struct {
	At            time.Time  `json:"at"`
	ContentFormat string     `json:"content_format"`
	Nodes         []nodeJSON `json:"nodes"`
}

func (w *webAPI) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", w.serveStatic)
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) { _, _ = rw.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/clients", w.clients)
	mux.HandleFunc("GET /api/clients/{ep}", w.client)
	mux.HandleFunc("GET /api/clients/{ep}/read", w.read)
	mux.HandleFunc("GET /api/clients/{ep}/observe", w.hub.serve)
	return securityHeaders(mux)
}

const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		hd := rw.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Content-Security-Policy", csp)
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		hd.Set("Cross-Origin-Resource-Policy", "same-origin")
		hd.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		h.ServeHTTP(rw, r)
	})
}

type staticFile struct {
	body  []byte
	ctype string
	etag  string
	cache string
}

func loadStatic() map[string]staticFile {
	out := map[string]staticFile{}
	sub, _ := fs.Sub(webFS, "web")
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		ct := map[string]string{".woff2": "font/woff2", ".svg": "image/svg+xml", ".ico": "image/x-icon",
			".webmanifest": "application/manifest+json"}[path.Ext(p)]
		if ct == "" {
			ct = mime.TypeByExtension(path.Ext(p))
		}
		if ct == "" {
			ct = "application/octet-stream"
		}
		cache := "no-cache" // revalidate with the ETag
		if strings.HasPrefix(p, "fonts/") {
			cache = "public, max-age=31536000, immutable"
		}
		out["/"+p] = staticFile{body: b, ctype: ct, etag: `"` + hex.EncodeToString(sum[:8]) + `"`, cache: cache}
		return nil
	})
	return out
}

func (w *webAPI) serveStatic(rw http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if p == "/" {
		p = "/index.html"
	}
	f, ok := w.static[p]
	if !ok {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", f.ctype)
	rw.Header().Set("ETag", f.etag)
	rw.Header().Set("Cache-Control", f.cache)
	http.ServeContent(rw, r, "", time.Time{}, bytes.NewReader(f.body))
}

func writeJSON(rw http.ResponseWriter, code int, v any) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(code)
	_ = json.NewEncoder(rw).Encode(v)
}

func (w *webAPI) clientOf(r *server.Registration) clientJSON {
	links := r.RawLinks
	if len(links) > 2048 {
		links = links[:2048] + "…"
	}
	sec := "NoSec"
	if r.Identity.Mode == server.ModePSK {
		sec = "PSK"
	}
	c := clientJSON{Endpoint: r.Endpoint, Version: r.Version, Binding: r.Binding, Queue: r.QueueMode,
		Security: sec, LifetimeS: int64(r.Lifetime / time.Second), RegisteredAt: r.RegisteredAt.UTC(),
		LastSeen: r.LastUpdate.UTC(), ObjectLinks: links, Objects: []objectSummary{}}
	if r.Lifetime > 0 {
		c.ExpiresAt = r.LastUpdate.Add(r.Lifetime).UTC()
	}
	if r.RootPath != "" && r.RootPath != "/" {
		c.RootPath = r.RootPath
	}
	for _, f := range r.ContentFormats {
		c.ContentFormats = append(c.ContentFormats, f.String())
	}
	for _, o := range objectsOf(w.models, r) {
		s := objectSummary{ID: o.ID, Instances: len(o.Instances)}
		if o.Model != nil {
			s.Name = o.Model.Name
		}
		c.Objects = append(c.Objects, s)
	}
	return c
}

func (w *webAPI) clients(rw http.ResponseWriter, _ *http.Request) {
	regs := w.srv.Store().All()
	out := make([]clientJSON, 0, len(regs))
	for _, r := range regs {
		out = append(out, w.clientOf(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RegisteredAt.After(out[j].RegisteredAt) })
	writeJSON(rw, 200, map[string]any{"clients": out, "max_registrations": w.lim.maxReg,
		"max_lifetime_s": int64(w.lim.maxLifetime / time.Second), "now": time.Now().UTC()})
}

func (w *webAPI) client(rw http.ResponseWriter, r *http.Request) {
	reg, ok := w.srv.Store().ByEndpoint(r.PathValue("ep"))
	if !ok {
		writeJSON(rw, 404, map[string]string{"error": "not registered"})
		return
	}
	out := map[string]any{"client": w.clientOf(reg), "objects": objectsOf(w.models, reg), "now": time.Now().UTC(),
		"limits": map[string]any{"reads_per_minute": w.lim.readsPerMin, "observe_for_s": int64(w.lim.observeFor / time.Second),
			"observe_per_ip": w.lim.observePerIP}}
	w.sendMu.Lock()
	if s, ok := w.sends[reg.ID]; ok {
		out["last_send"] = s
	}
	w.sendMu.Unlock()
	writeJSON(rw, 200, out)
}

func (w *webAPI) clientIP(r *http.Request) string {
	ip := hostOf(r.RemoteAddr)
	if w.lim.trustProxy && net.ParseIP(ip) != nil && net.ParseIP(ip).IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return ip
}

// allow takes one read from ip's budget and reports what is left.
func (w *webAPI) allow(ip string) (bool, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	recent := w.hits[ip][:0]
	for _, t := range w.hits[ip] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= w.lim.readsPerMin {
		w.hits[ip] = recent
		return false, 0
	}
	w.hits[ip] = append(recent, now)
	if len(w.hits) > 10000 {
		for k, v := range w.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > time.Minute {
				delete(w.hits, k)
			}
		}
	}
	return true, w.lim.readsPerMin - len(w.hits[ip])
}

type nodeJSON struct {
	Path  string `json:"path"`
	Type  string `json:"type,omitempty"`
	Value string `json:"value"`       // Go rendering, kept for API compatibility
	V     any    `json:"v,omitempty"` // typed: string, number, bool; opaque as hex; objlnk "o:i"
}

func typed(v lwm2m.Value) any {
	switch v.Type {
	case lwm2m.TypeString, lwm2m.TypeCorelnk:
		return v.Str
	case lwm2m.TypeInteger, lwm2m.TypeTime:
		return v.Int
	case lwm2m.TypeUnsigned:
		return v.Uint
	case lwm2m.TypeFloat:
		if math.IsNaN(v.Float) || math.IsInf(v.Float, 0) {
			return strconv.FormatFloat(v.Float, 'g', -1, 64)
		}
		return v.Float
	case lwm2m.TypeBoolean:
		return v.Bool
	case lwm2m.TypeOpaque:
		b := v.Bytes
		if len(b) > 256 {
			b = b[:256]
		}
		return hex.EncodeToString(b)
	case lwm2m.TypeObjlnk:
		return v.Link.String()
	}
	return nil
}

func nodesJSON(ns []lwm2m.Node) []nodeJSON {
	nodes := make([]nodeJSON, 0, len(ns))
	for i, n := range ns {
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
		tv := typed(n.Value)
		if s, ok := tv.(string); ok && len(s) > 1024 {
			tv = s[:1024] + "…"
		}
		nodes = append(nodes, nodeJSON{Path: n.PathString(), Type: n.Value.Type.String(), Value: v, V: tv})
	}
	return nodes
}

// responseJSON renders a Read response or a notification.
func responseJSON(resp *server.Response, took time.Duration) map[string]any {
	out := map[string]any{"code": server.CodeString(resp.Code), "at": time.Now().UTC()}
	if took > 0 {
		out["ms"] = took.Milliseconds()
	}
	if resp.HasFormat {
		out["content_format"] = int(resp.ContentFormat)
		out["content_format_name"] = resp.ContentFormat.String()
	}
	out["nodes"] = nodesJSON(resp.Nodes)
	if len(resp.Payload) > 0 {
		p := resp.Payload
		if len(p) > 2048 {
			p = p[:2048]
			out["payload_truncated"] = true
		}
		out["payload_hex"] = hex.EncodeToString(p)
		out["payload_len"] = len(resp.Payload)
	}
	if resp.DecodeErr != nil {
		out["decode_error"] = resp.DecodeErr.Error()
	}
	return out
}

// checkPath parses the path query parameter and refuses the credentials objects.
func checkPath(rw http.ResponseWriter, r *http.Request) (lwm2m.Path, bool) {
	p, err := lwm2m.ParsePath(r.URL.Query().Get("path"))
	if err != nil || p.Len() == 0 {
		writeJSON(rw, 400, map[string]string{"error": "path must be /object[/instance[/resource]]"})
		return p, false
	}
	if blockedObject(p.Object()) {
		writeJSON(rw, 403, map[string]string{"error": "reading credentials objects is not allowed"})
		return p, false
	}
	return p, true
}

func (w *webAPI) rateLimit(rw http.ResponseWriter, r *http.Request) bool {
	ok, left := w.allow(w.clientIP(r))
	rw.Header().Set("X-RateLimit-Limit", strconv.Itoa(w.lim.readsPerMin))
	rw.Header().Set("X-RateLimit-Remaining", strconv.Itoa(left))
	if !ok {
		rw.Header().Set("Retry-After", "60")
		writeJSON(rw, 429, map[string]string{"error": "rate limited: " + strconv.Itoa(w.lim.readsPerMin) + " reads per minute, try again shortly"})
	}
	return ok
}

func (w *webAPI) read(rw http.ResponseWriter, r *http.Request) {
	ep := r.PathValue("ep")
	p, ok := checkPath(rw, r)
	if !ok || !w.rateLimit(rw, r) {
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
	start := time.Now()
	resp, err := w.srv.Read(ctx, ep, p, server.ReadOptions{})
	if err != nil {
		writeJSON(rw, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(rw, 200, responseJSON(resp, time.Since(start)))
}
