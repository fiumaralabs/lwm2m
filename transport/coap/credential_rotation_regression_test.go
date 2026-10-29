package coap

import (
	"bytes"
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/dtls/v4/pkg/protocol/handshake"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v4"
)

type forbiddenServerSessions struct{ calls atomic.Int32 }

func (s *forbiddenServerSessions) Set(_ []byte, _ piondtls.Session) error { s.calls.Add(1); return nil }
func (s *forbiddenServerSessions) Get(_ []byte) (piondtls.Session, error) {
	s.calls.Add(1)
	return piondtls.Session{}, nil
}
func (s *forbiddenServerSessions) Del(_ []byte) error { s.calls.Add(1); return nil }

func TestRotatedPSKDoesNotResumeOldSession(t *testing.T) {
	testInvalidatedPSKDoesNotResume(t, false)
}

func TestRemovedPSKDoesNotResumeOldSession(t *testing.T) {
	testInvalidatedPSKDoesNotResume(t, true)
}

func testInvalidatedPSKDoesNotResume(t *testing.T, remove bool) {
	t.Helper()
	h := newHarness(t)
	old := []byte("0123456789abcdef")
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "rotate", PSKIdentity: "rotate", PSKKey: old}); err != nil {
		t.Fatal(err)
	}
	external := &forbiddenServerSessions{}
	addr := h.listenSecure(CertificateModes{}, piondtls.WithSessionStore(external))
	proxy := newProxy(t, addr)
	addr = proxy.Addr()
	sessions := &clientSessions{m: map[string]piondtls.Session{}}
	cfg := with(pskConfig("rotate", old), piondtls.WithSessionStore(sessions))
	c := h.mustRegisterSecure(testclient.Config{Endpoint: "rotate"}, addr, cfg)
	_ = c.Close()
	if got := external.calls.Load(); got != 0 {
		t.Fatalf("PSK listener used supplied session store %d times", got)
	}
	h.srv.RemoveRegistration("rotate")
	if remove {
		h.srv.Security().Remove("rotate")
	} else {
		if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "rotate", PSKIdentity: "rotate", PSKKey: []byte("fedcba9876543210")}); err != nil {
			t.Fatal(err)
		}
	}
	peer, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := piondtls.Dial("udp", peer, cfg...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()
	err = c2.HandshakeContext(ctx)
	_ = c2.Close()
	if err == nil {
		t.Fatal("old cached PSK session resumed after credential change")
	}

	if !remove {
		cfgNew := with(pskConfig("rotate", []byte("fedcba9876543210")), piondtls.WithSessionStore(sessions))
		h.mustRegisterSecure(testclient.Config{Endpoint: "rotate"}, newProxy(t, h.listenSecure(CertificateModes{})).Addr(), cfgNew)
	}
}

func TestRotatingBackDoesNotReviveCachedSession(t *testing.T) {
	h := newHarness(t)
	keyA := []byte("0123456789abcdef")
	put := func(key []byte) {
		t.Helper()
		if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "rotate-back", PSKIdentity: "rotate-back", PSKKey: key}); err != nil {
			t.Fatal(err)
		}
	}
	put(keyA)
	proxy := newProxy(t, h.listenSecure(CertificateModes{}))
	sessions := &clientSessions{m: map[string]piondtls.Session{}}
	cfg := with(pskConfig("rotate-back", keyA), piondtls.WithSessionStore(sessions))
	c := h.mustRegisterSecure(testclient.Config{Endpoint: "rotate-back"}, proxy.Addr(), cfg)
	_ = c.Close()
	h.srv.RemoveRegistration("rotate-back")
	put([]byte("fedcba9876543210"))
	put(keyA)
	m := proxy.mark()
	h.mustRegisterSecure(testclient.Config{Endpoint: "rotate-back"}, proxy.Addr(), cfg)
	messages := handshakeMsgs(proxy.since(m), true)
	if bytes.IndexByte(messages, byte(handshake.TypeClientKeyExchange)) < 0 {
		t.Fatalf("cached session revived after rotate-back: %v", messages)
	}
}
