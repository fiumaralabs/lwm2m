package coap

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/gorilla/websocket"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// wsHarness mounts WebSocketHandler on a plain (ws://) and a TLS (wss://)
// http server.
type wsHarness struct {
	*harness
	ws, wss string
	pki     *pki
}

func newWSHarness(t *testing.T) *wsHarness {
	t.Helper()
	h := &wsHarness{harness: newHarness(t), pki: newPKI(t)}
	plain := httptest.NewServer(h.b.WebSocketHandler())
	t.Cleanup(plain.Close)
	sec := httptest.NewUnstartedServer(h.b.WebSocketHandler())
	sec.TLS = &tls.Config{Certificates: []tls.Certificate{h.pki.server}}
	sec.StartTLS()
	t.Cleanup(sec.Close)
	h.ws = "ws" + strings.TrimPrefix(plain.URL, "http") + "/"
	h.wss = "ws" + strings.TrimPrefix(sec.URL, "http") + "/"
	return h
}

func (h *wsHarness) registered(ep string, overTLS bool) *testclient.TCPClient {
	h.t.Helper()
	c := testclient.NewTCP(testclient.Config{Endpoint: ep, Binding: "T"})
	c.Set(p("/1/0/0"), lwm2m.Integer(1))
	c.Set(p("/1/0/1"), lwm2m.Integer(86400))
	c.Set(p("/1/0/7"), lwm2m.String("T"))
	c.Set(p("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	c.Set(p("/3/0/9"), lwm2m.Integer(100))
	c.Set(p("/3/0/16"), lwm2m.String("T"))
	url, tc := h.ws, (*tls.Config)(nil)
	if overTLS {
		url, tc = h.wss, &tls.Config{RootCAs: h.pki.pool}
	}
	if _, err := c.DialWebSocket(url, tc); err != nil { // fails without the server CSM
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = c.Close() })
	r, err := c.Register(h.ctx)
	mustCode(h.t, r, err, "2.01")
	return c
}

// Proves: TCP-03
// Over coap+ws and coaps+ws (RFC 8323 §4) the server sends its CSM first
// and answers Ping with Pong; Register, Update and De-register work, the
// session is binding T, Read and Write go back over the same WebSocket,
// and Observe delivers notifications without ACK (any Observe value) until
// an explicit cancel, GET with Observe=1 on the token (RFC 8323 §7).
func TestWebSocketRegistrationDMObserve(t *testing.T) {
	for _, overTLS := range []bool{false, true} {
		name := map[bool]string{false: "ws", true: "wss"}[overTLS]
		t.Run(name, func(t *testing.T) {
			h := newWSHarness(t)
			c := h.registered("ws-ep", overTLS)
			if err := c.Ping(h.ctx); err != nil {
				t.Fatalf("ping: %v", err)
			}
			reg, ok := h.srv.Store().ByEndpoint("ws-ep")
			if !ok || reg.Peer().Binding() != "T" || reg.Binding != "T" {
				t.Fatalf("registration %+v", reg)
			}

			r := readOK(t, h.harness, "ws-ep", p("/3/0/9"))
			if len(r.Nodes) != 1 || !r.Nodes[0].Value.Equal(lwm2m.Integer(100)) {
				t.Fatalf("read %+v", r)
			}
			if req, _ := c.LastRequest(); req.Code != codes.GET || req.Path != "/3/0/9" {
				t.Fatalf("client got %+v", req)
			}
			w, err := h.srv.Write(h.ctx, "ws-ep", p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(300))}, server.WriteOptions{})
			mustResp(t, w, err, "2.04")
			if v, _ := c.Get(p("/1/0/1")); !v.Equal(lwm2m.Integer(300)) {
				t.Fatalf("write not applied: %v", v)
			}

			ob, or, err := h.srv.Observe(h.ctx, "ws-ep", p("/3/0/9"), server.ObserveOptions{})
			if err != nil || ob == nil || !or.Success() {
				t.Fatalf("observe: %v %+v", err, or)
			}
			oreq, _ := c.LastRequest()
			if oreq.Observe == nil || *oreq.Observe != 0 {
				t.Fatalf("observe request %+v", oreq)
			}
			for i, seq := range []uint32{5, 3} { // 3 would be stale over UDP
				c.Set(p("/3/0/9"), lwm2m.Integer(int64(10+i)))
				if err := c.Notify(h.ctx, oreq.Token, seq); err != nil {
					t.Fatal(err)
				}
				n := notification(t, h.harness, ob)
				if !n.Response.Nodes[0].Value.Equal(lwm2m.Integer(int64(10 + i))) {
					t.Fatalf("notification %d: %+v", i, n.Response)
				}
			}
			cr, err := h.srv.CancelObservation(h.ctx, ob, true)
			mustResp(t, cr, err, "2.05")
			if last, _ := c.LastRequest(); last.Code != codes.GET || last.Observe == nil || *last.Observe != 1 || !bytes.Equal(last.Token, oreq.Token) {
				t.Fatalf("cancel request %+v", last)
			}

			ur, err := c.Update(h.ctx, []string{"lt=600"})
			mustCode(t, ur, err, "2.04")
			h.ev.wait(t, func(e server.Event) bool {
				u, ok := e.(server.Updated)
				return ok && u.Registration.Endpoint == "ws-ep"
			})
			dr, err := c.Deregister(h.ctx)
			mustCode(t, dr, err, "2.02")
			if _, ok := h.srv.Store().ByEndpoint("ws-ep"); ok {
				t.Fatal("still registered")
			}
		})
	}
}

// Proves: TCP-03
// The handshake needs the "coap" subprotocol (RFC 8323 §4.1): without it
// the server answers 400, with it the server selects "coap". On the wire
// each WebSocket binary message is one CoAP message with a zero Len nibble
// and no length, MID or Type (RFC 8323 §4.2): the server's first message
// is its CSM (7.01), and a raw Ping (7.02) gets a Pong (7.03) echoing the
// token (§5.4).
func TestWebSocketSubprotocolAndFraming(t *testing.T) {
	h := newWSHarness(t)
	if _, hr, err := websocket.DefaultDialer.Dial(h.ws, nil); err == nil || hr == nil || hr.StatusCode != http.StatusBadRequest {
		t.Fatalf("dial without subprotocol: %v %+v", err, hr)
	}
	d := websocket.Dialer{Subprotocols: []string{"mqtt", "coap"}}
	ws, _, err := d.Dial(h.ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if ws.Subprotocol() != "coap" {
		t.Fatalf("subprotocol %q", ws.Subprotocol())
	}
	_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	typ, m, err := ws.ReadMessage()
	if err != nil || typ != websocket.BinaryMessage || len(m) < 2 || m[0]>>4 != 0 || codes.Code(m[1]) != codes.CSM {
		t.Fatalf("first message %d % x %v, want binary CSM", typ, m, err)
	}
	// Our CSM (no options, empty token), then Ping with token 0xAB.
	for _, out := range [][]byte{{0x00, byte(codes.CSM)}, {0x01, byte(codes.Ping), 0xAB}} {
		if err := ws.WriteMessage(websocket.BinaryMessage, out); err != nil {
			t.Fatal(err)
		}
	}
	_, m, err = ws.ReadMessage()
	if err != nil || !bytes.Equal(m, []byte{0x01, byte(codes.Pong), 0xAB}) {
		t.Fatalf("pong % x %v", m, err)
	}
}
