package sms

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/transport/coap"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/internal/coapwire"
	"github.com/fiumaralabs/lwm2m/security/oscore"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// smsOSCORE is an SMS binding whose server holds the OSCORE context of
// endpoint "osc"; cli is the client's side of that context.
type smsOSCORE struct {
	b    *Binding
	smsc *fakeSMSC
	srv  *server.Server
	cli  *oscore.Context
	ev   chan server.Event
	// seal and open are the client's SMS channel: identity for NoSec, a
	// Secured Packet card otherwise.
	seal func([]byte) SMS
	open func(SMS) []byte
	mid  uint16
}

func newSMSOSCORE(t *testing.T, sec Security, seal func([]byte) SMS, open func(SMS) []byte) *smsOSCORE {
	ev := make(chan server.Event, 16)
	srv := server.New(server.Config{OnEvent: func(e server.Event) { ev <- e }})
	t.Cleanup(func() { srv.Close() })
	cp := oscore.Params{MasterSecret: []byte("0123456789abcdef"), MasterSalt: []byte("salt"), SenderID: []byte("c1"), RecipientID: []byte("srv")}
	o := coap.New(srv).EnableOSCORE()
	if err := o.Put("osc", cp.Reverse()); err != nil {
		t.Fatal(err)
	}
	cli, err := oscore.New(cp)
	if err != nil {
		t.Fatal(err)
	}
	smsc := newSMSC()
	b := New(Config{Server: srv, SMSC: smsc, Security: sec, OSCORE: o, ResponseTimeout: 2 * time.Second})
	if seal == nil {
		seal = func(c []byte) SMS { return SMS{MSISDN: msisdn, Data: c} }
		open = func(m SMS) []byte { return m.Data }
	}
	return &smsOSCORE{b: b, smsc: smsc, srv: srv, cli: cli, ev: ev, seal: seal, open: open, mid: 100}
}

func (s *smsOSCORE) deliver(t *testing.T, m message.Message) {
	t.Helper()
	b, err := coapwire.MarshalCoAP(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.b.Deliver(context.Background(), s.seal(b)); err != nil {
		t.Fatal(err)
	}
}

func (s *smsOSCORE) next(t *testing.T) message.Message {
	t.Helper()
	m, err := coapwire.UnmarshalCoAP(s.open(s.smsc.next(t)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func path(t *testing.T, p string) message.Options {
	t.Helper()
	opts, _, err := message.Options{}.SetPath(make([]byte, 64), p)
	if err != nil {
		t.Fatal(err)
	}
	return opts
}

// register sends a protected Register (with Echo if echo != nil) and
// returns the decrypted reply.
func (s *smsOSCORE) register(t *testing.T, echo []byte) message.Message {
	t.Helper()
	opts := append(path(t, "/rd"), message.Option{ID: message.ContentFormat, Value: []byte{40}})
	for _, q := range []string{"ep=osc", "lt=600", "lwm2m=1.2", "b=S", "sms=" + msisdn} {
		opts = append(opts, message.Option{ID: message.URIQuery, Value: []byte(q)})
	}
	if echo != nil {
		opts = append(opts, message.Option{ID: oscore.OptionEcho, Value: echo})
	}
	plain := message.Message{Code: codes.POST, Token: []byte{1, 2}, Options: opts, Payload: []byte("</1/0>,</3/0>")}
	prot, x, err := s.cli.ProtectRequest(plain)
	if err != nil {
		t.Fatal(err)
	}
	s.mid++
	prot.Type, prot.MessageID = message.Confirmable, int32(s.mid)
	s.deliver(t, prot)
	r := s.next(t)
	if r.Type != message.Acknowledgement || r.MessageID != int32(s.mid) || !r.Options.HasOption(oscore.OptionOSCORE) {
		t.Fatalf("register reply %+v, want a protected piggybacked ACK", r)
	}
	inner, err := s.cli.UnprotectResponse(r, x)
	if err != nil {
		t.Fatal(err)
	}
	return inner
}

// serve answers the next downlink, which must be OSCORE-protected, with
// resp (protected); it returns the decrypted request.
func (s *smsOSCORE) serve(t *testing.T, resp message.Message) (message.Message, *oscore.Exchange) {
	t.Helper()
	req := s.next(t)
	if !req.Options.HasOption(oscore.OptionOSCORE) || req.Options.HasOption(message.URIPath) {
		t.Fatalf("downlink %+v is not OSCORE-protected", req)
	}
	inner, x, err := s.cli.UnprotectRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	prot, err := s.cli.ProtectResponse(resp, x, false)
	if err != nil {
		t.Fatal(err)
	}
	prot.Type, prot.MessageID, prot.Token = message.Acknowledgement, req.MessageID, req.Token
	s.deliver(t, prot)
	return inner, x
}

func plainRegister(t *testing.T, ep string) []byte {
	return enc(t, coapwire.Frame{Type: message.Confirmable, MID: uint16(len(ep)), Msg: server.Message{
		Code: codes.POST, Path: "/rd", Query: []string{"ep=" + ep, "lt=600", "lwm2m=1.2", "b=S"}, Token: []byte{9},
		Format: ptr(lwm2m.FormatLinkFormat), Payload: []byte("</3/0>"),
	}})
}

// refusePlain checks that an unprotected Register for the OSCORE
// endpoint gets an unprotected 4.01 and registers nothing (Tbl 6.7-2).
func (s *smsOSCORE) refusePlain(t *testing.T) {
	t.Helper()
	if err := s.b.Deliver(context.Background(), s.seal(plainRegister(t, "osc"))); err != nil {
		t.Fatal(err)
	}
	r := s.next(t)
	if r.Code != codes.Unauthorized || r.Options.HasOption(oscore.OptionOSCORE) || !bytes.Equal(r.Token, []byte{9}) {
		t.Fatalf("plain Register: %+v, want unprotected 4.01", r)
	}
	if len(s.srv.Store().All()) != 0 {
		t.Fatal("plain Register processed")
	}
}

// registerFresh runs the first Register on the context: 4.01 with Echo,
// then 2.01 with that Echo (OSC-04, as on UDP).
func (s *smsOSCORE) registerFresh(t *testing.T) {
	t.Helper()
	r := s.register(t, nil)
	echo, err := r.Options.GetBytes(oscore.OptionEcho)
	if r.Code != codes.Unauthorized || err != nil || len(echo) == 0 {
		t.Fatalf("first Register: %v echo %x, want 4.01 with Echo", r.Code, echo)
	}
	if len(s.srv.Store().All()) != 0 {
		t.Fatal("Register without Echo processed")
	}
	if r = s.register(t, echo); r.Code != codes.Created {
		t.Fatalf("Register with Echo: %v", r.Code)
	}
	reg, ok := s.srv.Store().ByEndpoint("osc")
	if !ok || reg.Binding != "S" || !strings.HasPrefix(reg.Identity.Addr, "oscore:") {
		t.Fatalf("registration %+v", reg)
	}
}

// read runs a Read over SMS, answered by the client with "ACME".
func (s *smsOSCORE) read(t *testing.T) {
	t.Helper()
	done := make(chan *server.Response, 1)
	go func() {
		resp, err := s.srv.Read(context.Background(), "osc", lwm2m.MustParsePath("/3/0/0"), server.ReadOptions{})
		if err != nil {
			t.Error(err)
		}
		done <- resp
	}()
	inner, _ := s.serve(t, message.Message{Code: codes.Content, Options: message.Options{{ID: message.ContentFormat, Value: []byte{0}}}, Payload: []byte("ACME")})
	if p, _ := inner.Options.Path(); inner.Code != codes.GET || p != "/3/0/0" {
		t.Fatalf("decrypted downlink %v %q", inner.Code, p)
	}
	if resp := <-done; resp == nil || resp.Code != codes.Content || resp.Nodes[0].Value.Str != "ACME" {
		t.Fatalf("read %+v", resp)
	}
}

// Proves: OSC-08
// SMS NoSec with /0/x/17 present = SMS protected by OSCORE (T §5.3.1):
// an unprotected Register for the OSCORE endpoint is refused with 4.01
// and other unprotected requests are dropped; the protected one goes
// through Echo freshness on the first use of the context, as on UDP
// (OSC-04), and registers with the OSCORE identity on binding S. Read,
// Observe and the SMS trigger (Execute /1/x/8) go out protected; a
// protected notification is delivered and a plain one on the same token
// dropped; a plain request cannot be sent on the channel.
func TestOSCOREOverSMSNoSec(t *testing.T) {
	s := newSMSOSCORE(t, nil, nil, nil)
	ctx := context.Background()
	s.refusePlain(t)
	// Any other unprotected request is dropped: NoSec SMS without OSCORE
	// is for debugging only (T §5.3).
	if err := s.b.Deliver(ctx, SMS{MSISDN: msisdn, Data: plainRegister(t, "other")}); err != nil {
		t.Fatal(err)
	}
	s.smsc.none(t)
	if len(s.srv.Store().All()) != 0 {
		t.Fatal("plain Register processed")
	}
	s.registerFresh(t)
	s.read(t)

	c, _ := s.b.Peer(msisdn)
	if _, err := c.Exchange(ctx, &server.Message{Code: codes.GET, Path: "/3/0", Token: []byte{4}}); !errors.Is(err, coapwire.ErrUnprotected) {
		t.Fatalf("plain downlink: %v", err)
	}

	// Observe: protected registration and notifications.
	type obsRes struct {
		ob  *server.Observation
		err error
	}
	obc := make(chan obsRes, 1)
	go func() {
		ob, _, err := s.srv.Observe(ctx, "osc", lwm2m.MustParsePath("/3/0/9"), server.ObserveOptions{})
		obc <- obsRes{ob, err}
	}()
	inner, x := s.serve(t, message.Message{Code: codes.Content, Options: message.Options{{ID: message.ContentFormat, Value: []byte{0}}, {ID: message.Observe, Value: []byte{}}}, Payload: []byte("100")})
	if !x.Observe() || inner.Code != codes.GET {
		t.Fatalf("observe request %+v", inner)
	}
	or := <-obc
	if or.err != nil || or.ob == nil {
		t.Fatalf("observe: %v", or.err)
	}
	notif, err := s.cli.ProtectResponse(message.Message{Code: codes.Content, Options: message.Options{{ID: message.ContentFormat, Value: []byte{0}}, {ID: message.Observe, Value: []byte{1}}}, Payload: []byte("42")}, x, true)
	if err != nil {
		t.Fatal(err)
	}
	notif.Type, notif.MessageID, notif.Token = message.Confirmable, 300, inner.Token
	s.deliver(t, notif)
	if ack := s.next(t); ack.Type != message.Acknowledgement || ack.MessageID != 300 {
		t.Fatalf("notification ACK %+v", ack)
	}
	waitNotification(t, s.ev)
	s.deliver(t, message.Message{Code: codes.Content, Type: message.Confirmable, MessageID: 301, Token: inner.Token,
		Options: message.Options{{ID: message.ContentFormat, Value: []byte{0}}, {ID: message.Observe, Value: []byte{2}}}, Payload: []byte("7")})
	s.next(t) // ACK
	select {
	case e := <-s.ev:
		if _, ok := e.(server.Notification); ok {
			t.Fatal("plain notification delivered")
		}
	case <-time.After(100 * time.Millisecond):
	}

	// The SMS trigger is protected with the endpoint's context.
	if err := s.b.Trigger(ctx, msisdn, 0, false); err != nil {
		t.Fatal(err)
	}
	trig, err := ParseWAPPush(s.smsc.next(t))
	if err != nil {
		t.Fatal(err)
	}
	tm, _ := coapwire.UnmarshalCoAP(trig)
	ti, _, err := s.cli.UnprotectRequest(tm)
	if p, _ := ti.Options.Path(); err != nil || ti.Code != codes.POST || p != "/1/0/8" || tm.Options.HasOption(message.URIPath) {
		t.Fatalf("trigger %v %q (%v), want protected Execute /1/0/8", ti.Code, p, err)
	}
}

func waitNotification(t *testing.T, ev chan server.Event) {
	t.Helper()
	for {
		select {
		case e := <-ev:
			if n, ok := e.(server.Notification); ok {
				if !bytes.Equal(n.Response.Payload, []byte("42")) {
					t.Fatalf("notification %+v", n.Response)
				}
				return
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no notification")
		}
	}
}

// Proves: OSC-08
// SMS Secured mode (Secured Packets) plus /0/x/17: both layers protect
// the message, as DTLS plus OSCORE does on UDP (T §5.4.1). The Secured
// Packet alone is not enough: a plain Register inside a valid packet is
// refused with 4.01. With OSCORE inside, Register (with Echo) and Read
// work.
func TestOSCOREOverSMSSecured(t *testing.T) {
	srvSec, card := pair(aesKeys)
	seal := func(c []byte) SMS {
		m, err := card.Seal(msisdn, c)
		if err != nil {
			t.Fatal(err)
		}
		m.MSISDN = msisdn
		return m
	}
	open := func(m SMS) []byte { return decodeCommand(t, aesKeys, m) }
	s := newSMSOSCORE(t, srvSec, seal, open)
	s.refusePlain(t)
	s.registerFresh(t)
	s.read(t)
}
