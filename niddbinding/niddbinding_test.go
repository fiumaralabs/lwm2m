package niddbinding

import (
	"bytes"
	"context"
	"encoding/hex"
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

type sent struct {
	ue   string
	data []byte
}

type fakeSCEF struct{ out chan sent }

func (f *fakeSCEF) Submit(_ context.Context, ue string, d []byte) error {
	f.out <- sent{ue, d}
	return nil
}

func (f *fakeSCEF) next(t *testing.T) sent {
	t.Helper()
	select {
	case s := <-f.out:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("nothing submitted")
		return sent{}
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

// Proves: CIOT-01
// Over NIDD the server addresses the UE by External Identifier (or
// MSISDN) through the SCEF: a Register arrives as Non-IP data and is
// answered with a piggybacked 2.01 to the same UE, the registration has
// binding N, and later requests go to that UE. No downlink message
// exceeds the 1358-byte Non-IP MTU (NAS has no segmentation): a larger one
// is refused before it reaches the SCEF, one of exactly 1358 bytes is sent.
// Unanswered CON requests are retransmitted with RFC 7252 backoff.
func TestNIDD(t *testing.T) {
	srv := server.New(server.Config{})
	defer srv.Close()
	scef := &fakeSCEF{out: make(chan sent, 16)}
	b := New(Config{Server: srv, SCEF: scef, AckTimeout: 20 * time.Millisecond, MaxRetransmit: 2})
	ue := "ue-17@iot.example.net"
	ctx := context.Background()
	lf := lwm2m.FormatLinkFormat
	reg, _ := coapwire.Marshal(coapwire.Frame{Type: message.Confirmable, MID: 3, Msg: server.Message{
		Code: codes.POST, Path: "/rd", Query: []string{"ep=nb1", "lt=3600", "lwm2m=1.2", "b=N"}, Token: []byte{5},
		Format: &lf, Payload: []byte("</3/0>,</3303/0>"),
	}})
	if err := b.Deliver(ctx, ue, reg); err != nil {
		t.Fatal(err)
	}
	s := scef.next(t)
	if r := frame(t, s.data); s.ue != ue || r.Type != message.Acknowledgement || r.Msg.Code != codes.Created {
		t.Fatalf("reply %+v to %s", r, s.ue)
	}
	r, ok := srv.Store().ByEndpoint("nb1")
	if !ok || r.Binding != "N" || r.Addr.String() != "nidd:"+ue {
		t.Fatalf("registration %+v", r)
	}

	c, _ := b.Peer(ue)
	_, err := c.Exchange(ctx, &server.Message{Code: codes.PUT, Path: "/3303/0/5750", Token: []byte{1}, Payload: make([]byte, MTU)})
	if !errors.Is(err, coapwire.ErrTooLarge) {
		t.Fatalf("oversized: %v", err)
	}
	_, err = srv.Write(ctx, "nb1", lwm2m.MustParsePath("/3303/0/5750"),
		[]lwm2m.Node{lwm2m.ValueNode(lwm2m.MustParsePath("/3303/0/5750"), lwm2m.String(string(make([]byte, 2000))))}, server.WriteOptions{})
	if !errors.Is(err, coapwire.ErrTooLarge) {
		t.Fatalf("server write over MTU: %v", err)
	}
	select {
	case s := <-scef.out:
		t.Fatalf("oversized message reached the SCEF (%d bytes)", len(s.data))
	default:
	}

	// Exactly MTU bytes: header 4 + token 1 + Uri-Path 3303/0/5750 (5+2+5)
	// + payload marker 1.
	fit := MTU - 4 - 1 - 12 - 1
	start := time.Now()
	_, err = c.Exchange(ctx, &server.Message{Code: codes.PUT, Path: "/3303/0/5750", Token: []byte{2}, Payload: bytes.Repeat([]byte{1}, fit)})
	if !errors.Is(err, coapwire.ErrTimeout) {
		t.Fatalf("err %v", err)
	}
	for range 3 { // first send and 2 retransmissions
		if s := scef.next(t); len(s.data) != MTU {
			t.Fatalf("sent %d bytes", len(s.data))
		}
	}
	if el := time.Since(start); el < 20*time.Millisecond+40*time.Millisecond+80*time.Millisecond {
		t.Fatalf("no backoff: %v", el)
	}
	if ValidUE("12345") != nil || ValidUE("a@b") != nil || ValidUE("x") == nil || ValidUE("@b") == nil {
		t.Fatal("ValidUE")
	}
}

// Notifications over NIDD reach the core as Notification events and a CON
// one is acknowledged; one for an unknown observation is Reset (OBS-02).
func TestNIDDNotify(t *testing.T) {
	notes := make(chan server.Event, 4)
	srv := server.New(server.Config{OnEvent: func(e server.Event) {
		if _, ok := e.(server.Notification); ok {
			notes <- e
		}
	}})
	defer srv.Close()
	scef := &fakeSCEF{out: make(chan sent, 16)}
	b := New(Config{Server: srv, SCEF: scef})
	ctx := context.Background()
	lf, tf := lwm2m.FormatLinkFormat, lwm2m.FormatText
	reg, _ := coapwire.Marshal(coapwire.Frame{Type: message.Confirmable, MID: 1, Msg: server.Message{
		Code: codes.POST, Path: "/rd", Query: []string{"ep=nb2", "lt=3600", "lwm2m=1.1", "b=N"}, Format: &lf, Payload: []byte("</3/0>"),
	}})
	_ = b.Deliver(ctx, "12345", reg)
	scef.next(t)
	go func() {
		s := <-scef.out
		f, _ := coapwire.Unmarshal(s.data)
		zero := uint32(0)
		ack, _ := coapwire.Marshal(coapwire.Frame{Type: message.Acknowledgement, MID: f.MID, Msg: server.Message{
			Code: codes.Content, Token: f.Msg.Token, Observe: &zero, Format: &tf, Payload: []byte("1"),
		}})
		_ = b.Deliver(ctx, "12345", ack)
	}()
	ob, _, err := srv.Observe(ctx, "nb2", lwm2m.MustParsePath("/3/0/9"), server.ObserveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	one := uint32(1)
	tok, _ := hex.DecodeString(ob.ID)
	note := func(token []byte, mid uint16) coapwire.Frame {
		n, _ := coapwire.Marshal(coapwire.Frame{Type: message.Confirmable, MID: mid, Msg: server.Message{
			Code: codes.Content, Token: token, Observe: &one, Format: &tf, Payload: []byte("2"),
		}})
		_ = b.Deliver(ctx, "12345", n)
		return frame(t, scef.next(t).data)
	}
	if f := note(tok, 50); f.Type != message.Acknowledgement || f.MID != 50 {
		t.Fatalf("notification reply %+v", f)
	}
	select {
	case <-notes:
	case <-time.After(5 * time.Second):
		t.Fatal("no Notification event")
	}
	if f := note([]byte{0xde, 0xad}, 51); f.Type != message.Reset || f.MID != 51 {
		t.Fatalf("unknown observation reply %+v", f)
	}
}
