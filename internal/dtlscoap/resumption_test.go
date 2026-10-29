package dtlscoap

import (
	"context"
	"crypto/x509"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/dtls/v4"
	"github.com/pion/dtls/v4/pkg/crypto/selfsign"
	"github.com/pion/dtls/v4/pkg/protocol"
)

type testSessions struct {
	mu sync.Mutex
	m  map[string]dtls.Session
}

func (s *testSessions) Set(k []byte, v dtls.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[string(k)] = v
	return nil
}
func (s *testSessions) Get(k []byte) (dtls.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[string(k)], nil
}
func (s *testSessions) Del(k []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, string(k))
	return nil
}

func TestCertificateOnlyListenerResumes(t *testing.T) {
	cert, err := selfsign.GenerateSelfSigned()
	if err != nil {
		t.Fatal(err)
	}
	serverSessions := &testSessions{m: make(map[string]dtls.Session)}
	cfg := ServerConfig([]dtls.ServerOption{dtls.WithCertificates(cert), dtls.WithMaxVersion(protocol.Version1_2), dtls.WithSessionStore(serverSessions)}, 0, 0, nil)
	l, err := Listen("udp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, err := l.AcceptWithContext(ctx)
			if err != nil {
				errs <- err
				return
			}
			err = c.(*Conn).HandshakeContext(ctx)
			errs <- err
			if err != nil {
				_ = c.Close()
				return
			}
		}
	}()
	addr := l.Addr().(*net.UDPAddr)
	sessions := &testSessions{m: make(map[string]dtls.Session)}
	var verified atomic.Int32
	opts := []dtls.ClientOption{dtls.WithInsecureSkipVerify(true), dtls.WithMaxVersion(protocol.Version1_2), dtls.WithSessionStore(sessions), dtls.WithVerifyPeerCertificate(func(_ [][]byte, _ [][]*x509.Certificate) error { verified.Add(1); return nil })}
	for i := 0; i < 2; i++ {
		c, err := dtls.Dial("udp", addr, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.HandshakeContext(ctx); err != nil {
			t.Fatal(err)
		}
		if err = <-errs; err != nil {
			t.Fatal(err)
		}
		_ = c.Close()
	}
	serverSessions.mu.Lock()
	stored := len(serverSessions.m)
	serverSessions.mu.Unlock()
	if stored == 0 {
		t.Fatal("caller-supplied server session store was not used")
	}
	if got := verified.Load(); got != 1 {
		t.Fatalf("certificate verified %d times; want one full handshake then resumption", got)
	}
}
