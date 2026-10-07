package coap

import (
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"

	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/pion/dtls/v4/pkg/crypto/ciphersuite"
)

// Proves: TLS13-01
// TLS 1.3 (RFC 8446) is used on the TLS binding: a listener requiring TLS
// 1.3 registers a TLS 1.3 client and refuses a TLS 1.2-only one.
func TestTLS13Binding(t *testing.T) {
	h := newTCPHarness(t)
	a, err := h.b.ListenTLS("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{h.pki.server},
		ClientCAs: h.pki.pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "tls13", X509: true}); err != nil {
		t.Fatal(err)
	}
	leaf := h.pki.leaf(t, "tls13", x509.ExtKeyUsageClientAuth)
	c := testclient.NewTCP(testclient.Config{Endpoint: "tls13"})
	if err := c.Dial(a.String(), &tls.Config{RootCAs: h.pki.pool, Certificates: []tls.Certificate{leaf}, MinVersion: tls.VersionTLS13, NextProtos: []string{"coap"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	r, err := c.Register(h.ctx)
	mustCode(t, r, err, "2.01")

	old := testclient.NewTCP(testclient.Config{Endpoint: "tls13"})
	if err := old.Dial(a.String(), &tls.Config{RootCAs: h.pki.pool, Certificates: []tls.Certificate{leaf}, MaxVersion: tls.VersionTLS12, NextProtos: []string{"coap"}}); err == nil {
		_ = old.Close()
		t.Fatal("TLS 1.2 client accepted on a TLS 1.3 listener")
	}
}

// Proves: TLS13-05
// /0/x/22 values: reserved bits are refused, and only the TLS 1.3
// features the server offers (certificate authentication) may be set.
func TestTLS13Features(t *testing.T) {
	if CheckTLS13Features(0) != nil || CheckTLS13Features(uint32(TLS13Certificate)) != nil {
		t.Fatal("valid values refused")
	}
	for _, v := range []uint32{1 << 4, 1 << 31, uint32(TLS13PSK), uint32(TLS13ZeroRTT), uint32(TLS13PSKWithPFS | TLS13Certificate)} {
		if CheckTLS13Features(v) == nil {
			t.Errorf("%#x accepted", v)
		}
	}
}

// Proves: TLS13-01, CID-01, CID-03
// DTLS 1.3 (RFC 9147) runs next to DTLS 1.2 on the same listener (pion
// negotiates; every other DTLS test is 1.2): a client limited to 1.3
// registers with an X.509 certificate (pion/dtls has no DTLS 1.3 external
// PSK yet, so /0/x/22 offers certificate authentication only, TLS13-05).
// It asks for a DTLS 1.3 Connection ID: its records carry the server's
// CID in the unified header, and after a NAT rebinding its Update is
// accepted without a new handshake and the next downlink goes to the new
// address.
func TestDTLS13WithCID(t *testing.T) {
	s := newSecure(t)
	xc := s.x509Client("d13")
	px := newProxy(t, s.addr)
	opts := with(testclient.X509Config(xc, s.pki.pool, "lwm2m.test", ciphersuite.TLS_AES_128_GCM_SHA256), dtls13...)
	c := s.mustRegisterSecure(testclient.Config{Endpoint: "d13", CID: true}, px.Addr(), opts)
	reg, _ := s.srv.Store().ByEndpoint("d13")
	if reg.Identity.Mode != server.ModeX509 {
		t.Fatalf("identity %+v", reg.Identity)
	}
	// RFC 9147 §4: unified header 001CSLEE, C set and an 8-byte CID.
	unifiedCID := func(d datagram) bool { return d.data[0]&0xe0 == 0x20 && d.data[0]&0x10 != 0 && len(d.data) > 9 }
	saw := false
	for _, d := range px.since(0) {
		saw = saw || d.up && unifiedCID(d)
	}
	if !saw {
		t.Fatal("no DTLS 1.3 records with the server's CID")
	}

	before := reg.Addr.String()
	px.rebind()
	m := px.mark()
	u, err := c.Update(s.ctx, nil, nil)
	mustCode(t, u, err, "2.04")
	reg, _ = s.srv.Store().ByEndpoint("d13")
	if reg.Addr.String() != px.UpstreamAddr() || reg.Addr.String() == before {
		t.Fatalf("registration address %s, want the rebound %s", reg.Addr, px.UpstreamAddr())
	}
	resp, err := s.srv.Read(s.ctx, "d13", p("/3/0/0"), server.ReadOptions{})
	mustResp(t, resp, err, "2.05")
	for _, d := range px.since(m) {
		if d.data[0] == recHandshake {
			t.Fatal("plaintext handshake after rebinding")
		}
	}
}
