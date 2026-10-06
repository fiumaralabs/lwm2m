package est_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/est"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v3"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/mux"
	coapnet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	"github.com/plgd-dev/go-coap/v3/options"
	"github.com/plgd-dev/go-coap/v3/udp/client"
)

func key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func csr(t *testing.T, k crypto.Signer, cn string, dns ...string) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}, DNSNames: dns}, k)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func tlsCert(c *x509.Certificate, k crypto.Signer) tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{c.Raw}, PrivateKey: k, Leaf: c}
}

// selfSigned is a manufacturer-installed IDevID (RFC 9148 §3).
func selfSigned(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	k := key(t)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c, k
}

type fixture struct {
	ca     *est.CA
	addr   string
	idevid tls.Certificate
	roots  *x509.CertPool
}

// newFixture runs an EST-coaps server over DTLS with the mandatory suite
// TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 on P-256 (RFC 9148 §3), client
// certificates required, and 64-byte blocks to exercise Block1/Block2
// (RFC 9148 §4.6).
func newFixture(t *testing.T) *fixture {
	t.Helper()
	ca, err := est.NewTestCA("EST CA")
	if err != nil {
		t.Fatal(err)
	}
	sk := key(t)
	sreq, _ := x509.ParseCertificateRequest(csr(t, sk, "est.example", "localhost"))
	scert, err := ca.Issue(sreq)
	if err != nil {
		t.Fatal(err)
	}
	idev, idevKey := selfSigned(t, "urn:dev:est")
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(idev)
	clientCAs.AddCert(ca.Cert)
	cfg := &piondtls.Config{
		Certificates: []tls.Certificate{tlsCert(scert, sk)},
		ClientAuth:   piondtls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		CipherSuites: []piondtls.CipherSuiteID{piondtls.TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8},
	}
	l, err := coapnet.NewDTLSListener("udp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := mux.NewRouter()
	s := &est.Server{CA: ca, Authorize: func(id est.Identity, c *x509.CertificateRequest) error {
		if id.Cert == nil || id.Cert.Subject.CommonName != c.Subject.CommonName {
			return est.ErrForbidden // enroll only the identity the IDevID names
		}
		return nil
	}}
	if err := s.Mount(r); err != nil {
		t.Fatal(err)
	}
	srv := coapdtls.NewServer(options.WithMux(r), options.WithBlockwise(true, blockwise.SZX64, 10*time.Second))
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { srv.Stop(); _ = l.Close() })
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	return &fixture{ca: ca, addr: l.Addr().String(), idevid: tlsCert(idev, idevKey), roots: roots}
}

func (f *fixture) dial(t *testing.T, cert tls.Certificate) *client.Conn {
	t.Helper()
	cc, err := coapdtls.Dial(f.addr, &piondtls.Config{
		Certificates: []tls.Certificate{cert}, RootCAs: f.roots, ServerName: "localhost",
		CipherSuites: []piondtls.CipherSuiteID{piondtls.TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8},
	}, options.WithBlockwise(true, blockwise.SZX64, 10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

type reply struct {
	code codes.Code
	cf   int
	body []byte
}

func do(t *testing.T, cc *client.Conn, code codes.Code, path string, cf, accept int, body []byte) reply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := cc.AcquireMessage(ctx)
	defer cc.ReleaseMessage(m)
	m.SetCode(code)
	m.SetType(message.Confirmable)
	if err := m.SetPath(path); err != nil {
		t.Fatal(err)
	}
	tok, _ := message.GetToken()
	m.SetToken(tok)
	if cf >= 0 {
		m.SetContentFormat(message.MediaType(cf))
	}
	if accept >= 0 {
		m.SetAccept(message.MediaType(accept))
	}
	if body != nil {
		m.SetBody(bytes.NewReader(body))
	}
	res, err := cc.Do(m)
	if err != nil {
		t.Fatal(err)
	}
	defer cc.ReleaseMessage(res)
	out := reply{code: res.Code(), cf: -1}
	if f, err := res.ContentFormat(); err == nil {
		out.cf = int(f)
	}
	if res.Body() != nil {
		out.body, _ = io.ReadAll(res.Body())
	}
	return out
}

func want(t *testing.T, r reply, code codes.Code, cf int) {
	t.Helper()
	if r.code != code || r.cf != cf {
		t.Fatalf("got %v cf %d, want %v cf %d (%q)", r.code, r.cf, code, cf, r.body)
	}
}

// Proves: EST-02
// EST-coaps over DTLS under /.well-known/est: /crts returns the CA in a
// PKCS #7 certs-only container (281, the default) or as one certificate
// (287); /sen enrolls a locally generated key from a PKCS #10 CSR (286) and
// answers 2.04; /sren re-enrolls with the issued certificate when Subject
// and SANs are unchanged. Responses and CSRs exceed one 64-byte block, so
// Block1 and Block2 are used.
func TestEnrollFlow(t *testing.T) {
	f := newFixture(t)
	cc := f.dial(t, f.idevid)

	r := do(t, cc, codes.GET, "/.well-known/est/crts", -1, -1, nil)
	want(t, r, codes.Content, 281)
	cas, err := est.ParseCertsOnly(r.body)
	if err != nil || len(cas) != 1 || !cas[0].Equal(f.ca.Cert) {
		t.Fatalf("crts %v %v", cas, err)
	}
	r = do(t, cc, codes.GET, "/.well-known/est/crts", -1, 287, nil)
	want(t, r, codes.Content, 287)
	if !bytes.Equal(r.body, f.ca.Cert.Raw) {
		t.Fatal("287 is not the CA certificate")
	}

	k := key(t) // the key pair is generated on the device (EST-01)
	r = do(t, cc, codes.POST, "/.well-known/est/sen", 286, -1, csr(t, k, "urn:dev:est"))
	want(t, r, codes.Changed, 281)
	certs, err := est.ParseCertsOnly(r.body)
	if err != nil || len(certs) != 1 {
		t.Fatal(err)
	}
	cert := certs[0]
	if _, err := cert.Verify(x509.VerifyOptions{Roots: f.roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "urn:dev:est" || !cert.PublicKey.(*ecdsa.PublicKey).Equal(&k.PublicKey) {
		t.Fatalf("issued %v", cert.Subject)
	}
	// An ArbitraryLabel segment selects a profile (RFC 9148 §4.1); Handle
	// accepts it (Mount routes only the default paths).
	s := est.Server{CA: f.ca}
	if r := s.Handle(est.Identity{PSKIdentity: "bs"}, est.Request{Code: codes.GET, Path: "/.well-known/est/lbl/crts"}); r.Code != codes.Content {
		t.Fatalf("label: %v", r.Code)
	}

	// Re-enroll on a session authenticated with the issued certificate.
	cc2 := f.dial(t, tlsCert(cert, k))
	k2 := key(t)
	r = do(t, cc2, codes.POST, "/.well-known/est/sren", 286, 287, csr(t, k2, "urn:dev:est"))
	want(t, r, codes.Changed, 287)
	renewed, err := x509.ParseCertificate(r.body)
	if err != nil || !renewed.PublicKey.(*ecdsa.PublicKey).Equal(&k2.PublicKey) || renewed.SerialNumber.Cmp(cert.SerialNumber) == 0 {
		t.Fatalf("renewed %v", err)
	}
	r = do(t, cc2, codes.POST, "/.well-known/est/sren", 286, -1, csr(t, k2, "urn:dev:other"))
	want(t, r, codes.Forbidden, -1)
	// The IDevID was not issued by this CA: no re-enrollment with it.
	r = do(t, cc, codes.POST, "/.well-known/est/sren", 286, -1, csr(t, k2, "urn:dev:est"))
	want(t, r, codes.Forbidden, -1)
}

// Proves: EST-02, EST-03
// Error mapping (RFC 9148 §4.5): wrong method 4.05, wrong Content-Format
// 4.15, unsupported Accept 4.06, malformed or unsigned CSR 4.00, a CSR for
// a name the client may not enroll 4.03. Server-side key generation and
// CSR attributes are not offered (4.04): the private key never leaves the
// device.
func TestEnrollErrors(t *testing.T) {
	f := newFixture(t)
	cc := f.dial(t, f.idevid)
	k := key(t)
	good := csr(t, k, "urn:dev:est")
	want(t, do(t, cc, codes.GET, "/.well-known/est/sen", -1, -1, nil), codes.MethodNotAllowed, -1)
	want(t, do(t, cc, codes.POST, "/.well-known/est/crts", 286, -1, good), codes.MethodNotAllowed, -1)
	want(t, do(t, cc, codes.POST, "/.well-known/est/sen", 0, -1, good), codes.UnsupportedMediaType, -1)
	want(t, do(t, cc, codes.POST, "/.well-known/est/sen", 286, 50, good), codes.NotAcceptable, -1)
	want(t, do(t, cc, codes.GET, "/.well-known/est/crts", -1, 50, nil), codes.NotAcceptable, -1)
	want(t, do(t, cc, codes.POST, "/.well-known/est/sen", 286, -1, []byte{0x30, 0x03, 1, 2, 3}), codes.BadRequest, -1)
	tampered := append([]byte(nil), good...)
	tampered[len(tampered)-5] ^= 0xff // break the self-signature (proof of possession)
	want(t, do(t, cc, codes.POST, "/.well-known/est/sen", 286, -1, tampered), codes.BadRequest, -1)
	want(t, do(t, cc, codes.POST, "/.well-known/est/sen", 286, -1, csr(t, k, "urn:dev:someone-else")), codes.Forbidden, -1)
	for _, p := range []string{"skg", "skc", "att"} {
		want(t, do(t, cc, codes.POST, "/.well-known/est/"+p, 286, -1, good), codes.NotFound, -1)
	}
	// Unauthenticated callers are refused (RFC 9148 §4).
	s := est.Server{CA: f.ca}
	if r := s.Handle(est.Identity{}, est.Request{Code: codes.GET, Path: "/.well-known/est/crts"}); r.Code != codes.Unauthorized {
		t.Fatalf("anonymous: %v", r.Code)
	}
	// The Bootstrap-Server's /0 instance for mode 4 omits /0/x/3 and /0/x/5.
	for _, n := range est.SecurityInstance(0, "coaps://localhost", false, f.ca.Cert) {
		if r := n.Path.Resource(); r == 3 || r == 5 {
			t.Fatalf("/0/0/%d written", r)
		}
	}
}

// Proves: EST-01
// Certificate mode with EST end to end: the Bootstrap-Server's /0 instance
// has Security Mode 4 and the server certificate in /0/x/4; the client
// enrolls a locally generated key over EST-coaps and then registers with
// the LwM2M Server in certificate mode, trusting /0/x/4 and authenticated
// by the EST-issued certificate whose CN is the endpoint.
func TestCertificateModeWithEST(t *testing.T) {
	f := newFixture(t)
	// LwM2M server credential, issued by the same CA.
	sk := key(t)
	sreq, _ := x509.ParseCertificateRequest(csr(t, sk, "lwm2m.example", "localhost"))
	scert, _ := f.ca.Issue(sreq)
	sec := est.SecurityInstance(1, "coaps://localhost", false, scert)
	var mode, pub lwm2m.Value
	for _, n := range sec {
		switch n.Path.Resource() {
		case 2:
			mode = n.Value
		case 4:
			pub = n.Value
		}
	}
	if !mode.Equal(lwm2m.Integer(4)) || !bytes.Equal(pub.Bytes, scert.Raw) {
		t.Fatalf("/0/1: %v", sec)
	}

	cc := f.dial(t, f.idevid)
	k := key(t)
	r := do(t, cc, codes.POST, "/.well-known/est/sen", 286, 287, csr(t, k, "urn:dev:est"))
	want(t, r, codes.Changed, 287)
	cert, _ := x509.ParseCertificate(r.body)

	srv := server.New(server.Config{RequestTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = srv.Close() })
	pool := x509.NewCertPool()
	pool.AddCert(f.ca.Cert)
	addr, err := srv.ListenDTLS("127.0.0.1:0", server.DTLSConfig{Config: srv.DTLSConfig(server.CertificateModes{
		Certificates: []tls.Certificate{tlsCert(scert, sk)}, ClientCAs: pool})})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Security().Put(server.SecurityInfo{Endpoint: "urn:dev:est", X509: true}); err != nil {
		t.Fatal(err)
	}
	trust := x509.NewCertPool()
	srvCert, _ := x509.ParseCertificate(pub.Bytes) // trust anchor from /0/x/4
	trust.AddCert(srvCert)
	c := testclient.New(testclient.Config{Endpoint: "urn:dev:est"})
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("x"))
	if err := c.DialDTLS(addr.String(), testclient.X509Config(tlsCert(cert, k), trust, "localhost")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.Register(ctx)
	if err != nil || res.Code != codes.Created {
		t.Fatalf("register: %v %v", res, err)
	}
	reg, ok := srv.Store().ByEndpoint("urn:dev:est")
	if !ok || reg.Identity.Mode != server.ModeX509 || reg.Identity.CertCN != "urn:dev:est" {
		t.Fatalf("registration %+v", reg)
	}
}
