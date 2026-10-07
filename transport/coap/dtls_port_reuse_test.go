package coap

import (
	"bytes"
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"

	"github.com/fiumaralabs/lwm2m/internal/dtlscoap"
	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/crypto/ciphersuite"
	"github.com/pion/dtls/v4/pkg/protocol"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	udpClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

// Proves: SEC-11, REG-12 (RFC 6347 §4.2.8)
// A client that reuses its local port for a new DTLS handshake (Anjay
// rebinds its last port after bootstrap, a re-Register or a reboot; a NAT
// can do the same) gets a new session, although the server still holds the
// old association for that address: the new epoch-0 ClientHello must start
// a handshake, not be swallowed by the old connection. The old association
// keeps the address until the new handshake completes; a ClientHello that
// never completes (spoofed from the client's address) does not break it,
// and a later one with another random replaces it. Found by interop/peers
// (Anjay bootstrap → DTLS DM re-registration). pion/dtls drops such a
// ClientHello as a replay; internal/dtlscoap has its own demux.
func TestDTLSNewHandshakeFromSamePort(t *testing.T) {
	cidOpt := piondtls.WithConnectionID(piondtls.OnlySendCIDGenerator(), piondtls.CIDPathMigrationReject)
	for _, tc := range []struct {
		name string
		v13  bool
		cid  bool
	}{{"1.2", false, false}, {"1.2/CID", false, true}, {"1.3", true, false}, {"1.3/CID", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSecure(t)
			if err := s.srv.Security().Put(server.SecurityInfo{Endpoint: "reuse", PSKIdentity: "reuse-id", PSKKey: []byte("0123456789abcdef")}); err != nil {
				t.Fatal(err)
			}
			opts := pskConfig("reuse-id", []byte("0123456789abcdef"))
			if tc.v13 { // pion has no DTLS 1.3 PSK yet: certificates
				opts = with(testclient.X509Config(s.x509Client("reuse"), s.pki.pool, "lwm2m.test", ciphersuite.TLS_AES_128_GCM_SHA256), dtls13...)
			}
			if tc.cid {
				opts = with(opts, cidOpt)
			}
			raddr, _ := net.ResolveUDPAddr("udp", s.addr)
			register := func(pc net.PacketConn) (string, *udpClient.Conn) {
				t.Helper()
				dc, err := piondtls.Client(pc, raddr, opts...)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
				defer cancel()
				if err := dc.HandshakeContext(ctx); err != nil {
					t.Fatalf("handshake from a reused port: %v", err)
				}
				if st, _ := dc.ConnectionState(); (st.NegotiatedVersion() == protocol.Version1_3) != tc.v13 {
					t.Fatalf("negotiated %v", st.NegotiatedVersion())
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
					t.Fatalf("register: %v %v", resp, err)
				}
				loc, _ := resp.Options().LocationPath()
				return loc, cc
			}
			pc1, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			laddr := pc1.LocalAddr().(*net.UDPAddr)
			first, cc1 := register(pc1)

			// A ClientHello that never completes, from the client's own
			// address: the established session keeps working.
			for _, d := range firstFlight(t, raddr, opts) {
				if _, err := pc1.WriteTo(d, raddr); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(100 * time.Millisecond)
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			defer cancel()
			if resp, err := cc1.Post(ctx, first, message.AppLinkFormat, bytes.NewReader(nil)); err != nil || resp.Code() != codes.Changed {
				t.Fatalf("Update after a stray ClientHello: %v %v", resp, err)
			}
			_ = pc1.Close() // the client forgets the session: no close_notify

			pc2, err := net.ListenUDP("udp", laddr)
			if err != nil {
				t.Fatal(err)
			}
			defer pc2.Close()
			second, _ := register(pc2) // another random replaces the stuck attempt
			if first == second {
				t.Fatalf("second Register kept location %s", first)
			}
		})
	}
}

// firstFlight returns the datagrams of a client's first flight (the
// ClientHello; DTLS 1.3 key shares may need two), captured without
// sending them.
func firstFlight(t *testing.T, raddr net.Addr, opts []piondtls.ClientOption) [][]byte {
	t.Helper()
	pc := &capture{closed: make(chan struct{})}
	dc, err := piondtls.Client(pc, raddr, with(opts, piondtls.WithFlightInterval(time.Hour))...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = dc.HandshakeContext(ctx)
	_ = dc.Close()
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if len(pc.out) == 0 {
		t.Fatal("no ClientHello captured")
	}
	return pc.out
}

// capture is a net.PacketConn that records writes and never delivers.
type capture struct {
	mu     sync.Mutex
	out    [][]byte
	closed chan struct{}
	once   sync.Once
}

func (c *capture) ReadFrom([]byte) (int, net.Addr, error) { <-c.closed; return 0, nil, net.ErrClosed }
func (c *capture) WriteTo(b []byte, _ net.Addr) (int, error) {
	c.mu.Lock()
	c.out = append(c.out, append([]byte(nil), b...))
	c.mu.Unlock()
	return len(b), nil
}
func (c *capture) Close() error                     { c.once.Do(func() { close(c.closed) }); return nil }
func (c *capture) LocalAddr() net.Addr              { return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)} }
func (c *capture) SetDeadline(t time.Time) error    { return c.SetReadDeadline(t) }
func (c *capture) SetWriteDeadline(time.Time) error { return nil }

// SetReadDeadline closes c once a deadline is set: pion cancels reads that way.
func (c *capture) SetReadDeadline(t time.Time) error {
	if !t.IsZero() {
		_ = c.Close()
	}
	return nil
}
