package lorawanbinding

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/internal/coapwire"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type downlink struct {
	dev       string
	fport     uint8
	data      []byte
	confirmed bool
	at        time.Time // when the Network Server reported it sent
}

// fakeNS reports each downlink as sent after delay.
type fakeNS struct {
	delay time.Duration
	out   chan downlink
}

func (n *fakeNS) Downlink(_ context.Context, dev string, fport uint8, p []byte, confirmed bool) error {
	time.Sleep(n.delay)
	n.out <- downlink{dev, fport, append([]byte(nil), p...), confirmed, time.Now()}
	return nil
}

func (n *fakeNS) next(t *testing.T) downlink {
	t.Helper()
	select {
	case d := <-n.out:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("no downlink")
		return downlink{}
	}
}

func frame(t *testing.T, b []byte) coapwire.Frame {
	t.Helper()
	f, err := coapwire.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func setup(t *testing.T, cfg Config) (*Binding, *fakeNS, *server.Server) {
	srv := server.New(server.Config{})
	t.Cleanup(func() { srv.Close() })
	ns := &fakeNS{out: make(chan downlink, 64)}
	cfg.Server, cfg.NS = srv, ns
	if cfg.FPort == 0 {
		cfg.FPort = 10
	}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b, ns, srv
}

const dev = "70B3D57ED0001234"

func registerFrame(t *testing.T, mid uint16, query []string, objs string) []byte {
	lf := lwm2m.FormatLinkFormat
	m := server.Message{Code: codes.POST, Path: "/rd", Query: query, Token: []byte{byte(mid)}}
	if objs != "" {
		m.Format, m.Payload = &lf, []byte(objs)
	}
	b, err := coapwire.Marshal(coapwire.Frame{Type: message.Confirmable, MID: mid, Msg: m})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Proves: LORA-01
// The server URI is lorawan://{FPort} with FPort 1-255; the server acts as
// the Application Server behind a Network Server on that FPort, frames on
// other FPorts are not LwM2M, and the session is NoSec (LoRaWAN secures
// it), identified by DevEUI.
func TestLoRaWANServerURI(t *testing.T) {
	for uri, want := range map[string]uint8{"lorawan://1": 1, "lorawan://255": 255, "lorawan://0": 0, "lorawan://256": 0, "coap://1": 0, "lorawan://+5": 0, "lorawan://": 0} {
		if got, err := ParseServerURI(uri); got != want || (err == nil) != (want != 0) {
			t.Errorf("%s: %d %v", uri, got, err)
		}
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("FPort 0 accepted")
	}
	b, ns, srv := setup(t, Config{FPort: 42})
	ctx := context.Background()
	_ = b.Uplink(ctx, dev, 43, registerFrame(t, 1, nil, "</3/0>"))
	select {
	case d := <-ns.out:
		t.Fatalf("answered a frame on another FPort: %x", d.data)
	case <-time.After(50 * time.Millisecond):
	}
	_ = b.Uplink(ctx, dev, 42, registerFrame(t, 1, nil, "</3/0>"))
	if d := ns.next(t); d.fport != 42 || d.dev != dev || frame(t, d.data).Msg.Code != codes.Created {
		t.Fatalf("reply %+v", d)
	}
	reg, ok := srv.Store().ByEndpoint(dev)
	if !ok || reg.Identity.Secure() || reg.Addr.String() != "lorawan:"+dev {
		t.Fatalf("registration %+v", reg)
	}
}

// Proves: LORA-02
// Register defaults of Tbl 6.8.4-1: ep is the DevEUI, lt 30 days, lwm2m
// 1.1, queue mode. A Register without an object list gets 4.09 when the
// server has no out-of-band list, and the retry with a list succeeds; with
// an out-of-band list the first attempt succeeds and uses it.
func TestLoRaWANRegisterDefaults(t *testing.T) {
	b, ns, srv := setup(t, Config{})
	ctx := context.Background()
	_ = b.Uplink(ctx, dev, 10, registerFrame(t, 1, nil, ""))
	if r := frame(t, ns.next(t).data); r.Msg.Code != codeConflict || r.Type != message.Acknowledgement {
		t.Fatalf("no object list: %+v", r)
	}
	_ = b.Uplink(ctx, dev, 10, registerFrame(t, 2, nil, "</1/0>,</3/0>"))
	if r := frame(t, ns.next(t).data); r.Msg.Code != codes.Created {
		t.Fatalf("retry: %+v", r)
	}
	reg, ok := srv.Store().ByEndpoint(dev)
	if !ok || reg.Lifetime != DefaultLifetime*time.Second || reg.Version != "1.1" || !reg.QueueMode || len(reg.Objects) != 2 {
		t.Fatalf("registration %+v", reg)
	}

	b2, ns2, srv2 := setup(t, Config{Objects: func(d string) string {
		if d == dev {
			return "</3/0>,</3303/0>"
		}
		return ""
	}})
	_ = b2.Uplink(ctx, dev, 10, registerFrame(t, 1, []string{"ep=meter-7", "lt=600", "lwm2m=1.2"}, ""))
	if r := frame(t, ns2.next(t).data); r.Msg.Code != codes.Created {
		t.Fatalf("out-of-band: %+v", r)
	}
	reg, ok = srv2.Store().ByEndpoint("meter-7")
	if !ok || reg.Version != "1.2" || reg.Lifetime != 600*time.Second || !reg.HasInstance(3303, 0) {
		t.Fatalf("registration %+v", reg)
	}
}

// Proves: LORA-03
// Responses are piggybacked on the ACK, sent at once. A retransmitted
// uplink CON gets the same reply without being processed twice. A
// downlink CON over unconfirmed LoRaWAN waits ACK_TIMEOUT from each
// Network Server sent-notification and is resent MAX_RETRANSMIT times;
// over confirmed LoRaWAN CoAP does not retransmit. Defaults are
// App. C.2: ACK_TIMEOUT 300 s, MAX_RETRANSMIT 4. An Empty CON (opening an
// RX window) is answered with RST.
func TestLoRaWANRetransmission(t *testing.T) {
	if b, _ := New(Config{FPort: 1}); b.cfg.AckTimeout != 300*time.Second || b.cfg.MaxRetransmit != 4 {
		t.Fatalf("defaults %v %d", b.cfg.AckTimeout, b.cfg.MaxRetransmit)
	}
	ack := 40 * time.Millisecond
	b, ns, srv := setup(t, Config{AckTimeout: ack})
	ns.delay = 20 * time.Millisecond
	ctx := context.Background()
	reg := registerFrame(t, 9, nil, "</3/0>")
	_ = b.Uplink(ctx, dev, 10, reg)
	first := ns.next(t)
	_ = b.Uplink(ctx, dev, 10, reg) // endpoint resent its CON (App. C.3.2.1 step 3)
	again := ns.next(t)
	if f := frame(t, first.data); f.Type != message.Acknowledgement || f.MID != 9 || f.Msg.Code != codes.Created || string(again.data) != string(first.data) {
		t.Fatalf("piggybacked reply %+v / duplicate %x", f, again.data)
	}
	if n := len(srv.Store().All()); n != 1 {
		t.Fatalf("%d registrations", n)
	}

	c := b.Peer(dev)
	_, err := c.Exchange(ctx, &server.Message{Code: codes.GET, Path: "/3/0", Token: []byte{1}})
	if !errors.Is(err, coapwire.ErrTimeout) {
		t.Fatalf("err %v", err)
	}
	var prev time.Time
	for i := range 1 + MaxRetransmit {
		d := ns.next(t)
		if d.confirmed || frame(t, d.data).Msg.Path != "/3/0" {
			t.Fatalf("downlink %d %+v", i, d)
		}
		if i > 0 && d.at.Sub(prev) < ack+ns.delay {
			t.Fatalf("resent %v after the previous sent-notification, want >= ACK_TIMEOUT + delivery", d.at.Sub(prev))
		}
		prev = d.at
	}
	select {
	case <-ns.out:
		t.Fatal("more than MAX_RETRANSMIT retransmissions")
	default:
	}

	// A response within ACK_TIMEOUT completes the exchange.
	go func() {
		d := <-ns.out
		f, _ := coapwire.Unmarshal(d.data)
		resp, _ := coapwire.Marshal(coapwire.Frame{Type: message.Acknowledgement, MID: f.MID, Msg: server.Message{Code: codes.Content, Token: f.Msg.Token}})
		_ = b.Uplink(ctx, dev, 10, resp)
	}()
	if r, err := c.Exchange(ctx, &server.Message{Code: codes.GET, Path: "/3/0/0", Token: []byte{2}}); err != nil || r.Code != codes.Content {
		t.Fatalf("exchange %v %v", r, err)
	}

	empty, _ := coapwire.Marshal(coapwire.Frame{Type: message.Confirmable, MID: 77})
	_ = b.Uplink(ctx, dev, 10, empty)
	if f := frame(t, ns.next(t).data); f.Type != message.Reset || f.MID != 77 {
		t.Fatalf("empty CON reply %+v", f)
	}

	bc, nsc, _ := setup(t, Config{Confirmed: true, AckTimeout: ack})
	_, err = bc.Peer(dev).Exchange(ctx, &server.Message{Code: codes.GET, Path: "/3/0", Token: []byte{3}})
	if !errors.Is(err, coapwire.ErrTimeout) || !nsc.next(t).confirmed {
		t.Fatalf("confirmed: %v", err)
	}
	select {
	case <-nsc.out:
		t.Fatal("CoAP retransmitted over confirmed LoRaWAN")
	case <-time.After(2 * ack):
	}
}
