package mqtt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/codec/senml"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fxamacker/cbor/v2"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

const ep = "b1cccdea-22ca-4448-bcf7-d07317ee0361"

var listenerID atomic.Int32

// startBroker runs an in-process MQTT broker and returns its address.
func startBroker(t *testing.T, tlsCfg *tls.Config, hooks ...mochi.Hook) string {
	t.Helper()
	b := mochi.New(&mochi.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for _, h := range append([]mochi.Hook{new(allowAll)}, hooks...) {
		if err := b.AddHook(h, nil); err != nil {
			t.Fatal(err)
		}
	}
	l := listeners.NewTCP(listeners.Config{ID: "t" + string(rune('a'+listenerID.Add(1))), Address: "127.0.0.1:0", TLSConfig: tlsCfg})
	if err := b.AddListener(l); err != nil {
		t.Fatal(err)
	}
	go func() { _ = b.Serve() }()
	t.Cleanup(func() {
		// mochi's Close read-locks its client map twice (GetByListener ->
		// Len), so a client disconnecting meanwhile deadlocks it: let the
		// test's clients drain first.
		for end := time.Now().Add(2 * time.Second); b.Clients.Len() > 0 && time.Now().Before(end); {
			time.Sleep(5 * time.Millisecond)
		}
		_ = b.Close()
	})
	return l.Address()
}

// allowAll lets every MQTT client connect and use every topic.
type allowAll struct{ mochi.HookBase }

func (*allowAll) ID() string { return "allow-all" }
func (*allowAll) Provides(b byte) bool {
	return b == mochi.OnConnectAuthenticate || b == mochi.OnACLCheck
}
func (*allowAll) OnConnectAuthenticate(*mochi.Client, packets.Packet) bool { return true }
func (*allowAll) OnACLCheck(*mochi.Client, string, bool) bool              { return true }

// env is a server, its MQTT binding and a broker.
type env struct {
	srv    *server.Server
	b      *Binding
	addr   string
	events chan server.Event
}

func newEnv(t *testing.T, prefix string, opts ...func(*Config)) *env {
	t.Helper()
	e := &env{events: make(chan server.Event, 64), addr: startBroker(t, nil)}
	e.srv = server.New(server.Config{OnEvent: func(ev server.Event) {
		select {
		case e.events <- ev:
		default:
		}
	}, RequestTimeout: 3 * time.Second})
	t.Cleanup(func() { _ = e.srv.Close() })
	var err error
	cfg := Config{Broker: "mqtt://" + e.addr, Prefix: prefix}
	for _, o := range opts {
		o(&cfg)
	}
	e.b, err = New(e.srv, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.b.Close)
	return e
}

func (e *env) event(t *testing.T, match func(server.Event) bool) server.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-e.events:
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for event")
		}
	}
}

// client is a scripted LwM2M-over-MQTT client.
type client struct {
	t        *testing.T
	c        paho.Client
	topic    string
	requests chan *Message // operations 9-22 from the Server
	results  chan *Message
	tok      uint64
}

func newClient(t *testing.T, addr, topic string) *client {
	t.Helper()
	c := &client{t: t, topic: topic, requests: make(chan *Message, 16), results: make(chan *Message, 256), tok: 1000}
	c.c = paho.NewClient(paho.NewClientOptions().AddBroker("tcp://" + addr).SetClientID(topic[len(topic)-8:] + hex.EncodeToString([]byte{byte(listenerID.Add(1))})))
	if err := wait(c.c.Connect()); err != nil {
		t.Fatal(err)
	}
	if err := wait(c.c.Subscribe(topic, 1, func(_ paho.Client, pm paho.Message) {
		m, err := Unmarshal(pm.Payload())
		if err != nil {
			return
		}
		if m.Result != nil {
			select { // also receives its own replies (A-7); never block paho
			case c.results <- m:
			default:
			}
		} else if op := *m.Operation; op >= OpRead && op <= OpCancelObserve {
			c.requests <- m
		}
	})); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.c.Disconnect(0) })
	return c
}

func (c *client) publish(m *Message) {
	c.t.Helper()
	b, err := Marshal(m)
	if err != nil {
		c.t.Fatal(err)
	}
	c.raw(b)
}

func (c *client) raw(b []byte) {
	c.t.Helper()
	if err := wait(c.c.Publish(c.topic, 1, false, b)); err != nil {
		c.t.Fatal(err)
	}
}

// call publishes a request and returns the result carrying its token.
func (c *client) call(m *Message) *Message {
	c.t.Helper()
	c.tok++
	m.Token = c.tok
	c.publish(m)
	return c.result(m.Token)
}

func (c *client) result(tok uint64) *Message {
	c.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case r := <-c.results:
			if r.Token == tok {
				return r
			}
		case <-deadline:
			c.t.Fatalf("no result for token %d", tok)
			return nil
		}
	}
}

func (c *client) next() *Message {
	c.t.Helper()
	select {
	case r := <-c.requests:
		return r
	case <-time.After(3 * time.Second):
		c.t.Fatal("no request from the server")
		return nil
	}
}

func (c *client) reply(req *Message, result uint64, ct *uint64, payload []byte) {
	c.publish(&Message{Result: u64(result), Token: req.Token, CT: ct, Payload: payload})
}

func (c *client) register(t *testing.T) {
	t.Helper()
	r := c.call(&Message{Operation: u64(OpRegister), Lifetime: u64(3600), Version: str("1.2"), B: str("M"),
		Payload: []byte("</1/0>,</3/0>,</4/0>,</5/0>")})
	if *r.Result != 201 {
		t.Fatalf("Register result %d, want 201", *r.Result)
	}
}

// Proves: MQTT-04, MQTT-05
func TestSpecVectors(t *testing.T) {
	vs, err := vectors.Load("spec-examples")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string][]byte{}
	for _, v := range vs {
		if v.BytesHex != nil {
			byID[v.ID], _ = hex.DecodeString(*v.BytesHex)
		}
	}
	// The Server's Read publication, T §8.3.3.
	got, _ := Marshal(&Message{Operation: u64(OpRead), Token: 56, URI: str("/3/0/0")})
	if hex.EncodeToString(got) != hex.EncodeToString(byID["spec-mqtt-read"]) {
		t.Errorf("Read = %x, want %x", got, byID["spec-mqtt-read"])
	}
	// Generic response with a text payload (A-7: accepted as bstr or tstr).
	r, err := Unmarshal(byID["spec-mqtt-read-response"])
	if err != nil || *r.Result != 205 || r.Token != 56 || *r.CT != 0 || string(r.Payload) != "Open Mobile Alliance" {
		t.Errorf("read response = %+v, %v", r, err)
	}
	if Code(*r.Result) != codes.Content {
		t.Errorf("205 -> %v", Code(*r.Result))
	}
	b, err := Unmarshal(byID["spec-mqtt-bootstrap-request"])
	if err != nil || *b.Operation != OpBootstrapRequest || b.Token != 42 {
		t.Errorf("bootstrap request = %+v, %v", b, err)
	}
	// Errata: the "Send" example uses operation 23, which is Notify.
	s, err := Unmarshal(byID["spec-mqtt-send"])
	if err != nil || *s.Operation != OpNotify || *s.CT != 112 {
		t.Errorf("send example = %+v, %v", s, err)
	}
	if _, err := Unmarshal([]byte{0xa1, 0x02, 0x01}); err == nil {
		t.Error("a message with neither operation nor result was accepted")
	}
}

// Proves: MQTT-02, MQTT-03, MQTT-07, MQTT-10, REG-05
func TestRegistrationInterface(t *testing.T) {
	e := newEnv(t, "tenant-a")
	if got := e.b.Topic(ep); got != "tenant-a/lwm2m/rd/"+ep {
		t.Fatalf("topic %q", got)
	}
	c := newClient(t, e.addr, "tenant-a/lwm2m/rd/"+ep)
	if r := c.call(&Message{Operation: u64(OpRegister), Lifetime: u64(3600), Version: str("1.2"), B: str("M"), SMS: str("491711234567"),
		Payload: []byte("</1/0>,</3/0>,</4/0>,</5/0>")}); *r.Result != 201 {
		t.Fatalf("Register result %d, want 201", *r.Result)
	}
	reg := e.event(t, func(ev server.Event) bool { _, ok := ev.(server.Registered); return ok }).(server.Registered).Registration
	if reg.Endpoint != ep || reg.Version != "1.2" || reg.Lifetime != time.Hour || reg.Binding != "M" || reg.SMS != "491711234567" || !reg.HasObject(5) {
		t.Fatalf("registration %+v", reg)
	}
	if r := c.call(&Message{Operation: u64(OpUpdate), Lifetime: u64(60), Payload: []byte("</1/0>,</3/0>")}); *r.Result != 204 {
		t.Fatalf("Update result %d, want 204", *r.Result)
	}
	up := e.event(t, func(ev server.Event) bool { _, ok := ev.(server.Updated); return ok }).(server.Updated).Registration
	if up.Lifetime != time.Minute || up.HasObject(5) {
		t.Fatalf("updated %+v", up)
	}
	if r := c.call(&Message{Operation: u64(OpDeregister)}); *r.Result != 202 {
		t.Fatalf("De-register result %d, want 202", *r.Result)
	}
	if _, ok := e.srv.Store().ByEndpoint(ep); ok {
		t.Fatal("still registered")
	}
	if r := c.call(&Message{Operation: u64(OpUpdate)}); *r.Result != 404 {
		t.Fatalf("Update after De-register: %d, want 404", *r.Result)
	}
	if r := c.call(&Message{Operation: u64(OpDeregister)}); *r.Result != 404 {
		t.Fatalf("De-register after De-register: %d, want 404", *r.Result)
	}
}

// Proves: MQTT-02, MQTT-10
func TestRegisterErrors(t *testing.T) {
	e := newEnv(t, "")
	c := newClient(t, e.addr, "lwm2m/rd/"+ep)
	for _, tc := range []struct {
		name string
		m    *Message
		want uint64
	}{
		{"ep differs from ENDPOINT", &Message{Operation: u64(OpRegister), EP: str("other"), Lifetime: u64(60), Version: str("1.2"), Payload: []byte("</3/0>")}, 400},
		{"no lifetime", &Message{Operation: u64(OpRegister), Version: str("1.2"), Payload: []byte("</3/0>")}, 400},
		{"unknown version", &Message{Operation: u64(OpRegister), Lifetime: u64(60), Version: str("9.9"), Payload: []byte("</3/0>")}, 412},
		{"1.2 without object list", &Message{Operation: u64(OpRegister), Lifetime: u64(60), Version: str("1.2")}, 409},
		{"bootstrap op on rd", &Message{Operation: u64(OpBootstrapRequest)}, 400},
		{"unknown op", &Message{Operation: u64(30)}, 501},
	} {
		if r := c.call(tc.m); *r.Result != tc.want {
			t.Errorf("%s: result %d, want %d", tc.name, *r.Result, tc.want)
		}
	}
	// The matching ep key is accepted, and an unprotected Outer_Wrapper
	// (T §8.6, msg-wrapper nil) is unwrapped.
	inner, _ := Marshal(&Message{Operation: u64(OpRegister), Token: 7, EP: str(ep), Lifetime: u64(60), Version: str("1.1"), Payload: []byte("</3/0>")})
	c.raw(append([]byte{0xa2, 0x01, 0xf6, 0x02}, inner...))
	if r := c.result(7); *r.Result != 201 {
		t.Fatalf("wrapped Register: %d", *r.Result)
	}
}

// Proves: MQTT-01, MQTT-08, MQTT-10
func TestDeviceManagement(t *testing.T) {
	e := newEnv(t, "tenant-a")
	c := newClient(t, e.addr, e.b.Topic(ep))
	c.register(t)
	ctx := context.Background()
	text, cbor := u64(0), u64(uint64(lwm2m.FormatSenMLCBOR))

	type result struct {
		r   *server.Response
		err error
	}
	do := func(f func() (*server.Response, error), check func(*Message), result_ uint64, ct *uint64, payload []byte) *server.Response {
		t.Helper()
		ch := make(chan result, 1)
		go func() { r, err := f(); ch <- result{r, err} }()
		req := c.next()
		check(req)
		c.reply(req, result_, ct, payload)
		res := <-ch
		if res.err != nil {
			t.Fatal(res.err)
		}
		if want := Code(result_); res.r.Code != want {
			t.Fatalf("code %v, want %v", res.r.Code, want)
		}
		return res.r
	}
	want := func(op uint64, uri string) func(*Message) {
		return func(m *Message) {
			t.Helper()
			if *m.Operation != op || (uri != "" && (m.URI == nil || *m.URI != uri)) || (uri == "" && m.URI != nil) {
				t.Fatalf("request %+v, want op %d uri %q", m, op, uri)
			}
		}
	}

	r := do(func() (*server.Response, error) {
		return e.srv.Read(ctx, ep, lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{})
	},
		want(OpRead, "/3/0/0"), 205, text, []byte("Open Mobile Alliance"))
	if len(r.Nodes) != 1 || r.Nodes[0].Value.Str != "Open Mobile Alliance" {
		t.Errorf("Read nodes %v", r.Nodes)
	}
	depth := 1
	do(func() (*server.Response, error) { return e.srv.Discover(ctx, ep, lwm2m.MustParsePath("/3"), &depth) },
		func(m *Message) {
			want(OpDiscover, "/3")(m)
			if m.Depth == nil || *m.Depth != 1 {
				t.Errorf("depth %v", m.Depth)
			}
		}, 205, u64(40), []byte("</3/0>"))
	val := []lwm2m.Node{{Kind: lwm2m.KindValue, Path: lwm2m.MustParsePath("/1/0/1"), Value: lwm2m.Integer(60)}}
	do(func() (*server.Response, error) {
		return e.srv.Write(ctx, ep, lwm2m.MustParsePath("/1/0/1"), val, server.WriteOptions{})
	}, func(m *Message) {
		want(OpWriteReplace, "/1/0/1")(m)
		if *m.CT != 0 || string(m.Payload) != "60" {
			t.Errorf("write %+v", m)
		}
	}, 204, nil, nil)
	do(func() (*server.Response, error) {
		return e.srv.Write(ctx, ep, lwm2m.MustParsePath("/1/0"), val, server.WriteOptions{Mode: server.PartialUpdate, Format: fmtp(lwm2m.FormatSenMLCBOR)})
	}, func(m *Message) {
		want(OpWritePartial, "/1/0")(m)
		if *m.CT != *cbor {
			t.Errorf("partial update ct %d", *m.CT)
		}
	}, 204, nil, nil)
	do(func() (*server.Response, error) {
		return e.srv.WriteAttributes(ctx, ep, lwm2m.MustParsePath("/3/0/9"), []string{"pmin=10", "pmax=60", "gt=1.5", "st=0.5", "epmin=1", "epmax=2", "edge=1", "hqmax=4"})
	}, func(m *Message) {
		want(OpWriteAttributes, "/3/0/9")(m)
		if *m.Pmin != 10 || *m.Pmax != 60 || *m.Gt != 1.5 || *m.St != 0.5 || *m.Epmin != 1 || *m.Epmax != 2 || *m.Edge != 1 || *m.Hqmax != 4 {
			t.Errorf("attributes %+v", m)
		}
	}, 204, nil, nil)
	do(func() (*server.Response, error) {
		return e.srv.Execute(ctx, ep, lwm2m.MustParsePath("/3/0/4"), "0='x'")
	},
		func(m *Message) {
			want(OpExecute, "/3/0/4")(m)
			if string(m.Payload) != "0='x'" {
				t.Errorf("execute args %q", m.Payload)
			}
		}, 204, nil, nil)
	inst := []lwm2m.Node{{Kind: lwm2m.KindValue, Path: lwm2m.MustParsePath("/5/1/1"), Value: lwm2m.String("coap://x")}}
	do(func() (*server.Response, error) {
		return e.srv.Create(ctx, ep, lwm2m.MustParsePath("/5"), inst, fmtp(lwm2m.FormatSenMLCBOR))
	},
		func(m *Message) {
			want(OpCreate, "/5")(m)
			if *m.CT != *cbor || len(m.Payload) == 0 {
				t.Errorf("create %+v", m)
			}
		}, 201, nil, nil)
	do(func() (*server.Response, error) { return e.srv.Delete(ctx, ep, lwm2m.MustParsePath("/5/1")) },
		want(OpDelete, "/5/1"), 202, nil, nil)
	do(func() (*server.Response, error) {
		return e.srv.ReadComposite(ctx, ep, []lwm2m.Path{lwm2m.MustParsePath("/3/0/0"), lwm2m.MustParsePath("/1/0/1")}, server.CompositeOptions{Format: fmtp(lwm2m.FormatSenMLJSON)})
	}, func(m *Message) {
		want(OpReadComposite, "")(m)
		ps, err := senml.ETCHCBOR.DecodePaths(m.Paths)
		if err != nil || len(ps) != 2 || ps[1] != lwm2m.MustParsePath("/1/0/1") {
			t.Errorf("paths %v %v", ps, err)
		}
	}, 205, nil, nil)
	do(func() (*server.Response, error) { return e.srv.WriteComposite(ctx, ep, val, nil) },
		func(m *Message) {
			want(OpWriteComposite, "")(m)
			if *m.CT != *cbor {
				t.Errorf("write-composite ct %d", *m.CT)
			}
		}, 204, nil, nil)
	// Error results map back to CoAP-numbered codes.
	for _, res := range []uint64{400, 401, 404, 405, 406, 408, 413, 415, 500, 501, 503} {
		do(func() (*server.Response, error) {
			return e.srv.Read(ctx, ep, lwm2m.MustParsePath("/4/0"), server.ReadOptions{})
		},
			want(OpRead, "/4/0"), res, nil, nil)
	}
}

func fmtp(f lwm2m.ContentFormat) *lwm2m.ContentFormat { return &f }

// Proves: MQTT-04
func TestNoInventedKeys(t *testing.T) {
	e := newEnv(t, "")
	c := newClient(t, e.addr, e.b.Topic(ep))
	c.register(t)
	ctx := context.Background()
	for _, q := range [][]string{{"lt=5"}, {"con=1"}, {"pmin"}} {
		if _, err := e.srv.WriteAttributes(ctx, ep, lwm2m.MustParsePath("/3/0/9"), q); !errors.Is(err, errUnsupported) {
			t.Errorf("Write-Attributes %v: %v, want refusal", q, err)
		}
	}
	if _, _, err := e.srv.Observe(ctx, ep, lwm2m.MustParsePath("/3/0/9"), server.ObserveOptions{Query: []string{"pmin=1"}}); !errors.Is(err, errUnsupported) {
		t.Errorf("Observe with attributes: %v", err)
	}
}

// Proves: MQTT-09
func TestInformationReporting(t *testing.T) {
	e := newEnv(t, "tenant-a")
	c := newClient(t, e.addr, e.b.Topic(ep))
	c.register(t)
	ctx := context.Background()
	text := u64(0)

	type obsResult struct {
		ob  *server.Observation
		err error
	}
	ch := make(chan obsResult, 1)
	go func() {
		ob, _, err := e.srv.Observe(ctx, ep, lwm2m.MustParsePath("/3/0/9"), server.ObserveOptions{})
		ch <- obsResult{ob, err}
	}()
	req := c.next()
	if *req.Operation != OpObserve || *req.URI != "/3/0/9" {
		t.Fatalf("observe %+v", req)
	}
	c.reply(req, 205, text, []byte("80"))
	res := <-ch
	if res.err != nil || res.ob == nil {
		t.Fatalf("observe: %v", res.err)
	}
	obsTok := req.Token
	c.publish(&Message{Operation: u64(OpNotify), Token: obsTok, CT: text, Payload: []byte("75")})
	n := e.event(t, func(ev server.Event) bool { _, ok := ev.(server.Notification); return ok }).(server.Notification)
	if n.Observation.ID != res.ob.ID || n.Response.Nodes[0].Value.Str != "75" {
		t.Fatalf("notification %+v", n.Response)
	}
	// A Notify for an unknown observation is answered with Cancel-Observe.
	c.publish(&Message{Operation: u64(OpNotify), Token: 999, CT: text, Payload: []byte("1")})
	if r := c.next(); *r.Operation != OpCancelObserve || r.Token != 999 {
		t.Fatalf("unknown notify answered with %+v", r)
	}
	// Active cancel carries the observation's token and nothing else.
	done := make(chan error, 1)
	go func() { _, err := e.srv.CancelObservation(ctx, res.ob, true); done <- err }()
	r := c.next()
	if *r.Operation != OpCancelObserve || r.Token != obsTok || r.URI != nil {
		t.Fatalf("cancel %+v", r)
	}
	c.reply(r, 205, nil, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Observe-Composite carries SenML-ETCH CBOR paths.
	go func() {
		ob, _, err := e.srv.ObserveComposite(ctx, ep, []lwm2m.Path{lwm2m.MustParsePath("/3/0/9"), lwm2m.MustParsePath("/3/0/20")}, server.CompositeOptions{})
		ch <- obsResult{ob, err}
	}()
	req = c.next()
	if ps, err := senml.ETCHCBOR.DecodePaths(req.Paths); *req.Operation != OpObserveComposite || err != nil || len(ps) != 2 {
		t.Fatalf("observe-composite %+v %v", req, err)
	}
	c.reply(req, 205, nil, nil)
	if res := <-ch; res.err != nil {
		t.Fatal(res.err)
	}

	// Send (24), with the payload of the T §8.3.4 example.
	payload, _ := hex.DecodeString("82a321652f332f302f006139020fa2006232300204")
	if r := c.call(&Message{Operation: u64(OpSend), CT: u64(112), Payload: payload}); *r.Result != 204 {
		t.Fatalf("Send result %d, want 204", *r.Result)
	}
	s := e.event(t, func(ev server.Event) bool { _, ok := ev.(server.SendReceived); return ok }).(server.SendReceived)
	if len(s.Nodes) != 2 || s.Nodes[0].Path != lwm2m.MustParsePath("/3/0/9") {
		t.Fatalf("send nodes %v", s.Nodes)
	}
	if r := c.call(&Message{Operation: u64(OpSend), Payload: payload}); *r.Result != 400 {
		t.Errorf("Send without ct: %d, want 400", *r.Result)
	}
	if r := c.call(&Message{Operation: u64(OpSend), CT: u64(110), Payload: []byte(`[{"n":"/9/0/0","v":1}]`)}); *r.Result != 404 {
		t.Errorf("Send of an unregistered object: %d, want 404", *r.Result)
	}
}

// Proves: MQTT-13
func TestPrefixIsolation(t *testing.T) {
	e := newEnv(t, "tenant-a")
	for _, topic := range []string{"tenant-b/lwm2m/rd/" + ep, "lwm2m/rd/" + ep, "tenant-a/lwm2m/bs/" + ep} {
		c := newClient(t, e.addr, topic)
		c.publish(&Message{Operation: u64(OpRegister), Token: 1, Lifetime: u64(60), Version: str("1.2"), Payload: []byte("</3/0>")})
		select {
		case r := <-c.results:
			t.Errorf("%s: server answered %+v", topic, r)
		case <-time.After(200 * time.Millisecond):
		}
	}
	if _, ok := e.srv.Store().ByEndpoint(ep); ok {
		t.Fatal("registered from another tenant's topic")
	}
}

// Proves: MQTT-01
func TestBrokerScheme(t *testing.T) {
	srv := server.New(server.Config{})
	defer srv.Close()
	for _, u := range []string{"tcp://127.0.0.1:1", "ssl://127.0.0.1:1", "coap://127.0.0.1:1"} {
		if _, err := New(srv, Config{Broker: u}); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	// mqtts:// over TLS.
	cert, pool := selfSigned(t)
	addr := startBroker(t, &tls.Config{Certificates: []tls.Certificate{cert}})
	b, err := New(srv, Config{Broker: "mqtts://" + addr, TLS: &tls.Config{RootCAs: pool, ServerName: "localhost"}})
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if (&peer{b: b, ep: ep}).Binding() != "M" {
		t.Error("binding letter")
	}
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(c)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// Proves: MQTT-04
// Every field encodes under its T Tbl 8.7-1 key: a message with all
// fields set decodes, as a plain integer-keyed CBOR map, to keys 1-22
// holding exactly the values given.
func TestKeyNumbers(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	m := &Message{Operation: u64(101), Token: 102, EP: str("ep"), PCT: u64(104), URI: str("/5"), Paths: []byte{6},
		Payload: Payload{7}, Lifetime: u64(108), Version: str("1.2"), B: str("M"), SMS: str("11"), Pmin: u64(112),
		Pmax: u64(113), Gt: f(14.5), St: f(15.5), Epmin: u64(116), Epmax: u64(117), Result: u64(118), CT: u64(119),
		Edge: u64(120), Hqmax: u64(121), Depth: u64(122)}
	b, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got map[uint64]any
	if err := cbor.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := map[uint64]any{1: uint64(101), 2: uint64(102), 3: "ep", 4: uint64(104), 5: "/5", 6: []byte{6}, 7: []byte{7},
		8: uint64(108), 9: "1.2", 10: "M", 11: "11", 12: uint64(112), 13: uint64(113), 14: 14.5, 15: 15.5,
		16: uint64(116), 17: uint64(117), 18: uint64(118), 19: uint64(119), 20: uint64(120), 21: uint64(121), 22: uint64(122)}
	if len(got) != len(want) {
		t.Fatalf("keys %v", got)
	}
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("key %d = %#v, want %#v", k, got[k], v)
		}
	}
}
