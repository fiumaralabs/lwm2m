package server

import (
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// gatewayClient registers a client with one /25 instance: a gateway.
func gatewayClient(h *harness, ep string) *testclient.Client {
	h.t.Helper()
	c := h.device(testclient.Config{Endpoint: ep})
	c.Set(p("/25/0/0"), lwm2m.String("urn:dev:ops:d01"))
	c.Set(p("/25/0/1"), lwm2m.String("d01"))
	mustCode(mustRegister(h, c))
	return c
}

// Proves: SEND-03, CBOR-11, GW-08
// A gateway's Send may carry objects of its end devices, prefixed in the
// names (SenML) or keys (LwM2M CBOR), although they were never registered:
// 2.04, delivered with Prefix. Unprefixed nodes still follow SEND-02, and a
// client without /25 has no end devices (4.04).
func TestGatewaySendPrefixed(t *testing.T) {
	h := newHarness(t)
	gw := gatewayClient(h, "gw")
	nodes := []lwm2m.Node{
		lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(80)),
		{Prefix: "d01", Path: p("/3/0/0"), Value: lwm2m.String("Company A")},
		{Prefix: "d01", Path: p("/3303/0/5700"), Value: lwm2m.Float(22.5)},
	}
	for _, cf := range []lwm2m.ContentFormat{lwm2m.FormatSenMLJSON, lwm2m.FormatSenMLCBOR, lwm2m.FormatLwM2MCBOR} {
		r, err := gw.Send(h.ctx, nodes, cf)
		mustCode(t, r, err, "2.04")
		ev := h.ev.wait(t, func(e Event) bool { _, ok := e.(SendReceived); return ok }).(SendReceived)
		if ev.ContentFormat != cf || !lwm2m.NodesEqual(ev.Nodes, nodes) {
			t.Fatalf("%v: event\n%s", cf, lwm2m.FormatNodes(ev.Nodes))
		}
	}
	// The spec's own example, {["d01",3,0]: {0:"Company A", 9:100}} (GW §9).
	cf := lwm2m.FormatLwM2MCBOR
	body := []byte{0xa1, 0x83, 0x63, 'd', '0', '1', 3, 0, 0xa2, 0, 0x69, 'C', 'o', 'm', 'p', 'a', 'n', 'y', ' ', 'A', 9, 0x18, 100}
	r, err := gw.Raw(h.ctx, codes.POST, "/dp", nil, &cf, body)
	mustCode(t, r, err, "2.04")
	ev := h.ev.wait(t, func(e Event) bool { _, ok := e.(SendReceived); return ok }).(SendReceived)
	if want := []lwm2m.Node{
		{Prefix: "d01", Path: p("/3/0/0"), Value: lwm2m.String("Company A")},
		{Prefix: "d01", Path: p("/3/0/9"), Value: lwm2m.Integer(100)},
	}; !lwm2m.NodesEqual(ev.Nodes, want) {
		t.Fatalf("spec example decoded to\n%s", lwm2m.FormatNodes(ev.Nodes))
	}
	r, err = gw.Send(h.ctx, []lwm2m.Node{lwm2m.ValueNode(p("/3303/0/5700"), lwm2m.Float(1))}, lwm2m.FormatSenMLCBOR)
	mustCode(t, r, err, "4.04") // the gateway itself registered no /3303
	plain := h.registered("plain")
	r, err = plain.Send(h.ctx, nodes[1:2], lwm2m.FormatSenMLCBOR)
	mustCode(t, r, err, "4.04")
}

// Proves: GW-05, GW-08, CBOR-11
// Read and Observe with an end-device prefix go out as GET /d01/3/0...
// (the prefix as an alternate path, after the gateway's own one); the
// response and the notifications decode with that Prefix, also in formats
// without names. A node of another device in the response is a decode
// error; a prefix is refused for a client that is not a gateway.
func TestGatewayEndDeviceReadObserve(t *testing.T) {
	h := newHarness(t)
	gw := gatewayClient(h, "gw")
	gw.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		var cf lwm2m.ContentFormat
		var ns []lwm2m.Node
		switch r.Path {
		case "/d01/3/0":
			cf = lwm2m.FormatTLV // no names: the request prefix applies
			ns = []lwm2m.Node{lwm2m.ValueNode(p("/3/0/0"), lwm2m.String("Company A"))}
		case "/d01/3/0/9":
			cf = lwm2m.FormatLwM2MCBOR
			ns = []lwm2m.Node{{Prefix: "d01", Path: p("/3/0/9"), Value: lwm2m.Integer(100)}}
		default:
			return 0, nil, nil, false
		}
		c, _ := codec.For(cf)
		b, err := c.Encode(p(r.Path[len("/d01"):]), ns)
		if err != nil {
			t.Error(err)
		}
		return codes.Content, &cf, b, true
	})
	tlv := lwm2m.FormatTLV
	r, err := h.srv.Read(h.ctx, "gw", p("/3/0"), ReadOptions{Prefix: "d01", Accept: &tlv})
	mustResp(t, r, err, "2.05")
	want := []lwm2m.Node{{Prefix: "d01", Path: p("/3/0/0"), Value: lwm2m.String("Company A")}}
	if r.DecodeErr != nil || !lwm2m.NodesEqual(r.Nodes, want) {
		t.Fatalf("read: %v\n%s", r.DecodeErr, lwm2m.FormatNodes(r.Nodes))
	}
	if req, _ := gw.LastRequest(); req.Path != "/d01/3/0" {
		t.Fatalf("read went to %s", req.Path)
	}

	ob, r, err := h.srv.Observe(h.ctx, "gw", p("/3/0/9"), ObserveOptions{Prefix: "d01"})
	mustResp(t, r, err, "2.05")
	if req, _ := gw.LastRequest(); req.Path != "/d01/3/0/9" || req.Observe == nil || *req.Observe != 0 {
		t.Fatalf("observe went to %s", req.Path)
	}
	if ob.Prefix != "d01" || !lwm2m.NodesEqual(r.Nodes, []lwm2m.Node{{Prefix: "d01", Path: p("/3/0/9"), Value: lwm2m.Integer(100)}}) {
		t.Fatalf("observe: %+v\n%s", ob, lwm2m.FormatNodes(r.Nodes))
	}
	if _, err := gw.NotifyRaw(h.ctx, ob.token, lwm2m.FormatSenMLJSON, []byte(`[{"n":"/d01/3/0/9","v":55}]`)); err != nil {
		t.Fatal(err)
	}
	n := notification(t, h, ob)
	if n.Response.DecodeErr != nil || !lwm2m.NodesEqual(n.Response.Nodes, []lwm2m.Node{{Prefix: "d01", Path: p("/3/0/9"), Value: lwm2m.Integer(55)}}) {
		t.Fatalf("notification: %v\n%s", n.Response.DecodeErr, lwm2m.FormatNodes(n.Response.Nodes))
	}
	if _, err := gw.NotifyRaw(h.ctx, ob.token, lwm2m.FormatSenMLJSON, []byte(`[{"n":"/d02/3/0/9","v":1}]`)); err != nil {
		t.Fatal(err)
	}
	if n := notification(t, h, ob); n.Response.DecodeErr == nil {
		t.Fatalf("node of d02 in a d01 notification accepted: %s", lwm2m.FormatNodes(n.Response.Nodes))
	}
	if _, err := h.srv.CancelObservation(h.ctx, ob, true); err != nil {
		t.Fatal(err)
	}
	if req, _ := gw.LastRequest(); req.Path != "/d01/3/0/9" || req.Observe == nil || *req.Observe != 1 {
		t.Fatalf("cancel went to %s", req.Path)
	}

	h.registered("plain")
	if _, err := h.srv.Read(h.ctx, "plain", p("/3/0"), ReadOptions{Prefix: "d01"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("prefix on a non-gateway: %v", err)
	}
	if _, _, err := h.srv.Observe(h.ctx, "gw", p("/3/0"), ObserveOptions{Prefix: "42"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("numeric prefix: %v", err)
	}
	if got := uriPath(&Registration{RootPath: "/lwm2m"}, "d01", p("/3303/0")); got != "/lwm2m/d01/3303/0" {
		t.Fatalf("alternate path: %s", got)
	}
}
