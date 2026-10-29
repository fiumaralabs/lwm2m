package coap

import (
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v4"
)

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
	addr := h.listenSecure(CertificateModes{})
	proxy := newProxy(t, addr)
	addr = proxy.Addr()
	sessions := &clientSessions{m: map[string]piondtls.Session{}}
	cfg := with(pskConfig("rotate", old), piondtls.WithSessionStore(sessions))
	c := h.mustRegisterSecure(testclient.Config{Endpoint: "rotate"}, addr, cfg)
	_ = c.Close()
	h.srv.RemoveRegistration("rotate")
	if remove {
		h.srv.Security().Remove("rotate")
	} else {
		if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "rotate", PSKIdentity: "rotate", PSKKey: []byte("fedcba9876543210")}); err != nil {
			t.Fatal(err)
		}
	}
	c2, err := h.secureDevice(testclient.Config{Endpoint: "rotate"}, addr, cfg)
	if err != nil {
		return
	}
	r, err := c2.Register(h.ctx)
	if err == nil && server.CodeString(r.Code) == "2.01" {
		t.Fatal("old cached PSK session registered after key rotation")
	}
}
