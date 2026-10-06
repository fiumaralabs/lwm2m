package oscore

import (
	"bytes"
	"testing"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func req(path string, opts ...message.Option) message.Message {
	return message.Message{Code: codes.POST, Token: []byte{1}, Options: sorted(append(message.Options{{ID: message.URIPath, Value: []byte(path)}}, opts...))}
}

// The Responder demands Echo on the first use of a context, then serves;
// a peer with a new kid context runs Appendix B.2 and ends on ID Context
// R2||R3; RoundTrip retries once on a 4.01 with Echo.
func TestResponderEchoAndAppendixB2(t *testing.T) {
	base := Params{MasterSecret: bytes.Repeat([]byte{7}, 16), SenderID: []byte("c"), RecipientID: []byte("s")}
	var r Responder
	if err := r.Put("ep", base.Reverse()); err != nil {
		t.Fatal(err)
	}
	cl, _ := New(base)
	serve := func(c *Context, m message.Message) (message.Message, error) {
		prot, _, err := c.ProtectRequest(m)
		if err != nil {
			t.Fatal(err)
		}
		q, reply := r.Verify(prot)
		if q == nil {
			return reply, nil
		}
		return q.Protect(message.Message{Code: codes.Changed})
	}
	// RoundTrip: 4.01+Echo, retried with Echo, 2.04.
	in, err := RoundTrip(cl, req("bs"), func(prot message.Message, _ *Exchange) (message.Message, error) {
		q, reply := r.Verify(prot)
		if q == nil {
			return reply, nil
		}
		return q.Protect(message.Message{Code: codes.Changed})
	})
	if err != nil || in.Code != codes.Changed {
		t.Fatalf("round trip: %v %v", in.Code, err)
	}

	// Appendix B.2.
	p1 := base
	p1.IDContext, p1.SendKIDContext = []byte("R1R1R1R1"), true
	c1, _ := New(p1)
	prot, x, _ := c1.ProtectRequest(req("bs"))
	_, reply := r.Verify(prot)
	v, _ := reply.Options.GetBytes(OptionOSCORE)
	h, _ := ParseHeader(v)
	if reply.Code != codes.Changed || len(h.KIDContext) != 8 {
		t.Fatalf("response #1: %v %+v", reply.Code, h)
	}
	p2 := base
	p2.IDContext = append(append([]byte{}, h.KIDContext...), p1.IDContext...)
	c2, _ := New(p2)
	inner, err := c2.UnprotectResponse(reply, x)
	echo, eerr := inner.Options.GetBytes(OptionEcho)
	if err != nil || inner.Code != codes.Unauthorized || eerr != nil {
		t.Fatalf("response #1 verify: %v %v", err, inner.Code)
	}
	p3 := base
	p3.IDContext, p3.SendKIDContext = append(append([]byte{}, h.KIDContext...), "R3R3R3R3"...), true
	c3, _ := New(p3)
	resp, _ := serve(c3, req("bs", message.Option{ID: OptionEcho, Value: echo}))
	if _, ok := get(resp.Options, OptionOSCORE); !ok || resp.Code != codes.Changed {
		t.Fatalf("request #2: %v", resp.Code)
	}
	e, _ := r.Get("ep")
	if !bytes.Equal(e.Context().Params().IDContext, p3.IDContext) {
		t.Fatal("context not replaced")
	}
	// The pre-B.2 context is gone; an unknown kid is 4.01 unprotected.
	other, _ := New(Params{MasterSecret: base.MasterSecret, SenderID: []byte("z"), RecipientID: []byte("s")})
	if resp, _ := serve(other, req("bs")); resp.Code != codes.Unauthorized || string(resp.Payload) != "Security context not found" {
		t.Fatalf("unknown kid: %v %q", resp.Code, resp.Payload)
	}
}
