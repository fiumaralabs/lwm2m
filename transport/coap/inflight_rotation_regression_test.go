package coap

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	piondtls "github.com/pion/dtls/v4"
)

type pausedPSKStore struct {
	server.SecurityStore
	lookup chan struct{}
	resume chan struct{}
}

func (s *pausedPSKStore) ByPSKIdentity(id string) (server.SecurityInfo, bool) {
	info, ok := s.SecurityStore.ByPSKIdentity(id)
	close(s.lookup)
	<-s.resume
	return info, ok
}

func TestInFlightPSKHandshakeRejectedAfterRotation(t *testing.T) {
	for _, remove := range []bool{false, true} {
		name := "put"
		if remove {
			name = "remove"
		}
		t.Run(name, func(t *testing.T) {
			backing := &pausedPSKStore{SecurityStore: server.NewMemorySecurityStore(), lookup: make(chan struct{}), resume: make(chan struct{})}
			h := newHarness(t, func(cfg *server.Config) { cfg.Security = backing })
			key := []byte("0123456789abcdef")
			if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "device", PSKIdentity: "identity", PSKKey: key}); err != nil {
				t.Fatal(err)
			}
			addr, err := net.ResolveUDPAddr("udp", h.dtls)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := piondtls.Dial("udp", addr, pskConfig("identity", key)...)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- conn.HandshakeContext(ctx) }()
			select {
			case <-backing.lookup:
			case <-ctx.Done():
				t.Fatal("PSK lookup did not start")
			}
			if remove {
				if _, ok := h.srv.Security().Remove("device"); !ok {
					t.Fatal("credential missing")
				}
			} else if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "device", PSKIdentity: "identity", PSKKey: []byte("fedcba9876543210")}); err != nil {
				t.Fatal(err)
			}
			close(backing.resume)
			if err := <-done; err == nil {
				t.Fatal("handshake succeeded after revocation")
			}
		})
	}
}
