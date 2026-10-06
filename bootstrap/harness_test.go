package bootstrap

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type harness struct {
	t       *testing.T
	bs      *Server
	udp     string
	dtls    string
	configs *MemoryConfigStore
	sec     *server.MemorySecurityStore
	results chan Result
	ctx     context.Context
}

func newHarness(t *testing.T, mod ...func(*Config)) *harness {
	t.Helper()
	h := &harness{t: t, configs: NewMemoryConfigStore(), sec: server.NewMemorySecurityStore(), results: make(chan Result, 32)}
	cfg := Config{Configs: h.configs, Security: h.sec, RequestTimeout: 5 * time.Second,
		OnSession: func(r Result) { h.results <- r }}
	for _, m := range mod {
		m(&cfg)
	}
	h.bs = New(cfg)
	a, err := h.bs.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.udp = a.String()
	d, err := h.bs.ListenDTLS("127.0.0.1:0", DTLSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	h.dtls = d.String()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	h.ctx = ctx
	t.Cleanup(func() {
		cancel()
		_ = h.bs.Close()
	})
	return h
}

func p(s string) lwm2m.Path { return lwm2m.MustParsePath(s) }

func u16(v uint16) *uint16  { return &v }
func u32p(v uint32) *uint32 { return &v }

// c13 fills the client with configuration C.13 (ETS): the BS account in
// Security instance 1, DEV-MIN, and no /1 instance.
func c13(c *testclient.BootstrapClient, mode SecurityMode, identity, key string) {
	c.Set(p("/0/1/0"), lwm2m.String("coaps://bs.example.com:5784"))
	c.Set(p("/0/1/1"), lwm2m.Boolean(true))
	c.Set(p("/0/1/2"), lwm2m.Integer(int64(mode)))
	c.Set(p("/0/1/3"), lwm2m.Opaque([]byte(identity)))
	c.Set(p("/0/1/5"), lwm2m.Opaque([]byte(key)))
	c.Set(p("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	c.Set(p("/3/0/1"), lwm2m.String("Lightweight M2M Client"))
	c.Set(p("/3/0/2"), lwm2m.String("345000123"))
	c.Set(p("/3/0/3"), lwm2m.String("1.0"))
	c.Set(p("/3/0/11/0"), lwm2m.Integer(0))
	c.Set(p("/3/0/16"), lwm2m.String("U"))
	c.AddObject(1)
}

// c1 is the bootstrap config that provisions C.1: PSK-SA + SRV-MIN.
func c1(uri, identity, key string) *BootstrapConfig {
	return &BootstrapConfig{
		Security: map[uint16]SecurityConfig{0: {URI: uri, SecurityMode: ModePSK,
			PublicKeyOrID: Bytes(identity), SecretKey: Bytes(key), ServerID: u16(1)}},
		Servers: map[uint16]ServerConfig{0: {ShortID: 1, Lifetime: 86400, Binding: "U"}},
	}
}

// client returns a NoSec C.13 client dialled to the BS.
func (h *harness) client(ep string, cfg testclient.Config) *testclient.BootstrapClient {
	h.t.Helper()
	cfg.Endpoint = ep
	c := testclient.NewBootstrap(cfg)
	c13(c, ModeNoSec, "", "")
	addr := h.udp
	if cfg.PSKIdentity != "" {
		addr = h.dtls
	}
	if err := c.Dial(addr); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = c.Close() })
	return c
}

func (h *harness) put(ep string, c *BootstrapConfig) {
	h.t.Helper()
	if err := h.configs.Put(ep, c); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) result() Result {
	h.t.Helper()
	select {
	case r := <-h.results:
		return r
	case <-time.After(10 * time.Second):
		h.t.Fatal("no session result")
		return Result{}
	}
}

// bootstrap runs a Bootstrap-Request and waits for Finish.
func (h *harness) bootstrap(c *testclient.BootstrapClient, query []string) (codes.Code, Result) {
	h.t.Helper()
	r, err := c.BootstrapRequest(h.ctx, query)
	mustCode(h.t, r, err, codes.Changed)
	fin, err := c.WaitFinish(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return fin, h.result()
}

func mustCode(t *testing.T, r *testclient.Response, err error, want codes.Code) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if r.Code != want {
		t.Fatalf("code %s, want %s", server.CodeString(r.Code), server.CodeString(want))
	}
}

func cfp(f lwm2m.ContentFormat) *lwm2m.ContentFormat { return &f }

// fakePeer is a scripted server.Peer for session tests that need control
// over timing.
type fakePeer struct {
	id server.Identity
	fn func(ctx context.Context, n int, m *server.Message) (*server.Message, error)
	mu sync.Mutex
	n  int
}

func (f *fakePeer) Identity() server.Identity { return f.id }
func (f *fakePeer) RemoteAddr() net.Addr      { return fakeAddr{} }
func (f *fakePeer) Binding() string           { return "U" }
func (f *fakePeer) Exchange(ctx context.Context, m *server.Message) (*server.Message, error) {
	f.mu.Lock()
	f.n++
	n := f.n
	f.mu.Unlock()
	return f.fn(ctx, n, m)
}

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake" }

// answer replies like a cooperative client: 2.02, 2.04, 2.05.
func answer(m *server.Message) *server.Message {
	switch m.Code {
	case codes.DELETE:
		return &server.Message{Code: codes.Deleted}
	case codes.GET:
		return &server.Message{Code: codes.Content}
	}
	return &server.Message{Code: codes.Changed}
}

// hang blocks until the attempt's context ends.
func hang(ctx context.Context) (*server.Message, error) {
	<-ctx.Done()
	return nil, errors.New("timeout: " + ctx.Err().Error())
}
