package coap

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/dtlscoap"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	dtls "github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/protocol"
)

type countingSessionStore struct {
	*dtlscoap.SessionStore
	writes atomic.Int32
	hits   atomic.Int32
}

func (s *countingSessionStore) Set(key []byte, session dtls.Session) error {
	s.writes.Add(1)
	return s.SessionStore.Set(key, session)
}

func (s *countingSessionStore) Get(key []byte) (dtls.Session, error) {
	session, err := s.SessionStore.Get(key)
	if len(session.ID) > 0 {
		s.hits.Add(1)
	}
	return session, err
}

func TestMixedListenerCertificateFullHandshake(t *testing.T) {
	h := newHarness(t)
	p := newDTLSPKI(t)
	serverCert := p.issue(t, "lwm2m.test", "lwm2m.test")
	clientCert := p.issue(t, "client")
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "client", X509: true}); err != nil {
		t.Fatal(err)
	}
	serverStore := &countingSessionStore{SessionStore: dtlscoap.NewSessionStore(time.Hour, 100, time.Now)}
	addr := h.listenSecure(CertificateModes{Certificates: []tls.Certificate{serverCert}, ClientCAs: p.pool}, dtls.WithMaxVersion(protocol.Version1_2), dtls.WithSessionStore(serverStore))
	remote, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	sessions := &countingSessionStore{SessionStore: dtlscoap.NewSessionStore(time.Hour, 100, time.Now)}
	// Seed a prior session so the first ClientHello attempts resumption even
	// when this listener never issues a resumable session of its own.
	if err := sessions.Set([]byte(remote.String()+"_lwm2m.test"), dtls.Session{ID: make([]byte, 32), Secret: make([]byte, 48)}); err != nil {
		t.Fatal(err)
	}
	var verified atomic.Int32
	opts := append(testclient.X509Config(clientCert, p.pool, "lwm2m.test"), dtls.WithMaxVersion(protocol.Version1_2), dtls.WithSessionStore(sessions), dtls.WithVerifyPeerCertificate(func(_ [][]byte, _ [][]*x509.Certificate) error { verified.Add(1); return nil }))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		c, err := dtls.Dial("udp", remote, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.HandshakeContext(ctx); err != nil {
			_ = c.Close()
			t.Fatal(err)
		}
		// A response confirms the server has completed its handshake before
		// the next connection attempts to resume the client session.
		if _, err := c.Write([]byte{0x40, 0x01, 0x00, 0x01}); err != nil {
			t.Fatal(err)
		}
		if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Read(make([]byte, 1024)); err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if sessions.hits.Load() == 0 {
		t.Fatal("client did not attempt resumption")
	}
	if got := serverStore.writes.Load(); got != 0 {
		t.Fatalf("ignored server store received %d writes", got)
	}
	if got := verified.Load(); got != 2 {
		t.Fatalf("certificate verified %d times; want a full handshake on each connection", got)
	}
}
