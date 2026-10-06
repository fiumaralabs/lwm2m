package mqttbinding

import (
	"context"
	"encoding/hex"
	"errors"
	"net/url"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// bsEnv is a Bootstrap-Server and its MQTT binding on a broker.
type bsEnv struct {
	addr    string
	bs      *bootstrap.Server
	b       *Binding
	configs *bootstrap.MemoryConfigStore
	results chan bootstrap.Result
}

func newBSEnv(t *testing.T, addr, prefix string, cfg bootstrap.Config, opts ...func(*Config)) *bsEnv {
	t.Helper()
	if addr == "" {
		addr = startBroker(t, nil)
	}
	e := &bsEnv{addr: addr, configs: bootstrap.NewMemoryConfigStore(), results: make(chan bootstrap.Result, 16)}
	cfg.Configs = e.configs
	cfg.RequestTimeout = 3 * time.Second
	cfg.OnSession = func(r bootstrap.Result) { e.results <- r }
	e.bs = bootstrap.New(cfg)
	t.Cleanup(func() { _ = e.bs.Close() })
	c := Config{Broker: "mqtt://" + addr, Prefix: prefix}
	for _, o := range opts {
		o(&c)
	}
	var err error
	if e.b, err = NewBootstrap(e.bs, c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.b.Close)
	return e
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

// bsDevice carries the bs topic to a testclient.BootstrapClient: the
// session's operations 1-5 become the CoAP requests the client's store
// answers, and its answers go back as T §8.4 results.
type bsDevice struct {
	t       *testing.T
	bc      *testclient.BootstrapClient
	c       mqtt.Client
	topic   string
	seen    chan *Message // session requests, in order
	results chan *Message
	tok     uint64
}

func newBSDevice(t *testing.T, addr, topic string, bc *testclient.BootstrapClient) *bsDevice {
	t.Helper()
	d := &bsDevice{t: t, bc: bc, topic: topic, seen: make(chan *Message, 64), results: make(chan *Message, 64)}
	d.c = mqtt.NewClient(mqtt.NewClientOptions().AddBroker("tcp://" + addr).SetOrderMatters(false).
		SetClientID("dev" + hex.EncodeToString([]byte{byte(listenerID.Add(1))})))
	if err := wait(d.c.Connect()); err != nil {
		t.Fatal(err)
	}
	if err := wait(d.c.Subscribe(topic, 1, d.on)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.c.Disconnect(0) })
	return d
}

func (d *bsDevice) on(_ mqtt.Client, pm mqtt.Message) {
	m, err := Unmarshal(pm.Payload())
	if err != nil {
		return
	}
	if m.Result != nil {
		select {
		case d.results <- m:
		default:
		}
		return
	}
	op := *m.Operation
	if op < OpBootstrapWrite || op > OpBootstrapFinish {
		return // our own requests
	}
	d.seen <- m
	r := testclient.Request{Path: "/"}
	if m.URI != nil {
		r.Path = *m.URI
	}
	switch op {
	case OpBootstrapWrite:
		r.Code, r.Format, r.Body = codes.PUT, ctOf(m), m.Payload
	case OpBootstrapRead:
		r.Code = codes.GET
		if m.PCT != nil {
			r.Accept = fmtp(lwm2m.ContentFormat(*m.PCT))
		}
	case OpBootstrapDelete:
		r.Code = codes.DELETE
	case OpBootstrapDiscover:
		r.Code, r.Accept = codes.GET, fmtp(lwm2m.FormatLinkFormat)
	case OpBootstrapFinish:
		r.Code, r.Path = codes.POST, "/bs"
	}
	code, cf, body := d.bc.Handle(r)
	out := &Message{Token: m.Token, Result: u64(Result(code)), Payload: body}
	if cf != nil {
		out.CT = u64(uint64(*cf))
	}
	b, _ := Marshal(out)
	_ = wait(d.c.Publish(d.topic, 1, false, b))
}

func (d *bsDevice) call(m *Message) *Message {
	d.t.Helper()
	d.tok++
	m.Token = d.tok
	b, _ := Marshal(m)
	if err := wait(d.c.Publish(d.topic, 1, false, b)); err != nil {
		d.t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case r := <-d.results:
			if r.Token == m.Token {
				return r
			}
		case <-deadline:
			d.t.Fatalf("no result for token %d", m.Token)
			return nil
		}
	}
}

func u16p(v uint16) *uint16 { return &v }

// serverAccount provisions an LwM2M Server over MQTT on the broker at
// addr: /0/1 + /1/1 (ssid 1, binding M) and the /24 instance /0/1/26
// links (T §8.8).
func serverAccount(t *testing.T, addr string) *bootstrap.BootstrapConfig {
	t.Helper()
	ws, err := BootstrapWrites(1, 0, MQTTServerParams{ClientID: "dev01", KeepAlive: 60}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &bootstrap.BootstrapConfig{
		ToDelete: []string{"/0", "/1"},
		Security: map[uint16]bootstrap.SecurityConfig{1: {URI: "mqtt://" + addr, SecurityMode: bootstrap.ModeNoSec, ServerID: u16p(1)}},
		Servers:  map[uint16]bootstrap.ServerConfig{1: {ShortID: 1, Lifetime: 600, Binding: "M"}},
		Writes:   ws,
	}
}

func newBootstrapClient(bsURI string) *testclient.BootstrapClient {
	bc := testclient.NewBootstrap(testclient.Config{Endpoint: ep, Version: "1.2"})
	bc.Set(lwm2m.NewPath(0, 0, 0), lwm2m.String(bsURI)) // the BS account
	bc.Set(lwm2m.NewPath(0, 0, 1), lwm2m.Boolean(true))
	bc.Set(lwm2m.NewPath(0, 0, 2), lwm2m.Integer(3))
	bc.Set(lwm2m.NewPath(3, 0, 0), lwm2m.String("Open Mobile Alliance"))
	return bc
}

// Client-Initiated Bootstrap over MQTT (T §8.3.1): Bootstrap-Request {0,
// token, pct} on tenant-a/lwm2m/bs/{ep} gets 204, then the session sends
// Bootstrap-Discover (4, uri "/"), -Delete (3) of /0 and /1, -Write (1,
// uri, ct = pct, payload) of /0/1, /1/1, /24/0 and /0/1/26, -Read (2, uri
// /1, pct) and -Finish (5, token only), each answered with its Tbl 8.5-1
// result. The client then registers with the LwM2M Server over MQTT using
// the account it was given.
// Proves: MQTT-06, MQTT-03, MQTT-10
func TestBootstrapOverMQTT(t *testing.T) {
	e := newBSEnv(t, "", "tenant-a", bootstrap.Config{})
	if got := e.b.Topic(ep); got != "tenant-a/lwm2m/bs/"+ep {
		t.Fatalf("topic %q", got)
	}
	cfg := serverAccount(t, e.addr)
	cfg.Discover, cfg.Read = true, []string{"/1"}
	if err := e.configs.Put(ep, cfg); err != nil {
		t.Fatal(err)
	}
	bc := newBootstrapClient("mqtt://" + e.addr)
	d := newBSDevice(t, e.addr, e.b.Topic(ep), bc)

	cbor := uint64(lwm2m.FormatSenMLCBOR)
	r, err := bc.BootstrapRequestWith(func() (*testclient.Response, error) {
		r := d.call(&Message{Operation: u64(OpBootstrapRequest), PCT: u64(cbor)})
		return &testclient.Response{Code: Code(*r.Result)}, nil
	})
	if err != nil || r.Code != codes.Changed {
		t.Fatalf("Bootstrap-Request: %+v, %v (want 204)", r, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c, err := bc.WaitFinish(ctx); err != nil || c != codes.Changed {
		t.Fatalf("Finish answered %v, %v\n%s", c, err, bc.Dump())
	}
	res := e.result(t)
	if res.Err != nil || res.Endpoint != ep || res.Format != lwm2m.FormatSenMLCBOR || len(res.Reads["/1"]) == 0 {
		t.Fatalf("session %+v", res)
	}
	type step struct {
		op       uint64
		uri      string
		ct, pct  uint64
		resultOK uint64
	}
	want := []step{
		{OpBootstrapDiscover, "/", 0, 0, 205},
		{OpBootstrapDelete, "/0", 0, 0, 202},
		{OpBootstrapDelete, "/1", 0, 0, 202},
		{OpBootstrapWrite, "/0/1", cbor, 0, 204},
		{OpBootstrapWrite, "/1/1", cbor, 0, 204},
		{OpBootstrapWrite, "/24/0", cbor, 0, 204},
		{OpBootstrapWrite, "/0/1/26", cbor, 0, 204},
		{OpBootstrapRead, "/1", 0, cbor, 205},
		{OpBootstrapFinish, "", 0, 0, 204},
	}
	for i, w := range want {
		m := <-d.seen
		uri, ct, pct := "", uint64(0), uint64(0)
		if m.URI != nil {
			uri = *m.URI
		}
		if m.CT != nil {
			ct = *m.CT
		}
		if m.PCT != nil {
			pct = *m.PCT
		}
		if *m.Operation != w.op || uri != w.uri || ct != w.ct || pct != w.pct ||
			(w.op == OpBootstrapWrite) != (len(m.Payload) > 0) || m.Result != nil {
			t.Fatalf("step %d: %+v, want %+v", i, m, w)
		}
		if got := Result(res.Steps[i].Result); got != w.resultOK {
			t.Errorf("step %d result %d, want %d", i, got, w.resultOK)
		}
	}

	// Register with the provisioned LwM2M Server Account over MQTT.
	uri, _, _, ok := bc.ServerAccount()
	if !ok {
		t.Fatalf("no Server Account\n%s", bc.Dump())
	}
	u, _ := url.Parse(uri)
	lt, _ := bc.Get(lwm2m.NewPath(1, 1, 1))
	if id, _ := bc.Get(lwm2m.NewPath(24, 0, 6)); u.Scheme != "mqtt" || id.Str != "dev01" {
		t.Fatalf("account %s, /24/0/6 %q", uri, id.Str)
	}
	srv := server.New(server.Config{})
	t.Cleanup(func() { _ = srv.Close() })
	sb, err := New(srv, Config{Broker: uri, Prefix: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sb.Close)
	c := newClient(t, u.Host, sb.Topic(ep))
	if r := c.call(&Message{Operation: u64(OpRegister), Lifetime: u64(uint64(lt.Int)), Version: str("1.2"), B: str("M"),
		Payload: []byte("</1/1>,</3/0>")}); *r.Result != 201 {
		t.Fatalf("Register: %d", *r.Result)
	}
	reg, ok := srv.Store().ByEndpoint(ep)
	if !ok || reg.Lifetime != 600*time.Second || reg.Binding != "M" {
		t.Fatalf("registration %+v", reg)
	}
}

// The T §8.3.1 example {1: 0, 2: 42}, published as is to
// tenant-a/lwm2m/bs/{ep}, is a Bootstrap-Request: it gets {18: 204, 2:
// 42} and, with no pct, the session writes TLV.
// Proves: MQTT-06
func TestBootstrapSpecVector(t *testing.T) {
	vs, err := vectors.Load("spec-examples")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors.Vector
	for _, x := range vs {
		if x.ID == "spec-mqtt-bootstrap-request" {
			v = x
		}
	}
	b, _ := hex.DecodeString(*v.BytesHex)
	e := newBSEnv(t, "", "tenant-a", bootstrap.Config{})
	cfg := serverAccount(t, e.addr)
	cfg.ToDelete = nil
	if err := e.configs.Put(ep, cfg); err != nil {
		t.Fatal(err)
	}
	c := newRawClient(t, e.addr, "tenant-a/lwm2m/bs/b1cccdea-22ca-4448-bcf7-d07317ee0361")
	c.send(b)
	if r, _ := c.next(nil, 3*time.Second, resultFor(42)); r == nil || *r.Result != 204 || r.CT != nil || r.Payload != nil {
		t.Fatalf("result %+v", r)
	}
	w, _ := c.next(nil, 3*time.Second, func(m *Message) bool { return m.Operation != nil && *m.Operation == OpBootstrapWrite })
	if w == nil || *w.URI != "/0/1" || *w.CT != uint64(lwm2m.FormatTLV) {
		t.Fatalf("first write %+v", w)
	}
}

// Bootstrap-Request results (T Tbl 8.5-1): 400 for an unknown endpoint or
// an ep key that differs from ENDPOINT, 415 for a pct the BS cannot
// write; other operations on bs are 501 (Tbl 8.5-5). A client's 406 to
// Finish fails the session.
// Proves: MQTT-06, MQTT-10
func TestBootstrapRequestErrors(t *testing.T) {
	e := newBSEnv(t, "", "", bootstrap.Config{})
	if err := e.configs.Put(ep, serverAccount(t, e.addr)); err != nil {
		t.Fatal(err)
	}
	other := newBSDevice(t, e.addr, e.b.Topic("unknown"), newBootstrapClient(""))
	if r := other.call(&Message{Operation: u64(OpBootstrapRequest)}); *r.Result != 400 {
		t.Errorf("unknown endpoint: %d, want 400", *r.Result)
	}
	bc := newBootstrapClient("")
	d := newBSDevice(t, e.addr, e.b.Topic(ep), bc)
	for _, tc := range []struct {
		name string
		m    *Message
		want uint64
	}{
		{"pct text/plain", &Message{Operation: u64(OpBootstrapRequest), PCT: u64(0)}, 415},
		{"pct not a format", &Message{Operation: u64(OpBootstrapRequest), PCT: u64(1 << 20)}, 400},
		{"ep differs from ENDPOINT", &Message{Operation: u64(OpBootstrapRequest), EP: str("other")}, 400},
		{"Register on bs", &Message{Operation: u64(7)}, 501},
		{"Send on bs", &Message{Operation: u64(OpSend)}, 501},
	} {
		if r := d.call(tc.m); *r.Result != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, *r.Result, tc.want)
		}
	}
	select {
	case r := <-e.results:
		t.Fatalf("a refused request started a session: %+v", r)
	default:
	}
	// An inconsistent configuration: the client answers Finish with 406.
	cfg := serverAccount(t, e.addr)
	cfg.Servers = nil
	cfg.Writes = nil
	if err := e.configs.Put(ep, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := bc.BootstrapRequestWith(func() (*testclient.Response, error) {
		return &testclient.Response{Code: Code(*d.call(&Message{Operation: u64(OpBootstrapRequest)}).Result)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if r := e.result(t); !errors.Is(r.Err, bootstrap.ErrFinishRejected) || Result(r.Steps[len(r.Steps)-1].Result) != 406 {
		t.Fatalf("session %+v", r)
	}
}

// Bootstrap-Pack-Request {6, token, ?payload = BS-account instances}
// answers {18: 205, 19: ct, 7: Pack}; pct selects the format. Results
// per T Tbl 8.5-1: 400 unknown endpoint or bad BS-account list, 405
// refused, 406 unsupported format, 501 not supported.
// Proves: MQTT-06, MQTT-10
func TestBootstrapPackOverMQTT(t *testing.T) {
	e := newBSEnv(t, "", "tenant-a", bootstrap.Config{})
	cfg := serverAccount(t, e.addr)
	cfg.Writes = nil // a Pack holds whole objects only (BS-15)
	if err := e.configs.Put(ep, cfg); err != nil {
		t.Fatal(err)
	}
	refusing := serverAccount(t, e.addr)
	refusing.RefusePack = true
	if err := e.configs.Put("refuses", refusing); err != nil {
		t.Fatal(err)
	}
	d := newBSDevice(t, e.addr, e.b.Topic(ep), newBootstrapClient(""))
	link := uint64(lwm2m.FormatLinkFormat)
	for _, f := range []lwm2m.ContentFormat{lwm2m.FormatSenMLCBOR, lwm2m.FormatSenMLJSON, lwm2m.FormatLwM2MCBOR} {
		r := d.call(&Message{Operation: u64(OpBootstrapPack), PCT: u64(uint64(f)), CT: &link, Payload: []byte("</0/0>")})
		if *r.Result != 205 || r.CT == nil || *r.CT != uint64(f) {
			t.Fatalf("Pack in %v: %+v", f, r)
		}
		cd, _ := codec.For(f)
		s := model.Default().Schema(map[uint16]model.Version{0: {Major: 1, Minor: 2}, 1: {Major: 1, Minor: 2}})
		nodes, err := cd.Decode(lwm2m.Root, r.Payload, s)
		if err != nil {
			t.Fatal(err)
		}
		var uri string
		for _, n := range nodes {
			if n.Path == lwm2m.NewPath(0, 1, 0) {
				uri = n.Value.Str
			}
		}
		if uri != "mqtt://"+e.addr {
			t.Fatalf("Pack %v: /0/1/0 = %q", nodes, uri)
		}
		if res := e.result(t); !res.Pack || res.Format != f || res.Err != nil {
			t.Fatalf("pack result %+v", res)
		}
	}
	if r := d.call(&Message{Operation: u64(OpBootstrapPack)}); *r.Result != 205 || *r.CT != uint64(lwm2m.FormatSenMLCBOR) {
		t.Fatalf("Pack without pct or payload: %+v", r)
	}
	e.result(t)
	for _, tc := range []struct {
		name  string
		topic string
		m     *Message
		want  uint64
	}{
		{"unknown endpoint", "nobody", &Message{}, 400},
		{"payload not CoRE links", ep, &Message{CT: u64(0), Payload: []byte("</0/0>")}, 400},
		{"not a BS-account instance", ep, &Message{Payload: []byte("</3/0>")}, 400},
		{"refused", "refuses", &Message{}, 405},
		{"pct text/plain", ep, &Message{PCT: u64(0)}, 406},
		{"pct not a format", ep, &Message{PCT: u64(1 << 20)}, 406},
	} {
		dd := d
		if tc.topic != ep {
			dd = newBSDevice(t, e.addr, e.b.Topic(tc.topic), newBootstrapClient(""))
		}
		tc.m.Operation = u64(OpBootstrapPack)
		if r := dd.call(tc.m); *r.Result != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, *r.Result, tc.want)
		}
	}

	off := newBSEnv(t, e.addr, "tenant-b", bootstrap.Config{DisablePack: true}, func(c *Config) { c.ClientID = "bs2" })
	if err := off.configs.Put(ep, cfg); err != nil {
		t.Fatal(err)
	}
	od := newBSDevice(t, e.addr, off.b.Topic(ep), newBootstrapClient(""))
	if r := od.call(&Message{Operation: u64(OpBootstrapPack)}); *r.Result != 501 {
		t.Errorf("Pack not supported: %d, want 501", *r.Result)
	}
}

// With a /23 key for the endpoint (T §8.8) every bs message is a
// COSE_Encrypt0 in an Outer_Wrapper (T §8.6): an unprotected
// Bootstrap-Request gets 400 and an unprotected Pack-Request 401, in
// clear; a protected one is answered and the session's requests are
// protected.
// Proves: MQTT-06, MQTT-11
func TestBootstrapCOSE(t *testing.T) {
	e := newBSEnv(t, "", "", bootstrap.Config{}, func(c *Config) {
		c.COSE = func(e string) *COSEKey {
			if e == ep {
				return testKey
			}
			return nil
		}
	})
	cfg := serverAccount(t, e.addr)
	cfg.Writes = nil
	if err := e.configs.Put(ep, cfg); err != nil {
		t.Fatal(err)
	}
	c := newRawClient(t, e.addr, e.b.Topic(ep))
	for _, tc := range []struct {
		op   uint64
		want uint64
	}{{OpBootstrapRequest, 400}, {OpBootstrapPack, 401}} {
		b, _ := Marshal(&Message{Operation: u64(tc.op), Token: tc.op + 1})
		c.send(b)
		if r, _ := c.next(nil, 3*time.Second, resultFor(tc.op+1)); r == nil || *r.Result != tc.want {
			t.Fatalf("unprotected op %d: %+v, want %d", tc.op, r, tc.want)
		}
	}
	c.send(c.sealed(&Message{Operation: u64(OpBootstrapRequest), Token: 9}))
	if r, raw := c.next(testKey, 3*time.Second, resultFor(9)); r == nil || *r.Result != 204 {
		t.Fatalf("protected Bootstrap-Request: %+v", r)
	} else {
		isProtected(t, raw, "")
	}
	for {
		m, raw := c.next(testKey, 3*time.Second, func(m *Message) bool { return m.Operation != nil && *m.Operation != OpBootstrapRequest })
		if m == nil {
			t.Fatal("session stalled")
		}
		isProtected(t, raw, "")
		result := map[uint64]uint64{OpBootstrapDelete: 202, OpBootstrapWrite: 204, OpBootstrapFinish: 204}[*m.Operation]
		c.send(c.sealed(&Message{Token: m.Token, Result: u64(result)}))
		if *m.Operation == OpBootstrapFinish {
			break
		}
	}
	if r := e.result(t); r.Err != nil || len(r.Steps) != 5 {
		t.Fatalf("session %+v", r)
	}
}
