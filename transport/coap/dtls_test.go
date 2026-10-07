package coap

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/security/dtls"
	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/crypto/ciphersuite"
	pionelliptic "github.com/pion/dtls/v4/pkg/crypto/elliptic"
	"github.com/pion/dtls/v4/pkg/protocol"
	"github.com/pion/dtls/v4/pkg/protocol/extension"
	"github.com/pion/dtls/v4/pkg/protocol/handshake"
)

// secureSetup is a harness with a listener serving PSK and X.509.
type secureSetup struct {
	*harness
	pki        *dtlsPKI
	serverCert tls.Certificate
	addr       string
}

func newSecure(t *testing.T, extra ...piondtls.ServerOption) *secureSetup {
	t.Helper()
	h := newHarness(t)
	p := newDTLSPKI(t)
	sc := p.issue(t, "lwm2m.test", "lwm2m.test")
	s := &secureSetup{harness: h, pki: p, serverCert: sc}
	s.addr = h.listenSecure(CertificateModes{Certificates: []tls.Certificate{sc}, ClientCAs: p.pool}, extra...)
	return s
}

func (s *secureSetup) x509Client(ep string) tls.Certificate {
	s.t.Helper()
	if err := s.srv.Security().Put(server.SecurityInfo{Endpoint: ep, X509: true}); err != nil {
		s.t.Fatal(err)
	}
	return s.pki.issue(s.t, ep)
}

// dtls13 forces DTLS 1.3 on a client.
var dtls13 = []piondtls.ClientOption{piondtls.WithMinVersion(protocol.Version1_3), piondtls.WithMaxVersion(protocol.Version1_3)}

// Proves: SEC-04, SEC-17, TLS13-02
// One listener serves PSK and X.509 clients (security modes 0 and 2; RPK,
// mode 1, waits for RFC 7250 in pion/dtls, so SEC-03 is pending). Every
// suite the server would let the Bootstrap-Server provision in /0/x/16
// completes a handshake when a client offers it alone: the mandatory PSK
// CCM_8 and CBC_SHA256, ECDHE_ECDSA CCM_8 and CBC_SHA256 (the RFC 7925
// profile), the additional AEAD suites (SEC-17 MAY) and the DTLS 1.3
// TLS_AES_128_GCM_SHA256.
func TestSecurityModesOnePort(t *testing.T) {
	s := newSecure(t)
	if err := s.srv.Security().Put(server.SecurityInfo{Endpoint: "psk", PSKIdentity: "psk-id", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	xc := s.x509Client("x509")

	values := dtls.Resource16(DTLSCipherSuites())
	for _, want := range []uint32{0xC0A8, 0x00AE, 0xC0AE, 0xC023, 0x1301} {
		if !containsU32(values, want) {
			t.Fatalf("/0/x/16 values %x lack suite %#x", values, want)
		}
	}
	modes := map[server.SecurityMode]bool{}
	for _, v := range values {
		id := ciphersuite.ID(v)
		t.Run(id.String(), func(t *testing.T) {
			switch {
			case strings.Contains(id.String(), "PSK"):
				s.mustRegisterSecure(testclient.Config{Endpoint: "psk"}, s.addr, pskConfig("psk-id", []byte("0123456789abcdef"), id))
				modes[server.ModePSK] = true
			case v>>8 == 0x13: // DTLS 1.3 suites authenticate with certificates here
				s.mustRegisterSecure(testclient.Config{Endpoint: "x509"}, s.addr, with(testclient.X509Config(xc, s.pki.pool, "lwm2m.test", id), dtls13...))
			default:
				s.mustRegisterSecure(testclient.Config{Endpoint: "x509"}, s.addr, testclient.X509Config(xc, s.pki.pool, "lwm2m.test", id))
				modes[server.ModeX509] = true
			}
		})
	}
	for ep, mode := range map[string]server.SecurityMode{"psk": server.ModePSK, "x509": server.ModeX509} {
		reg, ok := s.srv.Store().ByEndpoint(ep)
		if !ok || reg.Identity.Mode != mode || !modes[mode] {
			t.Fatalf("%s: registration %v, identity %v", ep, ok, reg)
		}
	}
}

func containsU32(l []uint32, v uint32) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// RPK (security mode 1) is refused with a clear error until pion/dtls
// supports RFC 7250: a raw-key server credential and a stored client key
// are not accepted, and the listener offers no certificate types.
func TestRPKRefused(t *testing.T) {
	h := newHarness(t)
	k := newKey(t)
	if _, err := h.b.DTLSConfig(CertificateModes{Certificates: []tls.Certificate{{Certificate: [][]byte{spkiOf(t, k)}, PrivateKey: k}}}); !errors.Is(err, server.ErrRPKUnsupported) {
		t.Fatalf("raw-key credential: %v", err)
	}
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "rpk", PublicKey: spkiOf(t, k)}); !errors.Is(err, server.ErrRPKUnsupported) {
		t.Fatalf("RPK client key: %v", err)
	}
	if !strings.Contains(server.ErrRPKUnsupported.Error(), "RFC 7250") {
		t.Fatal(server.ErrRPKUnsupported)
	}
}

// Proves: SEC-10, SEC-06
// X.509: the client chain must verify against the server trust store and
// the certificate CN must be the endpoint, with both mandatory suites.
func TestX509Mode(t *testing.T) {
	s := newSecure(t)
	xc := s.x509Client("urn:dev:x509")
	s.x509Client("urn:dev:other")
	for _, suite := range []ciphersuite.ID{ciphersuite.TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8, dtls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256} {
		s.mustRegisterSecure(testclient.Config{Endpoint: "urn:dev:x509"}, s.addr, testclient.X509Config(xc, s.pki.pool, "lwm2m.test", suite))
	}
	reg, _ := s.srv.Store().ByEndpoint("urn:dev:x509")
	if reg.Identity.Mode != server.ModeX509 || reg.Identity.CertCN != "urn:dev:x509" {
		t.Fatalf("identity %+v", reg.Identity)
	}
	// ep different from the certificate CN: 4.00.
	c, err := s.secureDevice(testclient.Config{Endpoint: "urn:dev:other"}, s.addr, testclient.X509Config(xc, s.pki.pool, "lwm2m.test"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Register(s.ctx)
	mustCode(t, r, err, "4.00")
	// Without ep the CN names the client (REG-02).
	c, err = s.secureDevice(testclient.Config{}, s.addr, testclient.X509Config(xc, s.pki.pool, "lwm2m.test"))
	if err != nil {
		t.Fatal(err)
	}
	r, err = c.Register(s.ctx)
	mustCode(t, r, err, "2.01")
	// A certificate from another CA, and a trusted one whose CN has no
	// credentials, fail the handshake.
	foreign := newDTLSPKI(t).issue(t, "urn:dev:x509")
	s.handshakeFails(testclient.Config{Endpoint: "urn:dev:x509"}, s.addr, testclient.X509Config(foreign, s.pki.pool, "lwm2m.test"))
	s.handshakeFails(testclient.Config{Endpoint: "urn:dev:nobody"}, s.addr, testclient.X509Config(s.pki.issue(t, "urn:dev:nobody"), s.pki.pool, "lwm2m.test"))
}

// Proves: SEC-14, SEC-18
// Without DNS the client dials an IP literal and sends /0/x/14 as SNI; the
// server picks the certificate for that name, so the client's RFC 6125
// check of the provisioned FQDN passes. An unknown name gets the default
// certificate, which the client rejects. /0/x/23 advertises bit 0 (SNI).
func TestSNICertificateSelection(t *testing.T) {
	h := newHarness(t)
	p := newDTLSPKI(t)
	a := p.issue(t, "a.lwm2m.test", "a.lwm2m.test")
	b := p.issue(t, "b.lwm2m.test", "b.lwm2m.test")
	addr := h.listenSecure(CertificateModes{Certificates: []tls.Certificate{a, b}, ClientCAs: p.pool})
	if DTLSExtensions&(1<<0) == 0 {
		t.Fatal("/0/x/23 lacks SNI")
	}
	xc := p.issue(t, "sni-dev")
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "sni-dev", X509: true}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.lwm2m.test", "b.lwm2m.test"} {
		var got string
		cfg := with(testclient.X509Config(xc, p.pool, name), piondtls.WithVerifyPeerCertificate(func(raw [][]byte, _ [][]*x509.Certificate) error {
			c, _ := x509.ParseCertificate(raw[0])
			got = c.Subject.CommonName
			return nil
		}))
		h.mustRegisterSecure(testclient.Config{Endpoint: "sni-dev"}, addr, cfg)
		if got != name {
			t.Fatalf("SNI %s: server presented %s", name, got)
		}
	}
	h.handshakeFails(testclient.Config{Endpoint: "sni-dev"}, addr, testclient.X509Config(xc, p.pool, "c.lwm2m.test"))
}

// Proves: SEC-19
// The server's chain (leaf + intermediate) satisfies every /0/x/15
// certificate usage with the matching /0/x/13 types the Bootstrap-Server
// may provision, checked the way a client does (security/dtls); a /0/x/4 that
// does not match the server fails the handshake.
func TestCertificateUsage(t *testing.T) {
	s := newSecure(t)
	xc := s.x509Client("cu-dev")
	leaf, inter := s.serverCert.Certificate[0], s.serverCert.Certificate[1]
	sha256 := func(b []byte) []byte { return hash(dtls.MatchSHA256, b) }
	cases := []struct {
		usage dtls.CertificateUsage
		match dtls.MatchingType
		assoc []byte
		ok    bool
	}{
		{dtls.UsageCAConstraint, dtls.MatchExact, s.pki.root.Raw, true},
		{dtls.UsageCAConstraint, dtls.MatchSHA256, sha256(inter), true},
		{dtls.UsageServiceCertConstraint, dtls.MatchExact, leaf, true},
		{dtls.UsageTrustAnchorAssertion, dtls.MatchSHA256, sha256(inter), true},
		{dtls.UsageTrustAnchorAssertion, dtls.MatchExact, s.pki.root.Raw, true},
		{dtls.UsageDomainIssued, dtls.MatchSHA512, hash(dtls.MatchSHA512, leaf), true},
		{dtls.UsageDomainIssued, dtls.MatchSHA384, hash(dtls.MatchSHA384, leaf), true},
		{dtls.UsageDomainIssued, dtls.MatchExact, inter, false},
		{dtls.UsageServiceCertConstraint, dtls.MatchExact, inter, false},
	}
	for _, c := range cases {
		cfg := with(testclient.X509Config(xc, nil, "lwm2m.test"),
			piondtls.WithInsecureSkipVerify(true), // the usage check below is the client's whole server validation
			piondtls.WithVerifyPeerCertificate(func(raw [][]byte, _ [][]*x509.Certificate) error {
				return dtls.VerifyServerCertificate(c.usage, c.match, c.assoc, raw, s.pki.pool, "lwm2m.test", time.Now())
			}))
		if c.ok {
			s.mustRegisterSecure(testclient.Config{Endpoint: "cu-dev"}, s.addr, cfg)
		} else {
			s.handshakeFails(testclient.Config{Endpoint: "cu-dev"}, s.addr, cfg)
		}
	}
}

func hash(m dtls.MatchingType, b []byte) []byte { return dtls.Digest(m, b) }

// Proves: CID-01, CID-02, CID-03, QM-05, TLS13-06
// The server assigns an 8-byte Connection ID (RFC 9146) and advertises it
// in /0/x/23 bit 12. When the client's NAT rebinds mid-session, its next
// record (an Update) arrives from a new address: the server keeps the
// registration, accepts the Update without any handshake, and sends the
// next downlink to the new address. /0/x/23 advertises only SNI and CID.
func TestCIDRebinding(t *testing.T) {
	h := newHarness(t)
	if DTLSExtensions != 1<<0|1<<12 {
		t.Fatalf("/0/x/23 = %#x, want SNI and CID only", DTLSExtensions)
	}
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "cid", PSKIdentity: "cid", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	addr := h.listenSecure(CertificateModes{})
	px := newProxy(t, addr)
	c := h.mustRegisterSecure(testclient.Config{Endpoint: "cid", CID: true}, px.Addr(), pskConfig("cid", []byte("0123456789abcdef")))
	reg, _ := h.srv.Store().ByEndpoint("cid")
	before := reg.Addr.String()
	h.ev.wait(t, func(e server.Event) bool { _, ok := e.(server.Registered); return ok })

	// After the handshake the client sends CID records with the server's 8-byte CID.
	sawCID := false
	for _, d := range px.since(0) {
		if d.up && d.data[0] == recCID {
			sawCID = len(records(d.data, 8)) > 0 && records(d.data, 8)[0].epoch == 1
		}
	}
	if !sawCID {
		t.Fatal("client records carry no server-assigned CID")
	}

	px.rebind()
	m := px.mark()
	u, err := c.Update(h.ctx, nil, nil)
	mustCode(t, u, err, "2.04")
	reg, _ = h.srv.Store().ByEndpoint("cid")
	if reg.Addr.String() != px.UpstreamAddr() || reg.Addr.String() == before {
		t.Fatalf("registration address %s, want the rebound %s", reg.Addr, px.UpstreamAddr())
	}
	resp, err := h.srv.Read(h.ctx, "cid", p("/3/0/0"), server.ReadOptions{})
	mustResp(t, resp, err, "2.05")
	after := px.since(m)
	if hs := append(handshakeMsgs(after, true), handshakeMsgs(after, false)...); len(hs) > 0 {
		t.Fatalf("handshake after rebinding: %v", hs)
	}
	h.ev.mu.Lock()
	defer h.ev.mu.Unlock()
	for _, e := range h.ev.l {
		if r, ok := e.(server.Registered); ok && r.Registration.ID != reg.ID {
			t.Fatal("rebinding created a new registration")
		}
	}
}

// clientSessions is a client-side session store.
type clientSessions struct{ m map[string]piondtls.Session }

func (c *clientSessions) Set(k []byte, s piondtls.Session) error { c.m[string(k)] = s; return nil }
func (c *clientSessions) Get(k []byte) (piondtls.Session, error) { return c.m[string(k)], nil }
func (c *clientSessions) Del(k []byte) error                     { delete(c.m, string(k)); return nil }

// Proves: SEC-11, QM-05
// The server keeps DTLS state: a client waking up on a new socket resumes
// its session (abbreviated handshake, no ClientKeyExchange) and the
// resumed session keeps the PSK identity, so its Update is accepted
// (pion does not restore the identity; internal/dtlscoap does). Server
// and client key pairs differ: a client presenting the server's own
// certificate (a trusted chain whose CN has credentials) is refused.
func TestSessionResumptionAndKeyUniqueness(t *testing.T) {
	h := newHarness(t)
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "sleepy", PSKIdentity: "sleepy", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	p := newDTLSPKI(t)
	sc := p.issue(t, "lwm2m.test", "lwm2m.test")
	addr := h.listenSecure(CertificateModes{Certificates: []tls.Certificate{sc}, ClientCAs: p.pool})
	px := newProxy(t, addr)
	sessions := &clientSessions{m: map[string]piondtls.Session{}}
	cfg := with(pskConfig("sleepy", []byte("0123456789abcdef")), piondtls.WithSessionStore(sessions))
	c := h.mustRegisterSecure(testclient.Config{Endpoint: "sleepy"}, px.Addr(), cfg)
	loc := c.Location()
	_ = c.Close()

	m := px.mark()
	c2, err := h.secureDevice(testclient.Config{Endpoint: "sleepy"}, px.Addr(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c2.Raw(h.ctx, 2, "/rd/"+loc, nil, nil, nil)
	mustCode(t, r, err, "2.04")
	up := handshakeMsgs(px.since(m), true)
	if len(up) == 0 || up[0] != byte(handshake.TypeClientHello) || bytes.IndexByte(up, byte(handshake.TypeClientKeyExchange)) >= 0 {
		t.Fatalf("handshake messages %v: want a resumption (ClientHello, no ClientKeyExchange)", up)
	}
	reg, _ := h.srv.Store().ByEndpoint("sleepy")
	if reg.Identity.Mode != server.ModePSK || reg.Identity.PSKIdentity != "sleepy" {
		t.Fatalf("identity after resumption %+v", reg.Identity)
	}

	// Key uniqueness: the server's own certificate is not a client key pair.
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "lwm2m.test", X509: true}); err != nil {
		t.Fatal(err)
	}
	h.handshakeFails(testclient.Config{Endpoint: "lwm2m.test"}, addr, testclient.X509Config(sc, p.pool, "lwm2m.test"))
}

// Alerts the server sends: an untrusted certificate is bad_certificate
// (42), a "Fail" alert for the client (T Tbl 5.2.10-1). An unknown PSK
// identity is internal_error (80), which pion/dtls sends and offers no
// way to change; the table wants unknown_psk_identity (115), so SEC-12 is
// pending (upstream branch unknown-psk-identity-alert). A record with a
// bad MAC is discarded silently: no alert, the session survives and the
// CoAP retransmission succeeds.
func TestDTLSAlerts(t *testing.T) {
	s := newSecure(t)
	if err := s.srv.Security().Put(server.SecurityInfo{Endpoint: "mac", PSKIdentity: "mac", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	foreign := newDTLSPKI(t).issue(t, "x")
	for _, c := range []struct {
		name string
		cfg  []piondtls.ClientOption
		want byte
	}{
		{"unknown PSK identity", pskConfig("nobody", []byte("0123456789abcdef")), 80},
		{"untrusted certificate", testclient.X509Config(foreign, s.pki.pool, "lwm2m.test"), 42},
	} {
		if got := s.alertFor(c.cfg); got != c.want {
			t.Errorf("%s: server alert %d, want %d", c.name, got, c.want)
		}
	}

	px := newProxy(t, s.addr)
	c := s.mustRegisterSecure(testclient.Config{Endpoint: "mac"}, px.Addr(), pskConfig("mac", []byte("0123456789abcdef")))
	var corrupted atomic.Bool
	px.mu.Lock()
	px.tamper = func(d datagram) []byte {
		if d.up && d.data[0] == recAppData && corrupted.CompareAndSwap(false, true) {
			bad := append([]byte(nil), d.data...)
			bad[len(bad)-1] ^= 0xff // breaks the CCM_8 tag
			return bad
		}
		return d.data
	}
	px.mu.Unlock()
	m := px.mark()
	u, err := c.Update(s.ctx, nil, nil)
	mustCode(t, u, err, "2.04")
	if !corrupted.Load() {
		t.Fatal("nothing corrupted")
	}
	for _, d := range px.since(m) {
		if !d.up && d.data[0] == recAlert {
			t.Fatal("server answered a bad MAC with an alert")
		}
	}
}

// Proves: SEC-01, SEC-02
// The client starts the handshake and the server only answers it, also
// when it has downlink for a client whose session is gone. No LwM2M data
// is exchanged before authentication; after it everything is encrypted
// (the endpoint name never appears on the wire) and a replayed record is
// dropped by DTLS replay protection: the server does not answer it, not
// even with the cached CoAP response. Tampered records are discarded
// (TestDTLSAlerts).
func TestDTLSRecordProtection(t *testing.T) {
	h := newHarness(t)
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "urn:dev:secret", PSKIdentity: "pid", PSKKey: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	addr := h.listenSecure(CertificateModes{})
	px := newProxy(t, addr)
	c := h.mustRegisterSecure(testclient.Config{Endpoint: "urn:dev:secret"}, px.Addr(), pskConfig("pid", []byte("0123456789abcdef")))
	all := px.since(0)
	if len(all) == 0 || !all[0].up || handshakeMsgs(all[:1], true)[0] != byte(handshake.TypeClientHello) {
		t.Fatal("the first datagram is not the client's ClientHello")
	}
	for _, d := range all {
		for _, r := range records(d.data, 8) {
			if r.epoch == 0 && r.typ != recHandshake && r.typ != 20 { // only handshake and CCS in clear
				t.Fatalf("unprotected record type %d", r.typ)
			}
		}
		if bytes.Contains(d.data, []byte("urn:dev:secret")) {
			t.Fatal("endpoint name visible on the wire")
		}
	}

	m := px.mark()
	u, err := c.Update(h.ctx, nil, nil)
	mustCode(t, u, err, "2.04")
	var update []byte
	for _, d := range px.since(m) {
		if d.up {
			update = d.data
			break
		}
	}
	m = px.mark()
	px.mu.Lock()
	_, _ = px.up.WriteToUDP(update, px.server)
	px.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	for _, d := range px.since(m) {
		if !d.up {
			t.Fatal("server answered a replayed record")
		}
	}

	// Session gone: the server never initiates a handshake.
	_ = c.Close()
	time.Sleep(100 * time.Millisecond)
	if _, err := h.srv.Read(h.ctx, "urn:dev:secret", p("/3/0/0"), server.ReadOptions{}); err == nil {
		t.Fatal("read succeeded on a closed session")
	}
	for _, typ := range handshakeMsgs(px.since(0), false) {
		if typ == byte(handshake.TypeClientHello) || typ == byte(handshake.TypeHelloRequest) {
			t.Fatal("server started a handshake")
		}
	}
}

// Proves: SEC-16, TLS13-03, TLS13-04
// The server uses the client's most preferred group it supports (/0/x/18
// order), secp256r1 works for ECDHE and ECDSA, and a client offering only
// a curve under 255 bits (secp192r1) or presenting a P-224 key is
// refused. A client limited to ecdsa_secp256r1_sha256 (0x0403, the
// /0/x/19 and /0/x/21 value for this server) completes the handshake.
func TestCurvesAndSignatures(t *testing.T) {
	s := newSecure(t)
	xc := s.x509Client("curves")
	for _, groups := range [][]pionelliptic.Curve{{pionelliptic.P384, pionelliptic.P256}, {pionelliptic.P256, pionelliptic.P384}} {
		cfg := with(testclient.X509Config(xc, s.pki.pool, "lwm2m.test"),
			piondtls.WithEllipticCurves(groups...), piondtls.WithSignatureSchemes(tls.ECDSAWithP256AndSHA256))
		px := newProxy(t, s.addr) // one proxy per session: the server keys sessions by address
		s.mustRegisterSecure(testclient.Config{Endpoint: "curves"}, px.Addr(), cfg)
		if got := serverKeyExchangeCurve(px.since(0)); got != uint16(groups[0]) {
			t.Fatalf("groups %v: server used %#x", groups, got)
		}
	}
	cfg := with(testclient.X509Config(xc, s.pki.pool, "lwm2m.test"), piondtls.WithClientHelloMessageHook(func(ch handshake.MessageClientHello) handshake.Message {
		for i, e := range ch.Extensions {
			if _, ok := e.(*extension.SupportedGroups); ok {
				ch.Extensions[i] = &extension.SupportedGroups{Groups: []pionelliptic.Curve{19}} // secp192r1
			}
		}
		return &ch
	}))
	s.handshakeFails(testclient.Config{Endpoint: "curves"}, s.addr, cfg)

	weak, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.srv.Security().Put(server.SecurityInfo{Endpoint: "weak", X509: true}); err != nil {
		t.Fatal(err)
	}
	s.handshakeFails(testclient.Config{Endpoint: "weak"}, s.addr, testclient.X509Config(s.pki.issueKey(t, weak, "weak"), s.pki.pool, "lwm2m.test"))
}

// alertFor runs a handshake that the server must refuse and returns the
// description of the fatal alert the server sent (plaintext in epoch 0).
func (s *secureSetup) alertFor(cfg []piondtls.ClientOption) byte {
	s.t.Helper()
	px := newProxy(s.t, s.addr)
	s.handshakeFails(testclient.Config{Endpoint: "x"}, px.Addr(), cfg)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range px.since(0) {
			for _, r := range records(d.data, 8) {
				if !d.up && r.typ == recAlert && r.epoch == 0 && len(r.body) == 2 && r.body[0] == 2 {
					return r.body[1]
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return 0
}

// serverKeyExchangeCurve returns the named curve of the server's
// ServerKeyExchange (RFC 8422 §5.4: curve_type 3, then the curve).
func serverKeyExchangeCurve(ds []datagram) uint16 {
	for _, d := range ds {
		if d.up {
			continue
		}
		for _, r := range records(d.data, 0) {
			if r.typ == recHandshake && r.epoch == 0 && len(r.body) > 15 && r.body[0] == byte(handshake.TypeServerKeyExchange) {
				return uint16(r.body[13])<<8 | uint16(r.body[14])
			}
		}
	}
	return 0
}

// Proves: SEC-21
// The Security object is never accessible to a LwM2M Server: Write,
// Observe, Discover, Delete, Create and Write-Attributes on /0 are
// refused before anything is sent, like Read (TestServerValidatesTargets).
func TestSecurityObjectInaccessible(t *testing.T) {
	h := newHarness(t)
	c := h.registered("sec0")
	ctx := h.ctx
	_, _, obsErr := h.srv.Observe(ctx, "sec0", p("/0/0"), server.ObserveOptions{})
	for i, err := range []error{
		second(h.srv.Write(ctx, "sec0", p("/0/0/0"), []lwm2m.Node{lwm2m.ValueNode(p("/0/0/0"), lwm2m.String("coap://evil"))}, server.WriteOptions{})),
		obsErr,
		second(h.srv.Discover(ctx, "sec0", p("/0"), nil)),
		second(h.srv.Delete(ctx, "sec0", p("/0/1"))),
		second(h.srv.Create(ctx, "sec0", p("/0"), nil, nil)),
		second(h.srv.WriteAttributes(ctx, "sec0", p("/0/0"), []string{"pmin=1"})),
		second(h.srv.WriteComposite(ctx, "sec0", []lwm2m.Node{lwm2m.ValueNode(p("/0/0/0"), lwm2m.String("coap://evil"))}, nil)),
	} {
		if !errors.Is(err, server.ErrBadRequest) {
			t.Errorf("operation %d on /0: err %v, want ErrBadRequest", i, err)
		}
	}
	if n := len(c.Requests()); n != 0 {
		t.Fatalf("%d requests on /0 reached the client", n)
	}
}
