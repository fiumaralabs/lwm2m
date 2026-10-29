package fota_test

import (
	"context"
	"crypto/x509"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/fota"
	"github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/crypto/ciphersuite"
	"github.com/pion/dtls/v4/pkg/crypto/selfsign"
	"github.com/pion/dtls/v4/pkg/protocol"
)

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]dtls.Session
	sets     int
}

func (s *sessionStore) Set(k []byte, v dtls.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets++
	s.sessions[string(k)] = v
	return nil
}
func (s *sessionStore) Get(k []byte) (dtls.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[string(k)], nil
}
func (s *sessionStore) Del(k []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, string(k))
	return nil
}

func TestFileServerPSKDoesNotCacheSessions(t *testing.T) {
	key := []byte("0123456789abcdef")
	psk := func([]byte) ([]byte, error) { return key, nil }
	serverSessions := &sessionStore{sessions: make(map[string]dtls.Session)}
	server := fota.NewFileServer()
	defer server.Close()
	addr, err := server.ListenDTLS("127.0.0.1:0", dtls.WithPSK(psk), dtls.WithMaxVersion(protocol.Version1_2), dtls.WithSessionStore(serverSessions))
	if err != nil {
		t.Fatal(err)
	}
	clients := &sessionStore{sessions: make(map[string]dtls.Session)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		c, err := dtls.Dial("udp", addr.(*net.UDPAddr), dtls.WithPSK(psk), dtls.WithPSKIdentityHint([]byte("device")), dtls.WithCipherSuites(ciphersuite.TLS_PSK_WITH_AES_128_CCM_8), dtls.WithMaxVersion(protocol.Version1_2), dtls.WithSessionStore(clients))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.HandshakeContext(ctx); err != nil {
			c.Close()
			t.Fatal(err)
		}
		c.Close()
	}
	serverSessions.mu.Lock()
	sets := serverSessions.sets
	serverSessions.mu.Unlock()
	if sets != 0 {
		t.Fatalf("PSK listener wrote %d sessions to caller store", sets)
	}
}

func TestFileServerCertificateOnlyOptInResumes(t *testing.T) {
	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	store := &sessionStore{sessions: make(map[string]dtls.Session)}
	server := fota.NewFileServer()
	server.CertificateOnlySessionResumption = true
	defer server.Close()
	addr, err := server.ListenDTLS("127.0.0.1:0", dtls.WithCertificates(cert), dtls.WithMaxVersion(protocol.Version1_2), dtls.WithSessionStore(store))
	if err != nil {
		t.Fatal(err)
	}
	clients := &sessionStore{sessions: make(map[string]dtls.Session)}
	var verified atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		c, err := dtls.Dial("udp", addr.(*net.UDPAddr), dtls.WithInsecureSkipVerify(true), dtls.WithMaxVersion(protocol.Version1_2), dtls.WithSessionStore(clients), dtls.WithVerifyPeerCertificate(func(_ [][]byte, _ [][]*x509.Certificate) error { verified.Add(1); return nil }))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.HandshakeContext(ctx); err != nil {
			c.Close()
			t.Fatal(err)
		}
		c.Close()
	}
	store.mu.Lock()
	sets := store.sets
	store.mu.Unlock()
	if sets == 0 {
		t.Fatal("caller-supplied certificate store was not used")
	}
	if got := verified.Load(); got != 1 {
		t.Fatalf("certificate verified %d times, want one full handshake followed by resumption", got)
	}
}
