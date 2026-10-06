package smsbinding

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/internal/coapwire"
	"github.com/fiumaralabs/lwm2m/internal/vectors"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// fakeSMSC records mobile-terminated SMS.
type fakeSMSC struct{ out chan SMS }

func newSMSC() *fakeSMSC { return &fakeSMSC{out: make(chan SMS, 64)} }

func (f *fakeSMSC) Submit(_ context.Context, m SMS) error { f.out <- m; return nil }

func (f *fakeSMSC) next(t *testing.T) SMS {
	t.Helper()
	select {
	case m := <-f.out:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no SMS submitted")
		return SMS{}
	}
}

func (f *fakeSMSC) none(t *testing.T) {
	t.Helper()
	select {
	case m := <-f.out:
		t.Fatalf("unexpected SMS %x", m.Data)
	case <-time.After(50 * time.Millisecond):
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

func enc(t *testing.T, f coapwire.Frame) []byte {
	t.Helper()
	b, err := coapwire.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const msisdn = "12345678"

func setup(t *testing.T, cfg Config) (*Binding, *fakeSMSC, *server.Server) {
	srv := server.New(server.Config{})
	t.Cleanup(func() { srv.Close() })
	smsc := newSMSC()
	cfg.Server, cfg.SMSC = srv, smsc
	return New(cfg), smsc, srv
}

// register sends a Register over SMS and returns the reply.
func register(t *testing.T, b *Binding, smsc *fakeSMSC, from string, query ...string) coapwire.Frame {
	t.Helper()
	req := enc(t, coapwire.Frame{Type: message.Confirmable, MID: 7, Msg: server.Message{
		Code: codes.POST, Path: "/rd", Query: query, Token: []byte{1, 2}, Payload: []byte("</1/0>,</3/0>"),
		Format: ptr(lwm2m.FormatLinkFormat),
	}})
	if err := b.Deliver(context.Background(), SMS{MSISDN: from, Data: req}); err != nil {
		t.Fatal(err)
	}
	return frame(t, smsc.next(t).Data)
}

func ptr(f lwm2m.ContentFormat) *lwm2m.ContentFormat { return &f }

// Proves: SMS-01
// CoAP travels as 8-bit SMS user data: a Register over SMS is answered by
// SMS with a piggybacked 2.01, and the server then reads the client over
// SMS. CoAP retransmission is off: an unanswered request is sent once.
// Messages above 140 bytes are concatenated (8-bit reference IE) both
// ways. The peer is addressed by MSISDN with binding S.
func TestCoAPOverSMS(t *testing.T) {
	b, smsc, srv := setup(t, Config{Debug: true, ResponseTimeout: 100 * time.Millisecond})
	r := register(t, b, smsc, "+1 234-5678", "ep=node", "lt=86400", "lwm2m=1.1", "b=S", "sms=12345678")
	if r.Type != message.Acknowledgement || r.MID != 7 || r.Msg.Code != codes.Created || !bytes.Equal(r.Msg.Token, []byte{1, 2}) {
		t.Fatalf("register reply %+v", r)
	}
	reg, ok := srv.Store().ByEndpoint("node")
	if !ok || reg.Binding != "S" || reg.SMS != msisdn || reg.Addr.String() != "sms:"+msisdn || reg.Addr.Network() != "sms" {
		t.Fatalf("registration %+v", reg)
	}

	// Read over SMS, answered with a piggybacked response.
	done := make(chan *server.Response, 1)
	go func() {
		resp, err := srv.Read(context.Background(), "node", lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{})
		if err != nil {
			t.Error(err)
		}
		done <- resp
	}()
	m := smsc.next(t)
	if m.MSISDN != msisdn || len(m.UDH) != 0 || m.DCS != 0x04 {
		t.Fatalf("downlink SMS %+v, want 8-bit (DCS 04) without UDH", m)
	}
	req := frame(t, m.Data)
	if req.Type != message.Confirmable || req.Msg.Code != codes.GET || req.Msg.Path != "/3/0/0" {
		t.Fatalf("request %+v", req)
	}
	ack := enc(t, coapwire.Frame{Type: message.Acknowledgement, MID: req.MID, Msg: server.Message{
		Code: codes.Content, Token: req.Msg.Token, Format: ptr(lwm2m.FormatText), Payload: []byte("ACME"),
	}})
	if err := b.Deliver(context.Background(), SMS{MSISDN: msisdn, Data: ack}); err != nil {
		t.Fatal(err)
	}
	if resp := <-done; resp == nil || resp.Code != codes.Content || len(resp.Nodes) != 1 || resp.Nodes[0].Value.Str != "ACME" {
		t.Fatalf("read %+v", resp)
	}

	// No retransmission: one SMS, then a timeout.
	c, _ := b.Peer(msisdn)
	_, err := c.Exchange(context.Background(), &server.Message{Code: codes.GET, Path: "/3/0", Token: []byte{9}})
	if !errors.Is(err, coapwire.ErrTimeout) {
		t.Fatalf("err %v", err)
	}
	smsc.next(t)
	smsc.none(t)

	// A long write is concatenated; the parts reassemble on the way in.
	big := bytes.Repeat([]byte("x"), 300)
	go func() {
		_, _ = c.Exchange(context.Background(), &server.Message{Code: codes.PUT, Path: "/3/0/14", Token: []byte{8}, Payload: big})
	}()
	var parts []SMS
	for range 3 {
		parts = append(parts, smsc.next(t))
	}
	var joined []byte
	for i, p := range parts {
		if 1+len(p.UDH)+len(p.Data) > MaxUserData || p.DCS != 0x04 || !bytes.Equal(p.UDH[:2], []byte{0, 3}) || p.UDH[3] != 3 || int(p.UDH[4]) != i+1 {
			t.Fatalf("part %d: udh %x, %d bytes", i, p.UDH, len(p.Data))
		}
		joined = append(joined, p.Data...)
	}
	if f := frame(t, joined); !bytes.Equal(f.Msg.Payload, big) {
		t.Fatal("concatenated payload differs")
	}
	b.reassemble(msisdn, SMS{UDH: parts[1].UDH, Data: parts[1].Data})
	got, done2 := b.reassemble(msisdn, SMS{UDH: parts[0].UDH, Data: parts[0].Data})
	if done2 {
		t.Fatal("reassembled early")
	}
	got, done2 = b.reassemble(msisdn, SMS{UDH: parts[2].UDH, Data: parts[2].Data})
	if !done2 || !bytes.Equal(got.Data, joined) {
		t.Fatal("reassembly out of order failed")
	}
}

// Proves: SMS-02, SMS-07
// The trigger is Execute /1/x/8 (or /1/x/9) in a WAP Push: the encoder
// reproduces App. L byte for byte (WDP port 2948, X-WAP-Application-ID
// 0x9A, the 14-byte CoAP POST of vector spec-coap-sms-trigger-execute-1-0-8),
// and the vector decodes to that request. Trigger does not wait for a
// response (the client must not send one), works with NoSec, and is only
// sent when the client is not on binding S, gave an SMS number and has
// /1/x/21 true.
func TestSMSTrigger(t *testing.T) {
	vs, err := vectors.Load("spec-examples")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors.Vector
	for _, x := range vs {
		if x.ID == "spec-coap-sms-trigger-execute-1-0-8" {
			v = x
		}
	}
	raw, _ := v.Payload()
	var exp struct {
		CoAP struct {
			MID   uint16   `json:"message_id"`
			Token string   `json:"token"`
			Path  []string `json:"uri_path"`
		} `json:"coap"`
	}
	if err := json.Unmarshal(v.Expected, &exp); err != nil {
		t.Fatal(err)
	}
	f := frame(t, raw)
	if f.Type != message.Confirmable || f.Msg.Code != codes.POST || f.MID != exp.CoAP.MID ||
		hex.EncodeToString(f.Msg.Token) != exp.CoAP.Token || f.Msg.Path != "/1/0/8" || len(f.Msg.Payload) != 0 {
		t.Fatalf("decoded %+v", f)
	}
	tok, _ := hex.DecodeString(exp.CoAP.Token)
	coap, err := TriggerMessage(exp.CoAP.MID, tok, 0, false)
	if err != nil || !bytes.Equal(coap, raw) {
		t.Fatalf("encoded %x, want %x (%v)", coap, raw, err)
	}
	udh, data := WAPPush(coap)
	pdu := append(append([]byte{byte(len(udh))}, udh...), data...)
	want, _ := hex.DecodeString("060504" + "0b84" + "c002" + "010603c4af9a" + *v.BytesHex)
	if !bytes.Equal(pdu, want) {
		t.Fatalf("WAP push %x, want %x (App. L Tbl L.-1)", pdu, want)
	}
	if got, err := ParseWAPPush(SMS{UDH: udh, Data: data}); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("ParseWAPPush %x %v", got, err)
	}
	if _, err := ParseWAPPush(SMS{UDH: []byte{5, 4, 0, 80, 0, 0}, Data: data}); err == nil {
		t.Fatal("wrong WDP port accepted")
	}
	if bs, _ := TriggerMessage(1, tok, 2, true); frame(t, bs).Msg.Path != "/1/2/9" {
		t.Fatal("bootstrap trigger path")
	}

	b, smsc, _ := setup(t, Config{}) // NoSec: trigger-only
	if err := b.Trigger(context.Background(), "+"+msisdn, 2, false); err != nil {
		t.Fatal(err)
	}
	m := smsc.next(t)
	coapIn, err := ParseWAPPush(m)
	if err != nil || m.MSISDN != msisdn || m.DCS != 0x04 || frame(t, coapIn).Msg.Path != "/1/2/8" {
		t.Fatalf("trigger SMS %+v %v", m, err)
	}

	reg := &server.Registration{Binding: "U", SMS: msisdn}
	if !ShouldTrigger(reg, true) || ShouldTrigger(reg, false) ||
		ShouldTrigger(&server.Registration{Binding: "S", SMS: msisdn}, true) || ShouldTrigger(&server.Registration{Binding: "U"}, true) {
		t.Fatal("ShouldTrigger")
	}
}

// xorSec is a stand-in Security: "protected" means a 0xA5 marker byte
// followed by the message XOR 0x5A.
type xorSec struct{}

func (xorSec) Seal(_ string, p []byte) (SMS, error) {
	out := []byte{0xA5}
	for _, c := range p {
		out = append(out, c^0x5A)
	}
	return SMS{Data: out}, nil
}

func (xorSec) Open(m SMS) ([]byte, error) {
	d := m.Data
	if len(d) == 0 || d[0] != 0xA5 {
		return nil, errors.New("not protected")
	}
	out := []byte{}
	for _, c := range d[1:] {
		out = append(out, c^0x5A)
	}
	return out, nil
}

// Proves: SMS-03, SMS-06
// With SMS Secured mode configured, an SMS not protected with the
// expected parameters is discarded without any reply, and a protected one
// is served with a protected reply. NoSec inbound SMS is ignored unless the
// binding runs in debug mode (NoSec is for debugging or triggering only).
// SMS from an MSISDN outside the allowed set are silently ignored.
func TestSMSSecurity(t *testing.T) {
	b, smsc, _ := setup(t, Config{Security: xorSec{}, Allowed: func(n string) bool { return n == msisdn }})
	req := enc(t, coapwire.Frame{Type: message.Confirmable, MID: 1, Msg: server.Message{
		Code: codes.POST, Path: "/rd", Query: []string{"ep=sec", "lt=60", "lwm2m=1.1", "b=S"}, Token: []byte{1},
		Format: ptr(lwm2m.FormatLinkFormat), Payload: []byte("</3/0>"),
	}})
	ctx := context.Background()
	_ = b.Deliver(ctx, SMS{MSISDN: msisdn, Data: req}) // clear text
	smsc.none(t)
	sealed, _ := xorSec{}.Seal(msisdn, req)
	_ = b.Deliver(ctx, SMS{MSISDN: "999", Data: sealed.Data}) // unknown sender
	smsc.none(t)
	_ = b.Deliver(ctx, SMS{MSISDN: msisdn, Data: sealed.Data})
	out := smsc.next(t)
	plain, err := xorSec{}.Open(out)
	if err != nil || frame(t, plain).Msg.Code != codes.Created {
		t.Fatalf("secured reply %x %v", out.Data, err)
	}

	nosec, smsc2, _ := setup(t, Config{})
	_ = nosec.Deliver(ctx, SMS{MSISDN: msisdn, Data: req})
	smsc2.none(t)
}

// Proves: SMS-04
// /0/x/6 values: 1 DTLS, 2 Secure Packet Structure, 3 NoSec, 204-255
// proprietary; 4 is reserved. One DTLS-protected SMS leaves 107 bytes of
// LwM2M payload, 99 with a token.
func TestSMSModes(t *testing.T) {
	for v, ok := range map[int64]bool{0: false, 1: true, 2: true, 3: true, 4: false, 5: false, 203: false, 204: true, 255: true, 256: false} {
		if _, err := ParseMode(v); (err == nil) != ok {
			t.Errorf("mode %d: err %v", v, err)
		}
	}
	if PayloadBudget(false) != 107 || PayloadBudget(true) != 99 {
		t.Fatal("payload budget")
	}
	for _, s := range []string{"", "+", "12a", "1234567890123456"} {
		if _, err := NormalizeMSISDN(s); err == nil {
			t.Errorf("MSISDN %q accepted", s)
		}
	}
}
