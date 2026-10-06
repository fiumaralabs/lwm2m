package server

import (
	"bytes"
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

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// pki is a test CA with a server certificate for 127.0.0.1.
type pki struct {
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	pool   *x509.CertPool
	server tls.Certificate
}

func newPKI(t *testing.T) *pki {
	t.Helper()
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
	k.server = k.leaf(t, "server", x509.ExtKeyUsageServerAuth)
	return k
}

func (k *pki) leaf(t *testing.T, cn string, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, k.ca, &key.PublicKey, k.caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tcpHarness adds a TCP and a TLS (X.509 client auth) listener.
type tcpHarness struct {
	*harness
	tcp, tls string
	pki      *pki
}

func newTCPHarness(t *testing.T) *tcpHarness {
	t.Helper()
	h := &tcpHarness{harness: newHarness(t), pki: newPKI(t)}
	a, err := h.srv.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h.tcp = a.String()
	a, err = h.srv.ListenTLS("127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{h.pki.server},
		ClientCAs: h.pki.pool, ClientAuth: tls.RequireAndVerifyClientCert})
	if err != nil {
		t.Fatal(err)
	}
	h.tls = a.String()
	return h
}

// device dials a TCP client; cn != "" dials TLS with a client certificate
// for that CN. The store matches harness.device.
func (h *tcpHarness) device(cfg testclient.Config, cn string) (*testclient.TCPClient, error) {
	c := testclient.NewTCP(cfg)
	c.Set(p("/1/0/0"), lwm2m.Integer(1))
	c.Set(p("/1/0/1"), lwm2m.Integer(86400))
	c.Set(p("/1/0/7"), lwm2m.String("T"))
	c.Set(p("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	c.Set(p("/3/0/9"), lwm2m.Integer(100))
	c.Set(p("/3/0/11/0"), lwm2m.Integer(0))
	c.Set(p("/3/0/16"), lwm2m.String("T"))
	addr, tc := h.tcp, (*tls.Config)(nil)
	if cn != "" {
		addr = h.tls
		tc = &tls.Config{RootCAs: h.pki.pool, Certificates: []tls.Certificate{h.pki.leaf(h.t, cn, x509.ExtKeyUsageClientAuth)},
			NextProtos: []string{"coap"}}
	}
	if err := c.Dial(addr, tc); err != nil {
		return nil, err
	}
	h.t.Cleanup(func() { _ = c.Close() })
	return c, nil
}

// registered returns a client registered over TCP, or over TLS with an
// X.509 identity whose CN is ep.
func (h *tcpHarness) registered(ep string, overTLS bool) *testclient.TCPClient {
	h.t.Helper()
	cn := ""
	if overTLS {
		cn = ep
		if err := h.srv.Security().Put(SecurityInfo{Endpoint: ep, X509: true}); err != nil {
			h.t.Fatal(err)
		}
	}
	c, err := h.device(testclient.Config{Endpoint: ep, Binding: "T"}, cn)
	if err != nil {
		h.t.Fatal(err)
	}
	r, err := c.Register(h.ctx)
	mustCode(h.t, r, err, "2.01")
	return c
}

func eachTCP(t *testing.T, f func(t *testing.T, h *tcpHarness, overTLS bool)) {
	for _, overTLS := range []bool{false, true} {
		name := "tcp"
		if overTLS {
			name = "tls"
		}
		t.Run(name, func(t *testing.T) { f(t, newTCPHarness(t), overTLS) })
	}
}

// Proves: TCP-01, TCP-02, GEN-12
// Over coap+tcp and coaps+tcp the server sends its CSM first and answers
// Ping with Pong (RFC 8323 §5.3, §5.4). Register, Update and De-register
// work; the registration's session is binding T, and Read and Write go
// back over that TCP connection while a UDP client is served over UDP.
func TestTCPRegistrationAndDM(t *testing.T) {
	eachTCP(t, func(t *testing.T, h *tcpHarness, overTLS bool) {
		c := h.registered("tcp-ep", overTLS) // Dial fails without the server CSM
		if err := c.Ping(h.ctx); err != nil {
			t.Fatalf("ping: %v", err)
		}
		reg, ok := h.srv.Store().ByEndpoint("tcp-ep")
		if !ok || reg.peer.Binding() != "T" || reg.Binding != "T" {
			t.Fatalf("registration %+v", reg)
		}
		wantMode := ModeNoSec
		if overTLS {
			wantMode = ModeX509
		}
		if reg.Identity.Mode != wantMode {
			t.Fatalf("identity %v, want %v", reg.Identity.Mode, wantMode)
		}
		u := h.harness.registered("udp-ep")

		r := readOK(t, h.harness, "tcp-ep", p("/3/0/9"))
		if len(r.Nodes) != 1 || !r.Nodes[0].Value.Equal(lwm2m.Integer(100)) {
			t.Fatalf("read %+v", r)
		}
		if req, _ := c.LastRequest(); req.Code != codes.GET || req.Path != "/3/0/9" {
			t.Fatalf("TCP client got %+v", req)
		}
		readOK(t, h.harness, "udp-ep", p("/3/0/9"))
		if n := len(c.Requests()); n != 1 {
			t.Fatalf("UDP read reached the TCP client (%d requests)", n)
		}
		if req, _ := u.LastRequest(); req.Path != "/3/0/9" {
			t.Fatalf("UDP client got %+v", req)
		}

		r2, err := h.srv.Write(h.ctx, "tcp-ep", p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(300))}, WriteOptions{})
		mustResp(t, r2, err, "2.04")
		if v, _ := c.Get(p("/1/0/1")); !v.Equal(lwm2m.Integer(300)) {
			t.Fatalf("write not applied: %v", v)
		}

		ur, err := c.Update(h.ctx, []string{"lt=600"})
		mustCode(t, ur, err, "2.04")
		h.ev.wait(t, func(e Event) bool { u, ok := e.(Updated); return ok && u.Registration.Endpoint == "tcp-ep" })
		dr, err := c.Deregister(h.ctx)
		mustCode(t, dr, err, "2.02")
		if _, ok := h.srv.Store().ByEndpoint("tcp-ep"); ok {
			t.Fatal("still registered")
		}
	})
}

func readOK(t *testing.T, h *harness, ep string, path lwm2m.Path) *Response {
	t.Helper()
	r, err := h.srv.Read(h.ctx, ep, path, ReadOptions{})
	return mustResp(t, r, err, "2.05")
}

// Proves: GEN-13
// An Update that changes the binding (b=U) is recorded for future
// sessions but does not move the current one: requests still go over the
// TCP connection the client registered on.
func TestTCPBindingChangeKeepsSession(t *testing.T) {
	h := newTCPHarness(t)
	c := h.registered("gen13", false)
	r, err := c.Update(h.ctx, []string{"b=U"})
	mustCode(t, r, err, "2.04")
	reg, _ := h.srv.Store().ByEndpoint("gen13")
	if reg.Binding != "U" || reg.peer.Binding() != "T" {
		t.Fatalf("binding %q, session %q", reg.Binding, reg.peer.Binding())
	}
	readOK(t, h.harness, "gen13", p("/3/0/9"))
	if req, _ := c.LastRequest(); req.Path != "/3/0/9" {
		t.Fatalf("read did not use the TCP session: %+v", req)
	}
}

// Proves: TCP-01, OBS-01, OBS-02
// Observe over TCP: notifications need no ACK and their Observe value is
// ignored, so an empty or lower value is still delivered (RFC 8323 §7.1).
// Without Reset, a notification for a forgotten token is dropped and the
// connection stays up; active cancel is GET with Observe=1 on the same
// token (RFC 8323 §7.4).
func TestTCPObserve(t *testing.T) {
	eachTCP(t, func(t *testing.T, h *tcpHarness, overTLS bool) {
		c := h.registered("tcp-obs", overTLS)
		ob, r, err := h.srv.Observe(h.ctx, "tcp-obs", p("/3/0/9"), ObserveOptions{})
		if err != nil || ob == nil || !r.Success() {
			t.Fatalf("observe: %v %+v", err, r)
		}
		req, _ := c.LastRequest()
		if req.Observe == nil || *req.Observe != 0 {
			t.Fatalf("request %+v", req)
		}
		for i, seq := range []uint32{5, 3, 0} { // 3 and the empty value would be stale over UDP
			c.Set(p("/3/0/9"), lwm2m.Integer(int64(10+i)))
			if err := c.Notify(h.ctx, req.Token, seq); err != nil {
				t.Fatal(err)
			}
			n := notification(t, h.harness, ob)
			if !n.Response.Nodes[0].Value.Equal(lwm2m.Integer(int64(10 + i))) {
				t.Fatalf("notification %d: %+v", i, n.Response)
			}
		}

		// Active cancel.
		cr, err := h.srv.CancelObservation(h.ctx, ob, true)
		mustResp(t, cr, err, "2.05")
		last, _ := c.LastRequest()
		if last.Code != codes.GET || last.Observe == nil || *last.Observe != 1 || !bytes.Equal(last.Token, req.Token) {
			t.Fatalf("cancel request %+v", last)
		}

		// Passive cancel: over a reliable transport there is no Reset, so the
		// server cancels explicitly (RFC 8323 §7.4); a late notification is
		// dropped, not Reset.
		ob2, _, err := h.srv.Observe(h.ctx, "tcp-obs", p("/3/0/9"), ObserveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		req2, _ := c.LastRequest()
		if _, err := h.srv.CancelObservation(h.ctx, ob2, false); err != nil {
			t.Fatal(err)
		}
		if last, _ := c.LastRequest(); last.Observe == nil || *last.Observe != 1 || !bytes.Equal(last.Token, req2.Token) {
			t.Fatalf("passive cancel over TCP did not send Observe=1: %+v", last)
		}
		if err := c.Notify(h.ctx, req2.Token, 9); err != nil {
			t.Fatal(err)
		}
		if err := c.Ping(h.ctx); err != nil {
			t.Fatalf("connection lost after unknown notification: %v", err)
		}
		// The Read response follows the notification on the ordered
		// connection, so the notification was processed by now.
		readOK(t, h.harness, "tcp-obs", p("/3/0/9"))
		h.ev.mu.Lock()
		defer h.ev.mu.Unlock()
		for _, e := range h.ev.l {
			if n, ok := e.(Notification); ok && n.Observation.ID == ob2.ID {
				t.Fatal("notification for a cancelled observation delivered")
			}
		}
	})
}

// Proves: TCP-01, SEND-01
// Send on /dp over TCP and TLS is accepted and delivered as an event.
func TestTCPSend(t *testing.T) {
	eachTCP(t, func(t *testing.T, h *tcpHarness, overTLS bool) {
		c := h.registered("tcp-send", overTLS)
		nodes := []lwm2m.Node{lwm2m.ValueNode(p("/3/0/9"), lwm2m.Integer(42))}
		r, err := c.Send(h.ctx, nodes, lwm2m.FormatSenMLCBOR)
		mustCode(t, r, err, "2.04")
		ev := h.ev.wait(t, func(e Event) bool { _, ok := e.(SendReceived); return ok }).(SendReceived)
		if ev.Registration.Endpoint != "tcp-send" || !lwm2m.NodesEqual(ev.Nodes, nodes) {
			t.Fatalf("event %+v", ev)
		}
	})
}

// Proves: REG-02, SEC-06, SEC-07
// Over TLS the verified client certificate is the identity: without ep the
// CN is the endpoint, an ep other than the CN is 4.00, and a plain-TCP
// (NoSec) Register for an endpoint with X.509 credentials is 4.00.
func TestTLSIdentity(t *testing.T) {
	h := newTCPHarness(t)
	if err := h.srv.Security().Put(SecurityInfo{Endpoint: "cert-ep", X509: true}); err != nil {
		t.Fatal(err)
	}
	c, err := h.device(testclient.Config{}, "cert-ep")
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Register(h.ctx)
	mustCode(t, r, err, "2.01")
	if reg, ok := h.srv.Store().ByEndpoint("cert-ep"); !ok || reg.Identity.CertCN != "cert-ep" {
		t.Fatalf("registration %+v", reg)
	}

	other, err := h.device(testclient.Config{Endpoint: "cert-ep"}, "intruder")
	if err != nil {
		t.Fatal(err)
	}
	r, err = other.Register(h.ctx)
	mustCode(t, r, err, "4.00")

	plain, err := h.device(testclient.Config{Endpoint: "cert-ep"}, "")
	if err != nil {
		t.Fatal(err)
	}
	r, err = plain.Register(h.ctx)
	mustCode(t, r, err, "4.00")

	// A certificate from an unknown CA fails the handshake.
	bad := testclient.NewTCP(testclient.Config{Endpoint: "x"})
	self := newPKI(t).leaf(t, "x", x509.ExtKeyUsageClientAuth)
	if err := bad.Dial(h.tls, &tls.Config{RootCAs: h.pki.pool, Certificates: []tls.Certificate{self}}); err == nil {
		_ = bad.Close()
		t.Fatal("untrusted client certificate accepted")
	}
}
