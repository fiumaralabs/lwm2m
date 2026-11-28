package server_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec/tlv"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type autoCreatePeer struct{ req *server.Message }

func (p *autoCreatePeer) Exchange(_ context.Context, req *server.Message) (*server.Message, error) {
	p.req = req
	return &server.Message{Code: codes.Created, Location: []string{"3303", "7"}}, nil
}
func (*autoCreatePeer) Identity() server.Identity {
	return server.Identity{Mode: server.ModeNoSec, Addr: "fake"}
}
func (*autoCreatePeer) RemoteAddr() net.Addr { return &net.UDPAddr{} }
func (*autoCreatePeer) Binding() string      { return "U" }

func TestCreateClientAssignedInstance(t *testing.T) {
	h := newHarness(t, func(c *server.Config) { c.Validator = nil })
	peer := &autoCreatePeer{}
	reg := &server.Registration{ID: "auto", Endpoint: "auto", Version: "1.1", Objects: []link.Object{{ID: 3303}}, RegisteredAt: time.Now(), LastUpdate: time.Now()}
	server.SetPeer(reg, peer)
	h.srv.Store().Add(reg)
	r, err := h.srv.Create(h.ctx, "auto", p("/3303"), []lwm2m.Node{lwm2m.ValueNode(p("/3303/5700"), lwm2m.Float(23))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Location) != 2 || r.Location[0] != "3303" || r.Location[1] != "7" {
		t.Fatalf("location: %v", r.Location)
	}
	if peer.req.Path != "/3303" || peer.req.Code != codes.POST || peer.req.Format == nil || *peer.req.Format != lwm2m.FormatTLV {
		t.Fatalf("request: %+v", peer.req)
	}
	records, err := tlv.ParseRaw(peer.req.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != tlv.ResourceValue || records[0].ID != 5700 {
		t.Fatalf("records: %+v", records)
	}
	_, err = h.srv.Create(h.ctx, "auto", p("/3303"), []lwm2m.Node{
		lwm2m.ValueNode(p("/3303/5700"), lwm2m.Float(23)),
		lwm2m.ValueNode(p("/3303/0/5701"), lwm2m.String("C")),
	}, nil)
	if err == nil {
		t.Fatal("ambiguous mixed paths were accepted")
	}
}
