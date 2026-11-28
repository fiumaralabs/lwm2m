package apiv1

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type locationGatewayPeer struct {
	req   *server.Message
	table []byte
}

func (p *locationGatewayPeer) Exchange(_ context.Context, req *server.Message) (*server.Message, error) {
	if req.Path == "/25" {
		f := lwm2m.FormatSenMLJSON
		return &server.Message{Code: codes.Content, Format: &f, Payload: p.table}, nil
	}
	p.req = req
	return &server.Message{Code: codes.Created, Location: []string{"d01", "3303", "7"}}, nil
}
func (*locationGatewayPeer) Identity() server.Identity {
	return server.Identity{Mode: server.ModeNoSec, Addr: "fake"}
}
func (*locationGatewayPeer) RemoteAddr() net.Addr { return &net.UDPAddr{} }
func (*locationGatewayPeer) Binding() string      { return "U" }

func TestOperationCreateGatewayAssignedLocation(t *testing.T) {
	keys := deviceKeys()
	entry := keys.entries["test"]
	entry.scopes += "operations:write,"
	keys.entries["test"] = entry
	st := memory.NewOperations()
	srv := server.New(server.Config{})
	defer srv.Close()
	executor := operations.NewExecutor(srv, st, operations.ExecutorOptions{})
	defer executor.Close()
	h := New(Options{Keys: keys, Server: srv, Operations: st, Executor: executor})
	deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`)
	c, err := codec.For(lwm2m.FormatSenMLJSON)
	if err != nil {
		t.Fatal(err)
	}
	table, err := c.Encode(lwm2m.MustParsePath("/25"), []lwm2m.Node{lwm2m.ValueNode(lwm2m.MustParsePath("/25/0/1"), lwm2m.String("d01"))})
	if err != nil {
		t.Fatal(err)
	}
	peer := &locationGatewayPeer{table: table}
	resp, after := srv.HandleUplink(peer, &server.Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=pump", "lwm2m=1.1", "lt=300"}, Payload: []byte("</25/0>")})
	after()
	if resp.Code != codes.Created {
		t.Fatalf("register: %+v", resp)
	}
	w := deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"create","prefix":"d01","path":"/3303","values":[{"path":"/3303/5700","type":"float","value":23}]}`)
	if w.Code != 202 {
		t.Fatalf("POST: %d %s", w.Code, w.Body.String())
	}
	w = deviceRequest(h, "GET", w.Header().Get("Location")+"?wait=2", "")
	if w.Code != 200 || !containsStatus(w.Body.Bytes(), operations.Succeeded) || !strings.Contains(w.Body.String(), `"location":"/d01/3303/7"`) {
		t.Fatalf("result: %d %s", w.Code, w.Body.String())
	}
	if peer.req == nil || peer.req.Path != "/d01/3303" {
		t.Fatalf("request: %+v", peer.req)
	}
}
