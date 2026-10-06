package bootstrap

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/est"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Bootstrapping over TLS (CoAP over TLS, binding T) with certificates:
// the endpoint is the verified client certificate's CN. Go's crypto/tls
// has no TLS-PSK or RPK, so those credentials are served over DTLS (see
// TestInt1BootstrapPSK, TestInt2BootstrapCertificateAndRPK) and OSCORE
// (TestOSCOREBootstrapPSKAppendixB2). The default PSK lookup refuses a
// low-entropy (D)TLS PSK: the handshake fails.
// Proves: BS-10
func TestBootstrapOverTLSAndWeakPSK(t *testing.T) {
	h := newHarness(t)
	k := newPKI(t)
	addr, err := h.bs.ListenTLS("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{k.leaf(t, "bs")},
		ClientCAs: k.pool, ClientAuth: tls.RequireAndVerifyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.sec.Put(server.SecurityInfo{Endpoint: "tls-ep", X509: true}); err != nil {
		t.Fatal(err)
	}
	h.put("tls-ep", c1("coaps+tcp://dm.example.com", "tls-ep", "dm-key-0123456789"))
	b := testclient.NewBootstrap(testclient.Config{Endpoint: "tls-ep"})
	c13(b, ModeX509, "", "")
	tc := &testclient.TCPClient{Client: b.Client}
	if err := tc.Dial(addr.String(), &tls.Config{Certificates: []tls.Certificate{k.leaf(t, "tls-ep")}, RootCAs: k.pool, NextProtos: []string{"coap"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tc.Close() })
	r, err := b.BootstrapRequestWith(func() (*testclient.Response, error) {
		return tc.Raw(h.ctx, codes.POST, "/bs", nil, nil, nil) // no ep: the CN
	})
	mustCode(t, r, err, codes.Changed)
	if fin, err := b.WaitFinish(h.ctx); err != nil || fin != codes.Changed {
		t.Fatalf("finish %s %v", server.CodeString(fin), err)
	}
	res := h.result()
	if res.Err != nil || res.Endpoint != "tls-ep" || res.Identity.Mode != server.ModeX509 {
		t.Fatalf("result %+v", res)
	}
	if uri, _ := b.Get(p("/0/0/0")); uri.Str != "coaps+tcp://dm.example.com" {
		t.Fatalf("not provisioned: %v", uri)
	}

	if err := h.sec.Put(server.SecurityInfo{Endpoint: "weak", PSKIdentity: "weak", PSKKey: []byte("password")}); err != nil {
		t.Fatal(err)
	}
	h.put("weak", c1("coap://dm.example.com", "weak", "dm-key-0123456789"))
	w := testclient.NewBootstrap(testclient.Config{Endpoint: "weak", PSKIdentity: "weak", PSKKey: []byte("password")})
	if err := w.Dial(h.dtls); err == nil {
		t.Cleanup(func() { _ = w.Close() })
		if r, err := w.BootstrapRequest(h.ctx, w.BootstrapQuery(nil)); err == nil {
			t.Fatalf("low-entropy PSK accepted: %s", server.CodeString(r.Code))
		}
	}
}

// Certificate mode with EST through the Bootstrap-Server (T §5.2.9.5): on
// its DTLS-PSK bootstrap session the client fetches the CA (/crts),
// enrolls a locally generated key (/sen) for its endpoint name, which the
// BS binds to the bootstrap credential (a CSR for another name is 4.03),
// then bootstraps: the /0 instance has Security Mode 4, the server
// certificate in /0/x/4 and no /0/x/3 or /0/x/5. It registers with the
// LwM2M Server in certificate mode with the EST certificate.
// Proves: SEC-08, EST-01, EST-02
func TestESTViaBootstrapServer(t *testing.T) {
	h := newHarness(t)
	ca, err := est.NewTestCA("EST CA")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.bs.MountEST(&est.Server{CA: ca}); err != nil {
		t.Fatal(err)
	}
	const ep = "urn:dev:est"
	if err := h.sec.Put(server.SecurityInfo{Endpoint: ep, PSKIdentity: "est-bs", PSKKey: []byte(bsKey)}); err != nil {
		t.Fatal(err)
	}

	// LwM2M Server with a certificate from the same CA.
	sk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	sder, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}}, sk)
	sreq, _ := x509.ParseCertificateRequest(sder)
	scert, err := ca.Issue(sreq)
	if err != nil {
		t.Fatal(err)
	}
	dm := server.New(server.Config{RequestTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = dm.Close() })
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	dmAddr, err := dm.ListenDTLS("127.0.0.1:0", server.DTLSConfig{Config: dm.DTLSConfig(server.CertificateModes{
		Certificates: []tls.Certificate{{Certificate: [][]byte{scert.Raw}, PrivateKey: sk}}, ClientCAs: pool})})
	if err != nil {
		t.Fatal(err)
	}
	if err := dm.Security().Put(server.SecurityInfo{Endpoint: ep, X509: true}); err != nil {
		t.Fatal(err)
	}
	h.put(ep, &BootstrapConfig{
		Security: map[uint16]SecurityConfig{0: {URI: "coaps://" + dmAddr.String(), SecurityMode: ModeEST, ServerPublicKey: scert.Raw, ServerID: u16(1)}},
		Servers:  map[uint16]ServerConfig{0: {ShortID: 1, Lifetime: 86400, Binding: "U"}},
	})

	c := h.client(ep, testclient.Config{PSKIdentity: "est-bs", PSKKey: []byte(bsKey)})
	r, err := c.Raw(h.ctx, codes.GET, est.Root+"/crts", nil, nil, nil)
	mustCode(t, r, err, codes.Content)
	if cas, err := est.ParseCertsOnly(r.Body); err != nil || len(cas) != 1 || !cas[0].Equal(ca.Cert) {
		t.Fatalf("crts: %v", err)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader) // generated on the device
	csr := func(cn string) []byte {
		der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	p10 := lwm2m.ContentFormat(est.FormatPKCS10)
	r, err = c.Raw(h.ctx, codes.POST, est.Root+"/sen", nil, &p10, csr("urn:dev:someone-else"))
	mustCode(t, r, err, codes.Forbidden)
	r, err = c.Raw(h.ctx, codes.POST, est.Root+"/sen", nil, &p10, csr(ep))
	mustCode(t, r, err, codes.Changed)
	certs, err := est.ParseCertsOnly(r.Body)
	if err != nil || len(certs) != 1 || certs[0].Subject.CommonName != ep {
		t.Fatalf("sen: %v", err)
	}

	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.Changed || res.Err != nil {
		t.Fatalf("finish %s %v", server.CodeString(fin), res.Err)
	}
	if v, _ := c.Get(p("/0/0/2")); v.Int != int64(ModeEST) {
		t.Fatalf("/0/0/2 = %v", v)
	}
	for _, rid := range []string{"/0/0/3", "/0/0/5"} {
		if _, ok := c.Get(p(rid)); ok {
			t.Fatalf("%s written in mode 4", rid)
		}
	}
	spk, _ := c.Get(p("/0/0/4"))
	trust, err := x509.ParseCertificate(spk.Bytes)
	if err != nil || !bytes.Equal(trust.Raw, scert.Raw) {
		t.Fatal("/0/0/4 is not the server certificate")
	}
	_ = c.Close()

	roots := x509.NewCertPool()
	roots.AddCert(trust)
	roots.AddCert(ca.Cert)
	if err := c.DialDTLS(dmAddr.String(), testclient.X509Config(tls.Certificate{Certificate: [][]byte{certs[0].Raw}, PrivateKey: key}, roots, "localhost")); err != nil {
		t.Fatal(err)
	}
	rr, err := c.Register(h.ctx)
	mustCode(t, rr, err, codes.Created)
	reg, ok := dm.Store().ByEndpoint(ep)
	if !ok || reg.Identity.Mode != server.ModeX509 || reg.Identity.CertCN != ep {
		t.Fatalf("registration %+v", reg)
	}
}

// Security Mode resource use (T Tbl 5.2.4-1): for each of the five modes
// the BS writes /0/x/3, /0/x/4 and /0/x/5 exactly when the table has a
// value for them (N/A resources are left out), and a config that sets an
// N/A resource or misses a required one is invalid.
// Proves: SEC-08
func TestSecurityModeResources(t *testing.T) {
	id, spk, sk := Bytes("id"), Bytes("server-key"), Bytes("secret")
	want := map[SecurityMode][3]bool{
		ModePSK: {true, false, true}, ModeRPK: {true, true, true}, ModeX509: {true, true, true},
		ModeNoSec: {false, false, false}, ModeEST: {false, true, false},
	}
	for mode, use := range want {
		sc := SecurityConfig{URI: "coaps://s.example.com", SecurityMode: mode, ServerID: u16(1)}
		vals := [3]*Bytes{&sc.PublicKeyOrID, &sc.ServerPublicKey, &sc.SecretKey}
		for i, v := range [3]Bytes{id, spk, sk} {
			if use[i] {
				*vals[i] = v
			}
		}
		cfg := &BootstrapConfig{Security: map[uint16]SecurityConfig{0: sc}, Servers: map[uint16]ServerConfig{0: {ShortID: 1, Lifetime: 60}}}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("mode %d: %v", mode, err)
		}
		got := map[uint16]bool{}
		for _, n := range sc.nodes(p("/0/0")) {
			got[n.Path.Resource()] = true
		}
		if v := sc.nodes(p("/0/0"))[2].Value; v.Int != int64(mode) {
			t.Fatalf("mode %d: /0/0/2 = %v", mode, v)
		}
		for i := range 3 {
			if got[uint16(3+i)] != use[i] {
				t.Fatalf("mode %d: /0/0/%d written=%v", mode, 3+i, got[uint16(3+i)])
			}
			bad := sc
			bv := [3]*Bytes{&bad.PublicKeyOrID, &bad.ServerPublicKey, &bad.SecretKey}
			if use[i] {
				*bv[i] = nil // required resource missing
			} else {
				*bv[i] = Bytes("x") // N/A resource set
			}
			cfg := &BootstrapConfig{Security: map[uint16]SecurityConfig{0: bad}, Servers: map[uint16]ServerConfig{0: {ShortID: 1, Lifetime: 60}}}
			if err := cfg.Validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("mode %d, resource %d: %v", mode, 3+i, err)
			}
		}
	}
	bad := &BootstrapConfig{Security: map[uint16]SecurityConfig{0: {URI: "coap://s.example.com", SecurityMode: 5, ServerID: u16(1)}}}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("mode 5: %v", err)
	}
}
