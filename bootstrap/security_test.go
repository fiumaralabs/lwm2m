package bootstrap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	piondtls "github.com/fiumaralabs/dtls/v3"
	"github.com/fiumaralabs/lwm2m/security/dtls"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

const bsKey = "bs-key-0123456789abcdef" // high-entropy per-device key (BS-10)

func pskConfig(identity, key string) *piondtls.Config {
	return &piondtls.Config{
		PSK:             func([]byte) ([]byte, error) { return []byte(key), nil },
		PSKIdentityHint: []byte(identity),
		CipherSuites:    []piondtls.CipherSuiteID{piondtls.TLS_PSK_WITH_AES_128_CCM_8},
	}
}

// ETS 1.1-int-1 Client Initiated Bootstrap Full (PSK): bootstrap over
// DTLS-PSK with the BS account's credentials, then Register with the
// provisioned PSK at the LwM2M Server (int-401). A PSK identity bound to
// another endpoint is refused with 4.00; without ep, the endpoint is the
// one the PSK identity belongs to.
// Proves: BS-01, BS-20, BS-10
func TestInt1BootstrapPSK(t *testing.T) {
	h := newHarness(t)
	dm := server.New(server.Config{RequestTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = dm.Close() })
	dmAddr, err := dm.ListenDTLS("127.0.0.1:0", server.DTLSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	const dmKey = "dm-key-0123456789abcdef"
	if err := dm.Security().Put(server.SecurityInfo{Endpoint: "ep1", PSKIdentity: "ep1", PSKKey: []byte(dmKey)}); err != nil {
		t.Fatal(err)
	}
	for _, si := range []server.SecurityInfo{
		{Endpoint: "ep1", PSKIdentity: "ep1-bs", PSKKey: []byte(bsKey)},
		{Endpoint: "ep2", PSKIdentity: "ep2-bs", PSKKey: []byte(bsKey)},
	} {
		if err := h.sec.Put(si); err != nil {
			t.Fatal(err)
		}
	}
	h.put("ep1", c1("coaps://"+dmAddr.String(), "ep1", dmKey))
	h.put("ep2", c1("coaps://"+dmAddr.String(), "ep2", dmKey))

	c := h.client("ep1", testclient.Config{PSKIdentity: "ep1-bs", PSKKey: []byte(bsKey)})
	fin, res := h.bootstrap(c, c.BootstrapQuery(nil))
	if fin != codes.Changed || res.Err != nil || res.Identity.PSKIdentity != "ep1-bs" {
		t.Fatalf("finish %s err %v identity %+v", server.CodeString(fin), res.Err, res.Identity)
	}
	// ep2's credentials claiming ep1: 4.00 (SEC-06, C1).
	other := h.client("ep1", testclient.Config{PSKIdentity: "ep2-bs", PSKKey: []byte(bsKey)})
	r, err := other.BootstrapRequest(h.ctx, other.BootstrapQuery(nil))
	mustCode(t, r, err, codes.BadRequest)
	// No ep: derived from the PSK identity (BS-20).
	r, err = other.BootstrapRequest(h.ctx, nil)
	mustCode(t, r, err, codes.Changed)
	if res := h.result(); res.Endpoint != "ep2" {
		t.Fatalf("endpoint %q", res.Endpoint)
	}

	// Step 5: Register with the provisioned account.
	uri, id, key, ok := c.ServerAccount()
	if !ok || uri != "coaps://"+dmAddr.String() {
		t.Fatalf("account %q", uri)
	}
	_ = c.Close()
	if err := c.DialDTLS(dmAddr.String(), pskConfig(string(id), string(key))); err != nil {
		t.Fatal(err)
	}
	rr, err := c.Register(h.ctx)
	mustCode(t, rr, err, codes.Created)
}

type pki struct {
	caKey *ecdsa.PrivateKey
	ca    *x509.Certificate
	pool  *x509.CertPool
}

func newPKI(t *testing.T) *pki {
	k := &pki{}
	k.caKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.caKey.PublicKey, k.caKey)
	if err != nil {
		t.Fatal(err)
	}
	k.ca, _ = x509.ParseCertificate(der)
	k.pool = x509.NewCertPool()
	k.pool.AddCert(k.ca)
	return k
}

func (k *pki) leaf(t *testing.T, cn string) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, k.ca, &key.PublicKey, k.caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// certListener opens a BS DTLS listener for certificate modes, with the
// credential callbacks of the LwM2M Server's DTLS config over the BS
// security store: the plug-in point for RPK and X.509 credentials.
func (h *harness) certListener(t *testing.T, m server.CertificateModes) string {
	helper := server.New(server.Config{Security: h.sec})
	t.Cleanup(func() { _ = helper.Close() })
	a, err := h.bs.ListenDTLS("127.0.0.1:0", DTLSConfig{Config: helper.DTLSConfig(m)})
	if err != nil {
		t.Fatal(err)
	}
	return a.String()
}

// ETS 1.1-int-2 Client Initiated Bootstrap Full (Cert), plus RPK: the BS
// bootstraps certificate and raw-public-key clients; the endpoint must be
// the certificate CN (4.00 otherwise, BS-01) and is derived from it when
// ep is omitted (BS-20); an RPK client is bound to its stored key.
// Proves: BS-01, BS-20, BS-10
func TestInt2BootstrapCertificateAndRPK(t *testing.T) {
	h := newHarness(t)
	k := newPKI(t)
	addr := h.certListener(t, server.CertificateModes{Certificates: []tls.Certificate{k.leaf(t, "bs")}, ClientCAs: k.pool})
	for _, ep := range []string{"cert-ep", "other"} {
		if err := h.sec.Put(server.SecurityInfo{Endpoint: ep, X509: true}); err != nil {
			t.Fatal(err)
		}
		h.put(ep, c1("coaps://s.example.com", ep, "k"))
	}
	c := testclient.NewBootstrap(testclient.Config{Endpoint: "other"})
	c13(c, ModeX509, "", "")
	if err := c.DialDTLS(addr, testclient.X509Config(k.leaf(t, "cert-ep"), k.pool, "")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	r, err := c.BootstrapRequest(h.ctx, c.BootstrapQuery(nil)) // ep=other, CN=cert-ep
	mustCode(t, r, err, codes.BadRequest)
	fin, res := h.bootstrap(c, nil) // no ep: CN
	if fin != codes.Changed || res.Err != nil || res.Endpoint != "cert-ep" || res.Identity.Mode != server.ModeX509 {
		t.Fatalf("finish %s err %v ep %q", server.CodeString(fin), res.Err, res.Endpoint)
	}

	// RPK (security mode 1).
	srvKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw, err := dtls.RawKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	rpkAddr := h.certListener(t, server.CertificateModes{Certificates: []tls.Certificate{raw}})
	cliKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	cliSPKI, _ := x509.MarshalPKIXPublicKey(cliKey.Public())
	if err := h.sec.Put(server.SecurityInfo{Endpoint: "rpk-ep", PublicKey: cliSPKI}); err != nil {
		t.Fatal(err)
	}
	h.put("rpk-ep", c1("coaps://s.example.com", "rpk-ep", "k"))
	srvSPKI, _ := x509.MarshalPKIXPublicKey(srvKey.Public())
	cfg, err := testclient.RPKConfig(cliKey, srvSPKI)
	if err != nil {
		t.Fatal(err)
	}
	rc := testclient.NewBootstrap(testclient.Config{Endpoint: "rpk-ep"})
	c13(rc, ModeRPK, "", "")
	if err := rc.DialDTLS(rpkAddr, cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	r, err = rc.BootstrapRequest(h.ctx, []string{"ep=cert-ep"}) // key bound to rpk-ep
	mustCode(t, r, err, codes.BadRequest)
	r, err = rc.BootstrapRequest(h.ctx, nil) // an RPK yields no endpoint name
	mustCode(t, r, err, codes.BadRequest)
	fin, res = h.bootstrap(rc, rc.BootstrapQuery(nil))
	if fin != codes.Changed || res.Err != nil || res.Identity.Mode != server.ModeRPK {
		t.Fatalf("rpk: finish %s err %v", server.CodeString(fin), res.Err)
	}
}

// Credentials may change at any time (T §5.2.4): once the BS credentials
// of an endpoint are replaced, a Bootstrap-Request on a DTLS session
// established with the old ones is refused.
// Proves: BS-29
func TestCredentialChangeMidSession(t *testing.T) {
	h := newHarness(t)
	if err := h.sec.Put(server.SecurityInfo{Endpoint: "ep", PSKIdentity: "old", PSKKey: []byte(bsKey)}); err != nil {
		t.Fatal(err)
	}
	h.put("ep", c1("coap://s.example.com", "id", "key"))
	c := h.client("ep", testclient.Config{PSKIdentity: "old", PSKKey: []byte(bsKey)})
	if fin, res := h.bootstrap(c, c.BootstrapQuery(nil)); fin != codes.Changed || res.Err != nil {
		t.Fatalf("finish %s err %v", server.CodeString(fin), res.Err)
	}
	if err := h.sec.Put(server.SecurityInfo{Endpoint: "ep", PSKIdentity: "new", PSKKey: []byte(bsKey)}); err != nil {
		t.Fatal(err)
	}
	r, err := c.BootstrapRequest(h.ctx, c.BootstrapQuery(nil))
	mustCode(t, r, err, codes.BadRequest)
}
