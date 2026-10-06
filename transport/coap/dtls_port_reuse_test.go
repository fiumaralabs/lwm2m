package coap

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"

	piondtls "github.com/fiumaralabs/dtls/v3"
	"github.com/fiumaralabs/lwm2m/internal/dtlscoap"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Proves: SEC-11, REG-12 (RFC 6347 §4.2.8)
// A client that reuses its local port for a new DTLS handshake (Anjay
// rebinds its last port after bootstrap, a re-Register or a reboot; a NAT
// can do the same) gets a new session, although the server still holds the
// old association for that address: the new epoch-0 ClientHello must start
// a handshake, not be swallowed by the old connection. The old association
// keeps the address until the new handshake completes. Found by interop/peers
// (Anjay bootstrap → DTLS DM re-registration).
func TestDTLSNewHandshakeFromSamePort(t *testing.T) {
	for _, cid := range []bool{false, true} {
		h := newHarness(t)
		if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "reuse", PSKIdentity: "reuse-id", PSKKey: []byte("0123456789abcdef")}); err != nil {
			t.Fatal(err)
		}
		raddr, _ := net.ResolveUDPAddr("udp", h.dtls)
		cfg := &piondtls.Config{
			PSK:             func([]byte) ([]byte, error) { return []byte("0123456789abcdef"), nil },
			PSKIdentityHint: []byte("reuse-id"),
			CipherSuites:    []piondtls.CipherSuiteID{piondtls.TLS_PSK_WITH_AES_128_CCM_8},
		}
		if cid {
			cfg.ConnectionIDGenerator = piondtls.OnlySendCIDGenerator()
		}
		register := func(pc net.PacketConn) string {
			t.Helper()
			dc, err := piondtls.Client(pc, raddr, cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
			defer cancel()
			if err := dc.HandshakeContext(ctx); err != nil {
				t.Fatalf("cid=%v: handshake from a reused port: %v", cid, err)
			}
			cc := dtlscoap.Client(dc)
			req, err := cc.NewPostRequest(ctx, "/rd", message.AppLinkFormat, bytes.NewReader([]byte("</3/0>")))
			if err != nil {
				t.Fatal(err)
			}
			req.AddQuery("ep=reuse")
			req.AddQuery("lt=60")
			req.AddQuery("lwm2m=1.1")
			resp, err := cc.Do(req)
			if err != nil || resp.Code() != codes.Created {
				t.Fatalf("cid=%v: register: %v %v", cid, resp, err)
			}
			loc, _ := resp.Options().LocationPath()
			return loc
		}
		pc1, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		laddr := pc1.LocalAddr().(*net.UDPAddr)
		first := register(pc1)
		_ = pc1.Close() // the client forgets the session: no close_notify
		pc2, err := net.ListenUDP("udp", laddr)
		if err != nil {
			t.Fatal(err)
		}
		second := register(pc2)
		_ = pc2.Close()
		if first == second {
			t.Fatalf("cid=%v: second Register kept location %s", cid, first)
		}
	}
}
