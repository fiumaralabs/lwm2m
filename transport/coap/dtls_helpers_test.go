package coap

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"

	piondtls "github.com/fiumaralabs/dtls/v3"
	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
)

// dtlsPKI is a root CA and an intermediate that issues leaf certificates.
type dtlsPKI struct {
	root, inter       *x509.Certificate
	rootKey, interKey *ecdsa.PrivateKey
	pool              *x509.CertPool // the root only
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func spkiOf(t *testing.T, k crypto.Signer) []byte {
	t.Helper()
	b, err := x509.MarshalPKIXPublicKey(k.Public())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var serial int64

func sign(t *testing.T, tmpl, parent *x509.Certificate, pub crypto.PublicKey, key crypto.Signer) *x509.Certificate {
	t.Helper()
	serial++
	tmpl.SerialNumber = big.NewInt(serial)
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	if parent == nil {
		parent = tmpl
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newDTLSPKI(t *testing.T) *dtlsPKI {
	t.Helper()
	p := &dtlsPKI{rootKey: newKey(t), interKey: newKey(t), pool: x509.NewCertPool()}
	ca := func(cn string) *x509.Certificate {
		return &x509.Certificate{Subject: pkix.Name{CommonName: cn}, IsCA: true, BasicConstraintsValid: true,
			KeyUsage: x509.KeyUsageCertSign}
	}
	p.root = sign(t, ca("root"), nil, p.rootKey.Public(), p.rootKey)
	p.inter = sign(t, ca("intermediate"), p.root, p.interKey.Public(), p.rootKey)
	p.pool.AddCert(p.root)
	return p
}

// issue returns a leaf certificate for cn (and DNS names), chain leaf +
// intermediate, usable as client or server certificate.
func (p *dtlsPKI) issue(t *testing.T, cn string, dns ...string) tls.Certificate {
	t.Helper()
	k := newKey(t)
	leaf := sign(t, &x509.Certificate{Subject: pkix.Name{CommonName: cn}, DNSNames: dns,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}, p.inter, k.Public(), p.interKey)
	return tls.Certificate{Certificate: [][]byte{leaf.Raw, p.inter.Raw}, PrivateKey: k, Leaf: leaf}
}

// listenSecure starts a DTLS listener serving PSK, RPK and X.509.
func (h *harness) listenSecure(m CertificateModes, mod ...func(*piondtls.Config)) string {
	h.t.Helper()
	cfg := h.b.DTLSConfig(m)
	for _, f := range mod {
		f(cfg)
	}
	a, err := h.b.ListenDTLS("127.0.0.1:0", DTLSConfig{Config: cfg})
	if err != nil {
		h.t.Fatal(err)
	}
	return a.String()
}

// secureDevice is h.device over a caller-built DTLS configuration.
func (h *harness) secureDevice(tc testclient.Config, addr string, cfg *piondtls.Config) (*testclient.Client, error) {
	h.t.Helper()
	c := testclient.New(tc)
	c.Set(lwm2m.MustParsePath("/1/0/0"), lwm2m.Integer(1))
	c.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(86400))
	c.Set(lwm2m.MustParsePath("/1/0/7"), lwm2m.String("U"))
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	c.Set(lwm2m.MustParsePath("/3/0/1"), lwm2m.String("Lightweight M2M Client"))
	if err := c.DialDTLS(addr, cfg); err != nil {
		return nil, err
	}
	h.t.Cleanup(func() { _ = c.Close() })
	return c, nil
}

// mustRegisterSecure dials and registers, failing the test otherwise.
func (h *harness) mustRegisterSecure(tc testclient.Config, addr string, cfg *piondtls.Config) *testclient.Client {
	h.t.Helper()
	c, err := h.secureDevice(tc, addr, cfg)
	if err != nil {
		h.t.Fatalf("%s: dial: %v", tc.Endpoint, err)
	}
	r, err := c.Register(h.ctx)
	mustCode(h.t, r, err, "2.01")
	return c
}

// handshakeFails dials and registers expecting failure; it returns the
// dial or request error text.
func (h *harness) handshakeFails(tc testclient.Config, addr string, cfg *piondtls.Config) string {
	h.t.Helper()
	cfg.FlightInterval = 50 * time.Millisecond
	c, err := h.secureDevice(tc, addr, cfg)
	if err != nil {
		return err.Error()
	}
	ctx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
	defer cancel()
	r, err := c.Register(ctx)
	if err == nil {
		h.t.Fatalf("%s: registered (%s) with a rejected credential", tc.Endpoint, server.CodeString(r.Code))
	}
	return err.Error()
}

func pskConfig(id string, key []byte, suites ...piondtls.CipherSuiteID) *piondtls.Config {
	if suites == nil {
		suites = []piondtls.CipherSuiteID{piondtls.TLS_PSK_WITH_AES_128_CCM_8}
	}
	return &piondtls.Config{
		PSK:             func([]byte) ([]byte, error) { return key, nil },
		PSKIdentityHint: []byte(id),
		CipherSuites:    suites,
	}
}

// Record content types (RFC 6347 §4.1, RFC 9146 §4).
const (
	recAlert     = 21
	recHandshake = 22
	recAppData   = 23
	recCID       = 25
)

// datagram is one UDP payload seen by the proxy.
type datagram struct {
	up   bool // client -> server
	data []byte
}

// record is one DTLS record header + fragment.
type record struct {
	typ   byte
	epoch uint16
	body  []byte
}

// records splits a datagram into records. Client-to-server CID records
// carry a cidLen-byte CID.
func records(d []byte, cidLen int) []record {
	var out []record
	for len(d) >= 13 {
		h := 13
		if d[0] == recCID {
			h += cidLen
		}
		if len(d) < h {
			break
		}
		n := int(d[h-2])<<8 | int(d[h-1])
		if len(d) < h+n {
			break
		}
		out = append(out, record{typ: d[0], epoch: uint16(d[3])<<8 | uint16(d[4]), body: d[h : h+n]})
		d = d[h+n:]
	}
	return out
}

// udpProxy forwards datagrams between one client and the server. rebind
// swaps the server-facing socket, which looks like a NAT rebinding to the
// server; the old socket is closed, so nothing reaches the client through
// the old mapping.
type udpProxy struct {
	t      *testing.T
	front  *net.UDPConn
	server *net.UDPAddr

	mu     sync.Mutex
	up     *net.UDPConn
	client *net.UDPAddr
	log    []datagram
	tamper func(d datagram) []byte // nil result drops the datagram
}

func newProxy(t *testing.T, server string) *udpProxy {
	t.Helper()
	sa, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		t.Fatal(err)
	}
	front, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	p := &udpProxy{t: t, front: front, server: sa}
	p.rebind()
	go p.fromClient()
	t.Cleanup(func() {
		_ = front.Close()
		p.mu.Lock()
		_ = p.up.Close()
		p.mu.Unlock()
	})
	return p
}

func (p *udpProxy) Addr() string { return p.front.LocalAddr().String() }

// UpstreamAddr is the address the server currently sees for the client.
func (p *udpProxy) UpstreamAddr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.up.LocalAddr().String()
}

func (p *udpProxy) rebind() {
	up, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		p.t.Fatal(err)
	}
	p.mu.Lock()
	old := p.up
	p.up = up
	p.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go p.fromServer(up)
}

func (p *udpProxy) pass(d datagram) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, datagram{up: d.up, data: append([]byte(nil), d.data...)})
	if p.tamper != nil {
		return p.tamper(d)
	}
	return d.data
}

func (p *udpProxy) fromClient() {
	buf := make([]byte, 65536)
	for {
		n, from, err := p.front.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		p.client = from
		up := p.up
		p.mu.Unlock()
		if out := p.pass(datagram{up: true, data: buf[:n]}); out != nil {
			_, _ = up.WriteToUDP(out, p.server)
		}
	}
}

func (p *udpProxy) fromServer(up *net.UDPConn) {
	buf := make([]byte, 65536)
	for {
		n, _, err := up.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p.mu.Lock()
		client := p.client
		p.mu.Unlock()
		if out := p.pass(datagram{data: buf[:n]}); out != nil && client != nil {
			_, _ = p.front.WriteToUDP(out, client)
		}
	}
}

// mark returns the current log position; since returns datagrams after it.
func (p *udpProxy) mark() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.log)
}

func (p *udpProxy) since(i int) []datagram {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]datagram(nil), p.log[i:]...)
}

// handshakeMsgs returns the plaintext (epoch 0) handshake message types
// in datagrams.
func handshakeMsgs(ds []datagram, up bool) []byte {
	var types []byte
	for _, d := range ds {
		if d.up != up {
			continue
		}
		for _, r := range records(d.data, 8) {
			if r.typ == recHandshake && r.epoch == 0 && len(r.body) > 0 {
				types = append(types, r.body[0])
			}
		}
	}
	return types
}
