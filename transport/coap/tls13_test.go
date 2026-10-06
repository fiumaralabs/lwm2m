package coap

import (
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"

	"github.com/fiumaralabs/lwm2m/testclient"
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
