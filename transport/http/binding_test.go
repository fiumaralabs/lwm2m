package http

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// seen is one request the Server sent to the device.
type seen struct {
	method, path, query, ct, accept, body string
}

// device is a scripted client-side HTTP server.
type device struct {
	*httptest.Server
	mu    sync.Mutex
	reply func(r *nethttp.Request) (status int, ct, body, location string)
	got   chan seen
}

func newDevice(t *testing.T) *device {
	d := &device{got: make(chan seen, 16)}
	d.reply = func(*nethttp.Request) (int, string, string, string) { return 200, "", "", "" }
	d.Server = httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		b, _ := io.ReadAll(r.Body)
		d.got <- seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), r.Header.Get("Accept"), string(b)}
		d.mu.Lock()
		status, ct, body, loc := d.reply(r)
		d.mu.Unlock()
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		if loc != "" {
			w.Header().Set("Location", loc)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(d.Close)
	return d
}

func (d *device) script(f func(r *nethttp.Request) (int, string, string, string)) {
	d.mu.Lock()
	d.reply = f
	d.mu.Unlock()
}

func (d *device) next(t *testing.T) seen {
	t.Helper()
	select {
	case s := <-d.got:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("device got no request")
		return seen{}
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	srv    *server.Server
	http   *httptest.Server
	dev    *device
	clock  *clock
	events chan server.Event
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{dev: newDevice(t), clock: &clock{t: time.Unix(1e9, 0)}, events: make(chan server.Event, 64)}
	e.srv = server.New(server.Config{Now: e.clock.now, ExpiryCheck: time.Hour, RequestTimeout: 3 * time.Second,
		OnEvent: func(ev server.Event) {
			select {
			case e.events <- ev:
			default:
			}
		}})
	t.Cleanup(func() { _ = e.srv.Close() })
	e.http = httptest.NewServer(New(e.srv, Config{ClientURL: func(string) string { return e.dev.URL }}))
	t.Cleanup(e.http.Close)
	return e
}

func (e *env) do(t *testing.T, method, path, ct, body string) *nethttp.Response {
	t.Helper()
	req, _ := nethttp.NewRequest(method, e.http.URL+path, strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	res, err := e.http.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res
}

const ep = "dev1"

func (e *env) register(t *testing.T, q, links string) string {
	t.Helper()
	res := e.do(t, "POST", "/rd?ep="+ep+"&lt=3600&lwm2m=1.2&b=H"+q, "application/link-format", links)
	if res.StatusCode != 201 {
		t.Fatalf("Register status %d", res.StatusCode)
	}
	return res.Header.Get("Location")
}

// Proves: HTTP-05, REG-05
func TestRegistrationInterface(t *testing.T) {
	e := newEnv(t)
	loc := e.register(t, "&sms=491711234567", "</1/0>,</3/0>,</5/0>")
	if !strings.HasPrefix(loc, "/rd/") || len(loc) <= 4 {
		t.Fatalf("Location %q, want under /rd", loc)
	}
	reg, ok := e.srv.Store().ByEndpoint(ep)
	if !ok || reg.Binding != "H" || reg.SMS != "491711234567" || reg.Lifetime != time.Hour {
		t.Fatalf("registration %+v", reg)
	}
	if res := e.do(t, "POST", loc+"?lt=60&b=H", "application/link-format", "</1/0>,</3/0>"); res.StatusCode != 204 {
		t.Fatalf("Update status %d", res.StatusCode)
	}
	if reg, _ := e.srv.Store().ByEndpoint(ep); reg.Lifetime != time.Minute || reg.HasObject(5) {
		t.Fatalf("updated %+v", reg)
	}
	if res := e.do(t, "POST", loc+"?bogus=1", "", ""); res.StatusCode != 400 {
		t.Errorf("Update with unknown parameter: %d", res.StatusCode)
	}
	if res := e.do(t, "DELETE", loc, "", ""); res.StatusCode != 204 {
		t.Fatalf("De-register status %d", res.StatusCode)
	}
	for _, m := range []string{"POST", "DELETE"} {
		if res := e.do(t, m, loc, "", ""); res.StatusCode != 404 {
			t.Errorf("%s after De-register: %d, want 404", m, res.StatusCode)
		}
	}
	for _, tc := range []struct {
		name, method, path, ct, body string
		want                         int
	}{
		{"no lifetime", "POST", "/rd?ep=x&lwm2m=1.2", "application/link-format", "</3/0>", 400},
		{"unknown version", "POST", "/rd?ep=x&lt=60&lwm2m=9.9", "application/link-format", "</3/0>", 412},
		{"no object list", "POST", "/rd?ep=x&lt=60&lwm2m=1.2", "", "", 409},
		{"unknown media type", "POST", "/rd?ep=x&lt=60&lwm2m=1.2", "application/x-nope", "</3/0>", 400},
		{"wrong media type", "POST", "/rd?ep=x&lt=60&lwm2m=1.2", "text/plain", "</3/0>", 400},
		{"GET /rd", "GET", "/rd", "", "", 405},
		{"PATCH", "PATCH", "/rd", "", "", 405},
		{"unknown path", "POST", "/xyz", "", "", 404},
	} {
		if res := e.do(t, tc.method, tc.path, tc.ct, tc.body); res.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, res.StatusCode, tc.want)
		}
	}
	e.srv.Security().Put(server.SecurityInfo{Endpoint: "fw", X509: true})
	if res := e.do(t, "POST", "/rd?ep=fw&lt=60&lwm2m=1.2", "application/link-format", "</3/0>"); res.StatusCode != 400 {
		t.Errorf("NoSec Register of an endpoint with credentials: %d, want 400", res.StatusCode)
	}
}

// Proves: HTTP-06, HTTP-10
func TestDeviceManagement(t *testing.T) {
	e := newEnv(t)
	e.register(t, "", "</1/0>,</3/0>,</5>")
	ctx := context.Background()
	run := func(f func() (*server.Response, error), want seen) *server.Response {
		t.Helper()
		ch := make(chan *server.Response, 1)
		go func() {
			r, err := f()
			if err != nil {
				t.Error(err)
			}
			ch <- r
		}()
		got := e.dev.next(t)
		if got != want {
			t.Errorf("device got %+v, want %+v", got, want)
		}
		return <-ch
	}
	e.dev.script(func(*nethttp.Request) (int, string, string, string) {
		return 200, "text/plain", "Open Mobile Alliance", ""
	})
	r := run(func() (*server.Response, error) {
		return e.srv.Read(ctx, ep, lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{Accept: fmtp(lwm2m.FormatText)})
	}, seen{method: "GET", path: "/3/0/0", accept: "text/plain"})
	if r.Code != codes.Content || len(r.Nodes) != 1 || r.Nodes[0].Value.Str != "Open Mobile Alliance" {
		t.Errorf("Read %+v", r)
	}
	e.dev.script(func(*nethttp.Request) (int, string, string, string) {
		return 200, "application/link-format", "</3/0>", ""
	})
	depth := 2
	if r := run(func() (*server.Response, error) { return e.srv.Discover(ctx, ep, lwm2m.MustParsePath("/3"), &depth) },
		seen{method: "GET", path: "/3", query: "depth=2", accept: "application/link-format"}); r.Code != codes.Content || string(r.Payload) != "</3/0>" {
		t.Errorf("Discover %+v", r)
	}
	e.dev.script(func(*nethttp.Request) (int, string, string, string) { return 204, "", "", "" })
	val := []lwm2m.Node{{Kind: lwm2m.KindValue, Path: lwm2m.MustParsePath("/1/0/1"), Value: lwm2m.Integer(60)}}
	if r := run(func() (*server.Response, error) {
		return e.srv.Write(ctx, ep, lwm2m.MustParsePath("/1/0/1"), val, server.WriteOptions{})
	}, seen{method: "PUT", path: "/1/0/1", ct: "text/plain", body: "60"}); r.Code != codes.Changed {
		t.Errorf("Write %v", r.Code)
	}
	run(func() (*server.Response, error) {
		return e.srv.Write(ctx, ep, lwm2m.MustParsePath("/1/0"), val, server.WriteOptions{Mode: server.PartialUpdate, Format: fmtp(lwm2m.FormatSenMLJSON)})
	}, seen{method: "POST", path: "/1/0", ct: "application/senml+json", body: `[{"bn":"/1/0/","n":"1","v":60}]`})
	if r := run(func() (*server.Response, error) {
		return e.srv.WriteAttributes(ctx, ep, lwm2m.MustParsePath("/3/0/9"), []string{"pmin=10", "gt=1.5", "lt=0.5", "con=1", "pmax"})
	}, seen{method: "PUT", path: "/3/0/9", query: "pmin=10&gt=1.5&lt=0.5&con=1&pmax"}); r.Code != codes.Changed {
		t.Errorf("Write-Attributes %v", r.Code)
	}
	run(func() (*server.Response, error) { return e.srv.Execute(ctx, ep, lwm2m.MustParsePath("/3/0/4"), "") },
		seen{method: "POST", path: "/3/0/4"})
	run(func() (*server.Response, error) {
		return e.srv.Execute(ctx, ep, lwm2m.MustParsePath("/3/0/4"), "0='x'")
	},
		seen{method: "POST", path: "/3/0/4", ct: "text/plain", body: "0='x'"})
	e.dev.script(func(*nethttp.Request) (int, string, string, string) { return 201, "", "", "/5/1" })
	inst := []lwm2m.Node{{Kind: lwm2m.KindValue, Path: lwm2m.MustParsePath("/5/1/1"), Value: lwm2m.String("coap://x")}}
	if r := run(func() (*server.Response, error) {
		return e.srv.Create(ctx, ep, lwm2m.MustParsePath("/5"), inst, fmtp(lwm2m.FormatSenMLJSON))
	}, seen{method: "POST", path: "/5", ct: "application/senml+json", body: `[{"bn":"/5/","n":"1/1","vs":"coap://x"}]`}); r.Code != codes.Created || strings.Join(r.Location, "/") != "5/1" {
		t.Errorf("Create %+v", r)
	}
	e.dev.script(func(*nethttp.Request) (int, string, string, string) { return 200, "", "", "" })
	if r := run(func() (*server.Response, error) { return e.srv.Delete(ctx, ep, lwm2m.MustParsePath("/5/1")) },
		seen{method: "DELETE", path: "/5/1"}); r.Code != codes.Deleted {
		t.Errorf("Delete %v", r.Code)
	}
}

func fmtp(f lwm2m.ContentFormat) *lwm2m.ContentFormat { return &f }

// Proves: HTTP-07
func TestStatusMapping(t *testing.T) {
	e := newEnv(t)
	e.register(t, "", "</3/0>")
	for status, want := range map[int]codes.Code{
		200: codes.Content, 204: codes.Content, 400: codes.BadRequest, 401: codes.Unauthorized, 403: codes.Forbidden,
		404: codes.NotFound, 405: codes.MethodNotAllowed, 406: codes.NotAcceptable, 413: codes.RequestEntityTooLarge,
		415: codes.UnsupportedMediaType, 500: codes.InternalServerError, 501: codes.NotImplemented,
		503: codes.ServiceUnavailable, 451: codes.BadRequest, 302: codes.InternalServerError,
	} {
		e.dev.script(func(*nethttp.Request) (int, string, string, string) { return status, "", "", "" })
		r, err := e.srv.Read(context.Background(), ep, lwm2m.MustParsePath("/3/0"), server.ReadOptions{})
		e.dev.next(t)
		if err != nil || r.Code != want {
			t.Errorf("HTTP %d -> %v (%v), want %v", status, r.Code, err, want)
		}
	}
	for c, want := range map[codes.Code]int{
		codes.Created: 201, codes.Changed: 204, codes.Deleted: 204, codes.Content: 200, codes.BadRequest: 400,
		codes.Forbidden: 403, codes.NotFound: 404, codes.PreconditionFailed: 412, 137: 409, codes.UnsupportedMediaType: 415,
	} {
		if got := Status(c); got != want {
			t.Errorf("Status(%v) = %d, want %d", c, got, want)
		}
	}
}

// Proves: HTTP-02
func TestAlternatePath(t *testing.T) {
	e := newEnv(t)
	e.register(t, "", `</lwm2m>;rt="oma.lwm2m",</lwm2m/3/0>,</lwm2m/5>`)
	ch := make(chan error, 1)
	go func() {
		_, err := e.srv.Create(context.Background(), ep, lwm2m.MustParsePath("/5"),
			[]lwm2m.Node{{Kind: lwm2m.KindValue, Path: lwm2m.MustParsePath("/5/0/1"), Value: lwm2m.String("x")}}, fmtp(lwm2m.FormatSenMLJSON))
		ch <- err
	}()
	if got := e.dev.next(t); got.path != "/lwm2m/5" {
		t.Errorf("Create went to %q, want /lwm2m/5", got.path)
	}
	if err := <-ch; err != nil {
		t.Fatal(err)
	}
}

// Proves: HTTP-08
func TestSend(t *testing.T) {
	e := newEnv(t)
	e.register(t, "", "</3/0>")
	body := `[{"bn":"/3/0/","n":"9","v":15},{"n":"20","v":4}]`
	if res := e.do(t, "POST", "/dp", "application/senml+json", body); res.StatusCode != 204 {
		t.Fatalf("Send status %d", res.StatusCode)
	}
	deadline := time.After(3 * time.Second)
	for got := false; !got; {
		select {
		case ev := <-e.events:
			var s server.SendReceived
			if s, got = ev.(server.SendReceived); got && len(s.Nodes) != 2 {
				t.Fatalf("nodes %v", s.Nodes)
			}
		case <-deadline:
			t.Fatal("no SendReceived event")
		}
	}
	for _, tc := range []struct {
		ct, body string
		want     int
	}{
		{"", body, 400},                                           // Content-Type is mandatory
		{"text/plain", "15", 400},                                 // not LwM2M CBOR, SenML JSON or SenML CBOR
		{"application/senml+json", "[", 400},                      // undecodable
		{"application/senml+json", `[{"n":"/9/0/0","v":1}]`, 404}, // object not registered
	} {
		if res := e.do(t, "POST", "/dp", tc.ct, tc.body); res.StatusCode != tc.want {
			t.Errorf("Send %q %q: %d, want %d", tc.ct, tc.body, res.StatusCode, tc.want)
		}
	}
}

// Proves: HTTP-01
func TestUnsupportedOperations(t *testing.T) {
	e := newEnv(t)
	e.register(t, "", "</3/0>")
	ctx := context.Background()
	paths := []lwm2m.Path{lwm2m.MustParsePath("/3/0/0")}
	if _, err := e.srv.ReadComposite(ctx, ep, paths, server.CompositeOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Read-Composite: %v", err)
	}
	if _, err := e.srv.WriteComposite(ctx, ep, []lwm2m.Node{{Kind: lwm2m.KindValue, Path: paths[0], Value: lwm2m.String("x")}}, nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Write-Composite: %v", err)
	}
	if _, _, err := e.srv.Observe(ctx, ep, paths[0], server.ObserveOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Observe: %v", err)
	}
	select {
	case s := <-e.dev.got:
		t.Errorf("device got %+v", s)
	default:
	}
}

// Proves: HTTP-01
// HTTP is secured by TLS: a client certificate the handshake verified is
// the X.509 identity (ep from the CN, another ep is 400). An unverified
// certificate (RequireAnyClientCert) authenticates nothing, so it cannot
// register an endpoint that has X.509 credentials.
func TestTLSClientCertificate(t *testing.T) {
	e := newEnv(t)
	cert := selfSigned(t, "certdev")
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	e.srv.Security().Put(server.SecurityInfo{Endpoint: "certdev", X509: true})
	post := func(cfg *tls.Config, path string) int {
		ts := httptest.NewUnstartedServer(New(e.srv, Config{}))
		ts.TLS = cfg
		ts.StartTLS()
		defer ts.Close()
		tr := ts.Client().Transport.(*nethttp.Transport).Clone()
		tr.TLSClientConfig.Certificates = []tls.Certificate{cert}
		req, _ := nethttp.NewRequest("POST", ts.URL+path, strings.NewReader("</3/0>"))
		req.Header.Set("Content-Type", "application/link-format")
		res, err := (&nethttp.Client{Transport: tr}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	unverified := &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	if s := post(unverified, "/rd?ep=certdev&lt=60&lwm2m=1.2"); s != 400 {
		t.Errorf("unverified certificate registered an X.509 endpoint: %d, want 400", s)
	}
	verified := &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	if s := post(verified, "/rd?ep=other&lt=60&lwm2m=1.2"); s != 400 {
		t.Errorf("ep not matching the certificate CN: %d, want 400", s)
	}
	if s := post(verified, "/rd?lt=60&lwm2m=1.2"); s != 201 { // ep derived from the CN (REG-02)
		t.Fatalf("Register over TLS: %d", s)
	}
	if reg, ok := e.srv.Store().ByEndpoint("certdev"); !ok || reg.Identity.Mode != server.ModeX509 {
		t.Fatalf("registration %+v", reg)
	}
}

// Proves: HTTP-09, QM-01
func TestQueueMode(t *testing.T) {
	e := newEnv(t)
	loc := e.register(t, "&Q", "</3/0>")
	e.clock.add(time.Hour) // past QueueAwake: the client is asleep
	ch := make(chan *server.Response, 1)
	go func() {
		r, _ := e.srv.Read(context.Background(), ep, lwm2m.MustParsePath("/3/0"), server.ReadOptions{})
		ch <- r
	}()
	select {
	case s := <-e.dev.got:
		t.Fatalf("sent to a sleeping client: %+v", s)
	case <-time.After(150 * time.Millisecond):
	}
	if res := e.do(t, "POST", loc, "", ""); res.StatusCode != 204 { // the client is awake
		t.Fatalf("Update %d", res.StatusCode)
	}
	if got := e.dev.next(t); got.path != "/3/0" {
		t.Fatalf("queued request %+v", got)
	}
	if r := <-ch; r == nil || r.Code != codes.Content {
		t.Fatalf("queued Read %+v", r)
	}
}

func selfSigned(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
