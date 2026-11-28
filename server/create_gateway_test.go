package server_test

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec/tlv"
	"github.com/fiumaralabs/lwm2m/link"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type createGatewayPeer struct {
	requests []*server.Message
	table    []byte
}

func (g *createGatewayPeer) Exchange(_ context.Context, req *server.Message) (*server.Message, error) {
	g.requests = append(g.requests, req)
	if req.Path == "/25" {
		f := lwm2m.FormatTLV
		return &server.Message{Code: codes.Content, Format: &f, Payload: g.table}, nil
	}
	return &server.Message{Code: codes.Created, Location: []string{"d01", "3303", "7"}}, nil
}
func (*createGatewayPeer) Identity() server.Identity {
	return server.Identity{Mode: server.ModeNoSec, Addr: "fake"}
}
func (*createGatewayPeer) RemoteAddr() net.Addr { return &net.UDPAddr{} }
func (*createGatewayPeer) Binding() string      { return "U" }

func TestCreateGatewayPrefix(t *testing.T) {
	h := newHarness(t, func(c *server.Config) { c.Validator = nil })
	table, err := (tlv.Codec{}).Encode(p("/25"), []lwm2m.Node{lwm2m.ValueNode(p("/25/0/1"), lwm2m.String("d01"))})
	if err != nil {
		t.Fatal(err)
	}
	peer := &createGatewayPeer{table: table}
	reg := &server.Registration{ID: "gw", Endpoint: "gw", Version: "1.1", Objects: []link.Object{{ID: 25}}, RegisteredAt: time.Now(), LastUpdate: time.Now()}
	server.SetPeer(reg, peer)
	h.srv.Store().Add(reg)
	for _, path := range []string{"/3303/0/5700", "/3303/5700"} {
		t.Run(path, func(t *testing.T) {
			nodes := []lwm2m.Node{lwm2m.ValueNode(p(path), lwm2m.Float(23))}
			r, err := h.srv.CreateWithPrefix(h.ctx, "gw", p("/3303"), nodes, nil, "d01")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.Location, []string{"d01", "3303", "7"}) {
				t.Fatalf("location: %v", r.Location)
			}
			req := peer.requests[len(peer.requests)-1]
			if req.Path != "/d01/3303" || req.Code != codes.POST {
				t.Fatalf("request: %+v", req)
			}
			if _, err := h.srv.CreateWithPrefix(h.ctx, "gw", p("/3303"), nodes, nil, "unknown"); !errors.Is(err, server.ErrBadRequest) {
				t.Fatalf("unknown prefix: %v", err)
			}
			if req := peer.requests[len(peer.requests)-1]; req.Path != "/25" {
				t.Fatalf("unknown sent: %s", req.Path)
			}
		})
	}
}
