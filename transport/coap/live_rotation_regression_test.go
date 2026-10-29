package coap

import (
	"context"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestRotatedPSKRejectsPreviouslyAuthenticatedLiveSession(t *testing.T) {
	h := newHarness(t)
	old := []byte("0123456789abcdef")
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "live-rotation", PSKIdentity: "live-id", PSKKey: old}); err != nil {
		t.Fatal(err)
	}
	c := h.mustRegisterSecure(testclient.Config{Endpoint: "live-rotation"}, h.dtls, pskConfig("live-id", old))
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
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()
	response, err := other.Register(ctx)
	if err == nil && response.Code == codes.Created {
		t.Fatal("old live PSK session registered after key rotation")
	}
}
