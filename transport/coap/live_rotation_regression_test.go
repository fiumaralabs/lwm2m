package coap

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v4"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestRotatedPSKRejectsPreviouslyAuthenticatedLiveSession(t *testing.T) {
	h := newHarness(t)
	old := []byte("0123456789abcdef")
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "live-rotation", PSKIdentity: "live-id", PSKKey: old}); err != nil {
		t.Fatal(err)
	}
	c := h.mustRegisterSecure(testclient.Config{Endpoint: "live-rotation"}, h.dtls, pskConfig("live-id", old))
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "unrelated", PSKIdentity: "unrelated-id", PSKKey: old}); err != nil {
		t.Fatal(err)
	}
	unrelated := h.mustRegisterSecure(testclient.Config{Endpoint: "unrelated"}, h.dtls, pskConfig("unrelated-id", old))
	// A second authenticated session may never have registered and therefore
	// cannot be reached through the registration store.
	other, err := h.secureDevice(testclient.Config{Endpoint: "live-rotation"}, h.dtls, pskConfig("live-id", old))
	if err != nil {
		t.Fatal(err)
	}
	if r, err := other.Register(h.ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("second session failed to register: %v %v", r, err)
	}
	// Re-register on the first session so the second session is live but is
	// no longer the registration's current peer.
	if r, err := c.Register(h.ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("first session failed to re-register: %v %v", r, err)
	}
	if !h.srv.RemoveRegistration("live-rotation") {
		t.Fatal("missing registration")
	}
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "live-rotation", PSKIdentity: "live-id", PSKKey: []byte("fedcba9876543210")}); err != nil {
		t.Fatal(err)
	}
	h.b.RevokePSKIdentity("live-id")
	if r, err := unrelated.Register(h.ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("unrelated session affected by revocation: %v %v", r, err)
	}
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()
	response, err := other.Register(ctx)
	if err == nil && response.Code == codes.Created {
		t.Fatal("old live PSK session registered after key rotation")
	}
	newClient := h.mustRegisterSecure(testclient.Config{Endpoint: "live-rotation"}, h.dtls, pskConfig("live-id", []byte("fedcba9876543210")))
	if r, err := newClient.Register(h.ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("new key failed after revocation: %v %v", r, err)
	}
}

func TestRevokePSKDuringHandshake(t *testing.T) {
	h := newHarness(t)
	old := []byte("0123456789abcdef")
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "inflight", PSKIdentity: "inflight-id", PSKKey: old}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	addr, err := h.b.ListenDTLS("127.0.0.1:0", DTLSConfig{Options: []piondtls.ServerOption{
		piondtls.WithVerifyConnection(func(*piondtls.State) error {
			close(entered)
			<-release
			return nil
		}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()
	a, err := net.ResolveUDPAddr("udp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	c, err := piondtls.Dial("udp", a, pskConfig("inflight-id", old)...)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() {
		result <- c.HandshakeContext(ctx)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("handshake never reached verification")
	}
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "inflight", PSKIdentity: "inflight-id", PSKKey: []byte("fedcba9876543210")}); err != nil {
		close(release)
		t.Fatal(err)
	}
	h.b.RevokePSKIdentity("inflight-id")
	close(release)
	if err := <-result; err == nil {
		t.Fatal("old in-flight handshake succeeded")
	}
}
