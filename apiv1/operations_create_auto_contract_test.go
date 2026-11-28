package apiv1

import (
	"context"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/codec/tlv"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"net"
	"strings"
	"testing"
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

func TestOperationCreateClientAssignedInstance(t *testing.T) {
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
	peer := &autoCreatePeer{}
	resp, after := srv.HandleUplink(peer, &server.Message{Code: codes.POST, Path: "/rd", Query: []string{"ep=pump", "lwm2m=1.1", "lt=300"}, Payload: []byte("</3303>")})
	after()
	if resp.Code != codes.Created {
		t.Fatalf("register: %+v", resp)
	}
	w := deviceRequest(h, "POST", "/v1/devices/pump/operations", `{"type":"create","path":"/3303","values":[{"path":"/3303/5700","type":"float","value":23}]}`)
	if w.Code != 202 {
		t.Fatalf("POST: %d %s", w.Code, w.Body.String())
	}
	w = deviceRequest(h, "GET", w.Header().Get("Location")+"?wait=2", "")
	if w.Code != 200 || !containsStatus(w.Body.Bytes(), operations.Succeeded) || !strings.Contains(w.Body.String(), `"location":"/3303/7"`) {
		t.Fatalf("result: %d %s", w.Code, w.Body.String())
	}
	if peer.req == nil || peer.req.Format == nil || *peer.req.Format != lwm2m.FormatTLV {
		t.Fatalf("wrong create format: %+v", peer.req)
	}
	records, err := tlv.ParseRaw(peer.req.Payload)
	if err != nil || len(records) != 1 || records[0].Type != tlv.ResourceValue || records[0].ID != 5700 {
		t.Fatalf("wrong create payload: %+v %v", records, err)
	}
}
