package server_test

import (
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
)

type contextRegistrationStore struct {
	*server.MemoryStore
	contexts chan server.RegisterContext
}

func (s *contextRegistrationStore) AddChecked(*server.Registration) (*server.Registration, error) {
	return nil, errors.New("legacy checked method must not be used")
}

func (s *contextRegistrationStore) AddCheckedContext(c server.RegisterContext, r *server.Registration) (*server.Registration, error) {
	s.contexts <- c
	return s.MemoryStore.Add(r), nil
}

func TestRegisterContextAuthenticatedFingerprint(t *testing.T) {
	store := &contextRegistrationStore{MemoryStore: server.NewMemoryStore(), contexts: make(chan server.RegisterContext, 8)}
	h := newHarness(t, func(c *server.Config) { c.Store = store })
	key := []byte("0123456789abcdef")
	if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "ctx-device", PSKIdentity: "ctx-identity", PSKKey: key}); err != nil {
		t.Fatal(err)
	}
	c := h.device(testclient.Config{Endpoint: "ctx-device", PSKIdentity: "ctx-identity", PSKKey: key})
	register := func(query []string, links string) server.RegisterContext {
		t.Helper()
		r, err := c.RegisterRaw(h.ctx, query, []byte(links), true)
		mustCode(t, r, err, "2.01")
		return <-store.contexts
	}
	query := []string{"ep=ctx-device", "lt=600", "lwm2m=1.1", "b=U"}
	first := register(query, "</3/0>")
	second := register([]string{"b=U", "lwm2m=1.1", "lt=600", "ep=ctx-device"}, "</3/0>")
	if first.Endpoint != "ctx-device" || first.Identity.Mode != server.ModePSK || first.Identity.PSKIdentity != "ctx-identity" {
		t.Fatalf("unexpected authenticated context: %+v", first)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatal("same request content did not yield same fingerprint")
	}
	if first.Fingerprint == register([]string{"ep=ctx-device", "lt=601", "lwm2m=1.1", "b=U"}, "</3/0>").Fingerprint {
		t.Fatal("lifetime did not affect fingerprint")
	}
	if first.Fingerprint == register(query, "</3/1>").Fingerprint {
		t.Fatal("links did not affect fingerprint")
	}
}
