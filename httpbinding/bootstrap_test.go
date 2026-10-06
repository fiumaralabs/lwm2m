package httpbinding

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/oscore"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type bsEnv struct {
	bs      *bootstrap.Server
	http    *httptest.Server
	dev     *device
	configs *bootstrap.MemoryConfigStore
	results chan bootstrap.Result
}

// newBSEnv runs a Bootstrap-Server over HTTP whose clients live at url
// (default: a scripted device).
func newBSEnv(t *testing.T, cfg bootstrap.Config, url ...string) *bsEnv {
	t.Helper()
	e := &bsEnv{dev: newDevice(t), results: make(chan bootstrap.Result, 16)}
	target := e.dev.URL
	if len(url) > 0 {
		target = url[0]
	}
	e.configs = bootstrap.NewMemoryConfigStore()
	cfg.Configs = e.configs
	cfg.RequestTimeout = 3 * time.Second
	cfg.OnSession = func(r bootstrap.Result) { e.results <- r }
	e.bs = bootstrap.New(cfg)
	t.Cleanup(func() { _ = e.bs.Close() })
	e.http = httptest.NewServer(NewBootstrap(e.bs, Config{ClientURL: func(string) string { return target }}))
	t.Cleanup(e.http.Close)
	return e
}

func (e *bsEnv) do(t *testing.T, method, path, accept string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, e.http.URL+path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := e.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func (e *bsEnv) result(t *testing.T) bootstrap.Result {
	t.Helper()
	select {
	case r := <-e.results:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no bootstrap result")
		return bootstrap.Result{}
	}
}

func u16p(v uint16) *uint16 { return &v }

func bsConfig() *bootstrap.BootstrapConfig {
	return &bootstrap.BootstrapConfig{
		Discover: true,
		ToDelete: []string{"/0", "/1"},
		Security: map[uint16]bootstrap.SecurityConfig{0: {URI: "https://dm.example.com", SecurityMode: bootstrap.ModeNoSec, ServerID: u16p(1)}},
		Servers:  map[uint16]bootstrap.ServerConfig{0: {ShortID: 1, Lifetime: 3600, Binding: "H"}},
		Read:     []string{"/2"},
	}
}

// cooperative answers the session's requests like a client would.
func cooperative(r *http.Request) (int, string, string, string) {
	switch {
	case r.Method == http.MethodGet && r.Header.Get("Accept") == "application/link-format":
		return 200, "application/link-format", `</>;lwm2m=1.2,</0/1>,</1>,</2>,</3/0>`, ""
	case r.Method == http.MethodGet:
		return 204, "", "", "" // Read of an empty /2
	case r.Method == http.MethodPost:
		return 204, "", "", "" // Finish
	}
	return 204, "", "", ""
}

// Bootstrap over HTTP (T §7.1.2 Tbl 7.1.2-1, §7.3 Tbl 7.3-1): POST
// /bs?ep=&pct= answers 200 and the BS then sends Bootstrap-Discover (GET /
// with Accept application/link-format), -Delete (DELETE /0, /1), -Write
// (PUT /0/0, /1/0 with the pct media type), -Read (GET /2 with Accept) and
// -Finish (POST /bs) to the client's HTTP server; 200 and 204 count as
// success. Bootstrap-Request errors: 400 (unknown endpoint, a
// non-POST), 415 (unsupported pct). Bootstrap-Pack-Request: GET
// /bspack?ep= with Accept answers 200 with the Pack; 400 unknown ep, 405
// refused, 406 unsupported Accept, 501 not supported. A client's 406 on
// Finish fails the session.
// Proves: HTTP-03, HTTP-04
func TestBootstrapOverHTTP(t *testing.T) {
	e := newBSEnv(t, bootstrap.Config{})
	if err := e.configs.Put("bs-ep", bsConfig()); err != nil {
		t.Fatal(err)
	}
	e.dev.script(cooperative)
	res, _ := e.do(t, "POST", "/bs?ep=bs-ep&pct=11542", "")
	if res.StatusCode != 200 {
		t.Fatalf("Bootstrap-Request status %d", res.StatusCode)
	}
	type step struct{ method, path, ct, accept string }
	want := []step{
		{"GET", "/", "", "application/link-format"},
		{"DELETE", "/0", "", ""},
		{"DELETE", "/1", "", ""},
		{"PUT", "/0/0", lwm2m.FormatTLV.String(), ""},
		{"PUT", "/1/0", lwm2m.FormatTLV.String(), ""},
		{"GET", "/2", "", lwm2m.FormatTLV.String()},
		{"POST", "/bs", "", ""},
	}
	for _, w := range want {
		s := e.dev.next(t)
		if s.method != w.method || s.path != w.path || s.ct != w.ct || s.accept != w.accept {
			t.Fatalf("got %+v, want %+v", s, w)
		}
	}
	if r := e.result(t); r.Err != nil || r.Endpoint != "bs-ep" {
		t.Fatalf("session %+v", r)
	}

	for _, c := range []struct {
		method, path, accept string
		status               int
	}{
		{"POST", "/bs?ep=unknown", "", 400},
		{"POST", "/bs?ep=bs-ep&pct=0", "", 415},
		{"GET", "/bs?ep=bs-ep", "", 400}, // not the Bootstrap-Request method
		{"GET", "/bspack?ep=bs-ep", lwm2m.FormatSenMLCBOR.String(), 200},
		{"GET", "/bspack?ep=unknown", lwm2m.FormatSenMLCBOR.String(), 400},
		{"GET", "/bspack?ep=bs-ep", "text/plain", 406},
		{"GET", "/elsewhere", "", 404},
	} {
		res, body := e.do(t, c.method, c.path, c.accept)
		if res.StatusCode != c.status {
			t.Fatalf("%s %s: %d, want %d", c.method, c.path, res.StatusCode, c.status)
		}
		if c.status == 200 && (res.Header.Get("Content-Type") != lwm2m.FormatSenMLCBOR.String() || len(body) == 0) {
			t.Fatalf("Pack: %q %d bytes", res.Header.Get("Content-Type"), len(body))
		}
	}
	e.result(t) // the Pack

	refused := bsConfig()
	refused.RefusePack = true
	_ = e.configs.Put("refused", refused)
	if res, _ := e.do(t, "GET", "/bspack?ep=refused", ""); res.StatusCode != 405 {
		t.Fatalf("refused Pack: %d", res.StatusCode)
	}
	e.result(t)
	off := newBSEnv(t, bootstrap.Config{DisablePack: true})
	if res, _ := off.do(t, "GET", "/bspack?ep=bs-ep", ""); res.StatusCode != 501 {
		t.Fatalf("Pack not supported: %d", res.StatusCode)
	}

	// Finish answered 406 (inconsistent configuration).
	e.dev.script(func(r *http.Request) (int, string, string, string) {
		if r.Method == http.MethodPost {
			return 406, "", "", ""
		}
		return cooperative(r)
	})
	res, _ = e.do(t, "POST", "/bs?ep=bs-ep", "")
	if res.StatusCode != 200 {
		t.Fatalf("Bootstrap-Request status %d", res.StatusCode)
	}
	if r := e.result(t); !errors.Is(r.Err, bootstrap.ErrFinishRejected) {
		t.Fatalf("finish 406: %v", r.Err)
	}
	// Tbl 7.1.2-1 allows Bootstrap-Read on Object 2 only.
	lf := lwm2m.FormatTLV
	if err := checkBootstrapRequest(&server.Message{Code: codes.GET, Path: "/1", Accept: &lf}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("read /1: %v", err)
	}
}

// oscoreDevice is a client HTTP server whose traffic is OSCORE-protected
// in HTTP (RFC 8613 §11): it verifies each POST with the OSCORE header
// field, records the inner request and answers a protected response.
type oscoreDevice struct {
	*httptest.Server
	ctx *oscore.Context
	got chan string
}

func newOSCOREDevice(t *testing.T, ctx *oscore.Context) *oscoreDevice {
	d := &oscoreDevice{ctx: ctx, got: make(chan string, 32)}
	d.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		in, err := fromHTTP(r.Header.Get("OSCORE"), r.Header.Get("Content-Type"), codes.POST, body)
		if err != nil || r.Method != http.MethodPost {
			d.got <- "unprotected " + r.Method + " " + r.URL.Path
			w.WriteHeader(401)
			return
		}
		inner, x, err := ctx.UnprotectRequest(in)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		p, _ := inner.Options.Path()
		d.got <- inner.Code.String() + " /" + strings.TrimPrefix(p, "/")
		code := codes.Changed
		switch inner.Code {
		case codes.GET:
			code = codes.Content
		case codes.DELETE:
			code = codes.Deleted
		}
		prot, _ := ctx.ProtectResponse(message.Message{Code: code}, x, false)
		toHTTP(w, prot)
	}))
	t.Cleanup(d.Close)
	return d
}

// OSCORE in HTTP (T §5.4.1, RFC 8613 §11): the client's Bootstrap-Request
// is a POST with the OSCORE header field and an application/oscore body;
// the BS demands Echo on the new context (200 + OSCORE header, inner
// 4.01), serves the repeated request (inner 2.04), and sends the
// session's requests to the client's HTTP server protected the same way.
// Unprotected HTTP Bootstrap-Requests for the OSCORE endpoint get 401.
// Proves: OSC-09
func TestOSCOREOverHTTPBootstrap(t *testing.T) {
	// RFC 8613 §11.5 example ("OSCORE: CSU" for option 0x0925) and the
	// empty-option form.
	if got := encodeOSCOREHeader([]byte{0x09, 0x25}); got != "CSU" {
		t.Fatalf("header %q", got)
	}
	if v, err := decodeOSCOREHeader("AA"); err != nil || len(v) != 0 || encodeOSCOREHeader(nil) != "AA" {
		t.Fatal("empty OSCORE option")
	}
	cp := oscore.Params{MasterSecret: make([]byte, 16), SenderID: []byte("dev"), RecipientID: []byte("bs")}
	_, _ = rand.Read(cp.MasterSecret)
	cl, _ := oscore.New(cp)
	dev := newOSCOREDevice(t, cl)
	e := newBSEnv(t, bootstrap.Config{}, dev.URL)
	if err := e.bs.EnableOSCORE().Put("osc-ep", cp.Reverse()); err != nil {
		t.Fatal(err)
	}
	cfg := bsConfig()
	cfg.Discover, cfg.Read, cfg.ToDelete = false, nil, nil
	_ = e.configs.Put("osc-ep", cfg)

	send := func(extra ...message.Option) message.Message {
		t.Helper()
		m := message.Message{Code: codes.POST, Token: []byte{1, 2}, Options: message.Options{
			{ID: message.URIPath, Value: []byte("bs")}, {ID: message.URIQuery, Value: []byte("ep=osc-ep")}}}
		m.Options = append(m.Options, extra...)
		prot, x, err := cl.ProtectRequest(m)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := prot.Options.GetBytes(oscore.OptionOSCORE)
		req, _ := http.NewRequest("POST", e.http.URL+"/", bytes.NewReader(prot.Payload))
		req.Header.Set("OSCORE", encodeOSCOREHeader(v))
		req.Header.Set("Content-Type", oscoreMediaType)
		res, err := e.http.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != oscoreMediaType {
			t.Fatalf("status %d %q", res.StatusCode, body)
		}
		in, err := fromHTTP(res.Header.Get("OSCORE"), res.Header.Get("Content-Type"), codes.Changed, body)
		if err != nil {
			t.Fatal(err)
		}
		inner, err := cl.UnprotectResponse(in, x)
		if err != nil {
			t.Fatal(err)
		}
		return inner
	}
	r := send()
	echo, err := r.Options.GetBytes(oscore.OptionEcho)
	if r.Code != codes.Unauthorized || err != nil {
		t.Fatalf("first use: %v", r.Code)
	}
	if r = send(message.Option{ID: oscore.OptionEcho, Value: echo}); r.Code != codes.Changed {
		t.Fatalf("Bootstrap-Request: %v", r.Code)
	}
	for _, w := range []string{"PUT /0/0", "PUT /1/0", "POST /bs"} {
		select {
		case g := <-dev.got:
			if g != w {
				t.Fatalf("device got %q, want %q", g, w)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no request at the device")
		}
	}
	if res := e.result(t); res.Err != nil || res.Endpoint != "osc-ep" {
		t.Fatalf("session %+v", res)
	}
	if res, _ := e.do(t, "POST", "/bs?ep=osc-ep", ""); res.StatusCode != 401 {
		t.Fatalf("unprotected: %d", res.StatusCode)
	}
}

// Proves: HTTP-02
// The Bootstrap-Server uses plain /{o}/{i}/{r} paths over HTTP even when
// the client's Bootstrap-Discover reports an alternate path (T §7.1.1).
func TestBootstrapIgnoresAlternatePath(t *testing.T) {
	e := newBSEnv(t, bootstrap.Config{})
	if err := e.configs.Put("bs-ep", bsConfig()); err != nil {
		t.Fatal(err)
	}
	e.dev.script(func(r *http.Request) (int, string, string, string) {
		if r.Method == http.MethodGet && r.Header.Get("Accept") == "application/link-format" {
			return 200, "application/link-format", `</lwm2m>;rt="oma.lwm2m";lwm2m=1.2,</lwm2m/0/1>,</lwm2m/1>,</lwm2m/2>,</lwm2m/3/0>`, ""
		}
		return cooperative(r)
	})
	if res, _ := e.do(t, "POST", "/bs?ep=bs-ep&pct=11542", ""); res.StatusCode != 200 {
		t.Fatalf("Bootstrap-Request status %d", res.StatusCode)
	}
	for _, want := range []string{"/", "/0", "/1", "/0/0", "/1/0", "/2", "/bs"} {
		if s := e.dev.next(t); s.path != want {
			t.Fatalf("BS sent %s %s, want path %s", s.method, s.path, want)
		}
	}
	if r := e.result(t); r.Err != nil {
		t.Fatal(r.Err)
	}
}
