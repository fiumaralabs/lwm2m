package mqttbinding

import (
	"bytes"
	"context"
	"encoding/hex"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/cose"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fxamacker/cbor/v2"
)

var testKey = &COSEKey{KID: "ep1", Alg: cose.AESCCM16_64_128, Key: []byte("0123456789abcdef")}

// rawClient sees every publication on one topic as bytes.
type rawClient struct {
	t     *testing.T
	c     mqtt.Client
	topic string
	in    chan []byte
}

func newRawClient(t *testing.T, addr, topic string) *rawClient {
	t.Helper()
	r := &rawClient{t: t, topic: topic, in: make(chan []byte, 256)}
	r.c = mqtt.NewClient(mqtt.NewClientOptions().AddBroker("tcp://" + addr).SetClientID("raw" + hex.EncodeToString([]byte{byte(listenerID.Add(1))})))
	if err := wait(r.c.Connect()); err != nil {
		t.Fatal(err)
	}
	if err := wait(r.c.Subscribe(topic, 1, func(_ mqtt.Client, pm mqtt.Message) {
		select {
		case r.in <- bytes.Clone(pm.Payload()):
		default:
		}
	})); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.c.Disconnect(0) })
	return r
}

func (r *rawClient) send(b []byte) {
	r.t.Helper()
	if err := wait(r.c.Publish(r.topic, 1, false, b)); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rawClient) sealed(m *Message) []byte {
	r.t.Helper()
	b, err := seal(testKey, m)
	if err != nil {
		r.t.Fatal(err)
	}
	return b
}

// next returns the next publication accepted by match, decoded with k
// (nil: unprotected), and its raw bytes; nil after d.
func (r *rawClient) next(k *COSEKey, d time.Duration, match func(*Message) bool) (*Message, []byte) {
	deadline := time.After(d)
	for {
		select {
		case b := <-r.in:
			m, err := unwrap(b, k)
			if err == nil && match(m) {
				return m, b
			}
		case <-deadline:
			return nil, nil
		}
	}
}

func resultFor(tok uint64) func(*Message) bool {
	return func(m *Message) bool { return m.Result != nil && m.Token == tok }
}

// isProtected checks the T §8.6 Outer_Wrapper shape and that nothing of
// the LwM2M message is visible outside the ciphertext.
func isProtected(t *testing.T, b []byte, secret string) {
	t.Helper()
	var o struct {
		Wrapper []byte      `cbor:"1,keyasint"`
		Inner   map[int]any `cbor:"2,keyasint"`
	}
	if err := cbor.Unmarshal(b, &o); err != nil || len(o.Wrapper) == 0 || len(o.Inner) != 0 {
		t.Fatalf("not an Outer_Wrapper with COSE: %x (%v)", b, err)
	}
	var list []cbor.RawMessage
	if err := cbor.Unmarshal(o.Wrapper, &list); err != nil || len(list) != 1 {
		t.Fatalf("msg-wrapper is not [COSE_Encrypt0]: %x", o.Wrapper)
	}
	if secret != "" && bytes.Contains(b, []byte(secret)) {
		t.Fatalf("%q visible in protected message %x", secret, b)
	}
}

// Proves: MQTT-11
func TestCOSEProtectedRoundTrip(t *testing.T) {
	e := newEnv(t, "", func(c *Config) {
		c.COSE = func(e string) *COSEKey {
			if e == ep {
				return testKey
			}
			return nil
		}
	})
	c := newRawClient(t, e.addr, e.b.Topic(ep))

	c.send(c.sealed(&Message{Operation: u64(OpRegister), Token: 1, Lifetime: u64(60), Version: str("1.2"), B: str("M"), Payload: []byte("</3/0>")}))
	r, raw := c.next(testKey, 3*time.Second, resultFor(1))
	if r == nil || *r.Result != 201 {
		t.Fatalf("protected Register: %+v", r)
	}
	isProtected(t, raw, "")
	e.event(t, func(ev server.Event) bool { _, ok := ev.(server.Registered); return ok })

	type res struct {
		r   *server.Response
		err error
	}
	ch := make(chan res, 1)
	go func() {
		r, err := e.srv.Read(context.Background(), ep, lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{})
		ch <- res{r, err}
	}()
	req, raw := c.next(testKey, 3*time.Second, func(m *Message) bool { return m.Operation != nil && *m.Operation == OpRead })
	if req == nil || *req.URI != "/3/0/0" {
		t.Fatalf("protected Read request: %+v", req)
	}
	isProtected(t, raw, "/3/0/0")
	c.send(c.sealed(&Message{Token: req.Token, Result: u64(205), CT: u64(0), Payload: []byte("Open Mobile Alliance")}))
	got := <-ch
	if got.err != nil || len(got.r.Nodes) != 1 || got.r.Nodes[0].Value.Str != "Open Mobile Alliance" {
		t.Fatalf("Read = %+v, %v", got.r, got.err)
	}
	if _, err := Unmarshal(raw); err == nil {
		t.Error("a protected message decoded without the key")
	}
}

// Proves: MQTT-11
func TestCOSERejects(t *testing.T) {
	e := newEnv(t, "", func(c *Config) {
		c.COSE = func(e string) *COSEKey {
			if e == ep {
				return testKey
			}
			return nil
		}
	})
	c := newRawClient(t, e.addr, e.b.Topic(ep))
	c.send(c.sealed(&Message{Operation: u64(OpRegister), Token: 1, Lifetime: u64(60), Version: str("1.2"), Payload: []byte("</3/0>")}))
	if r, _ := c.next(testKey, 3*time.Second, resultFor(1)); r == nil || *r.Result != 201 {
		t.Fatalf("Register: %+v", r)
	}
	quiet := 300 * time.Millisecond

	// Tampered ciphertext: no authentic token, so no answer, no update.
	upd := c.sealed(&Message{Operation: u64(OpUpdate), Token: 2, Lifetime: u64(99)})
	upd[len(upd)-4] ^= 0x01
	c.send(upd)
	// A message under another key (or a wrong kid) is no better.
	other := *testKey
	other.Key = []byte("fedcba9876543210")
	b, _ := seal(&other, &Message{Operation: u64(OpUpdate), Token: 3, Lifetime: u64(99)})
	c.send(b)
	wrongKID := *testKey
	wrongKID.KID = "ep2"
	b, _ = seal(&wrongKID, &Message{Operation: u64(OpUpdate), Token: 4, Lifetime: u64(99)})
	c.send(b)
	for tok := uint64(2); tok <= 4; tok++ {
		if r, _ := c.next(nil, quiet, resultFor(tok)); r != nil {
			t.Errorf("token %d answered: %+v", tok, r)
		}
	}
	if reg, _ := e.srv.Store().ByEndpoint(ep); reg.Lifetime == 99*time.Second {
		t.Error("forged Update applied")
	}

	// Missing protection: requests are refused in clear (T §8.5), the
	// registration is untouched.
	plain, _ := Marshal(&Message{Operation: u64(OpUpdate), Token: 5, Lifetime: u64(99)})
	c.send(plain)
	if r, _ := c.next(nil, 3*time.Second, resultFor(5)); r == nil || *r.Result != 400 {
		t.Errorf("unprotected Update: %+v, want 400", r)
	}
	plain, _ = Marshal(&Message{Operation: u64(OpRegister), Token: 6, Lifetime: u64(60), Version: str("1.2"), Payload: []byte("</3/0>")})
	c.send(append([]byte{0xa2, 0x01, 0xf6, 0x02}, plain...)) // nil msg-wrapper is unprotected too
	if r, _ := c.next(nil, 3*time.Second, resultFor(6)); r == nil || *r.Result != 403 {
		t.Errorf("unprotected Register: %+v, want 403", r)
	}
	if reg, _ := e.srv.Store().ByEndpoint(ep); reg.Lifetime != time.Minute {
		t.Errorf("lifetime %v after unprotected requests", reg.Lifetime)
	}

	// An unprotected response to a protected request is not accepted.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		_, err := e.srv.Read(ctx, ep, lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{})
		errc <- err
	}()
	req, _ := c.next(testKey, 3*time.Second, func(m *Message) bool { return m.Operation != nil && *m.Operation == OpRead })
	if req == nil {
		t.Fatal("no Read")
	}
	plain, _ = Marshal(&Message{Token: req.Token, Result: u64(205), CT: u64(0), Payload: []byte("spoofed")})
	c.send(plain)
	if err := <-errc; err == nil {
		t.Error("unprotected Read response accepted")
	}

	// An endpoint without a /23 key cannot send COSE the Server can't open.
	o := newRawClient(t, e.addr, e.b.Topic("plain-ep"))
	o.send(o.sealed(&Message{Operation: u64(OpRegister), Token: 7, Lifetime: u64(60), Version: str("1.2"), Payload: []byte("</3/0>")}))
	if r, _ := o.next(nil, quiet, resultFor(7)); r != nil {
		t.Errorf("COSE from a keyless endpoint answered: %+v", r)
	}
}

// Proves: MQTT-11
func TestCOSEKeyNodes(t *testing.T) {
	ns, err := testKey.Nodes(1)
	if err != nil {
		t.Fatal(err)
	}
	want := []lwm2m.Node{
		lwm2m.ValueNode(lwm2m.MustParsePath("/23/1/0"), lwm2m.String("ep1")),
		lwm2m.ValueNode(lwm2m.MustParsePath("/23/1/1"), lwm2m.Integer(10)),
		lwm2m.ValueNode(lwm2m.MustParsePath("/23/1/2"), lwm2m.String("0123456789abcdef")),
	}
	if !lwm2m.NodesEqual(ns, want) {
		t.Fatalf("nodes %v", ns)
	}
	s := model.Default().Schema(map[uint16]model.Version{23: {Major: 1, Minor: 0}})
	if err := s.CheckCreate(23, ns); err != nil {
		t.Errorf("/23 nodes do not fit E.10: %v", err)
	}
	for _, k := range []COSEKey{
		{KID: "k", Alg: 99, Key: testKey.Key},
		{KID: "k", Alg: cose.AESCCM16_64_128, Key: []byte("short")},
		{KID: "k", Alg: cose.AESCCM16_64_128, Key: []byte("0123456789abcd\xff\xfe")},
	} {
		if _, err := k.Nodes(0); err == nil {
			t.Errorf("%+v accepted", k)
		}
	}
	if err := (COSEKey{Alg: cose.A256GCM, Key: bytes.Repeat([]byte("k"), 32)}).Validate(); err != nil {
		t.Error(err)
	}
}
