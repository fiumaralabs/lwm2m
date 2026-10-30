package coap

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
)

func TestDispatchedRegistrationRevoked(t *testing.T) {
	for _, operation := range []string{"register", "update"} {
		for _, mutation := range []string{"put", "remove"} {
			t.Run(operation+"/"+mutation, func(t *testing.T) {
				entered, resume := make(chan struct{}), make(chan struct{})
				var armed atomic.Bool
				h := newHarness(t, func(cfg *server.Config) {
					cfg.Now = func() time.Time {
						if armed.CompareAndSwap(true, false) {
							close(entered)
							<-resume
						}
						return time.Unix(1700000000, 0)
					}
				})
				completed := make(chan struct{}, 4)
				h.b.afterHook = func() { completed <- struct{}{} }
				key := []byte("0123456789abcdef")
				if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "device", PSKIdentity: "identity", PSKKey: key}); err != nil {
					t.Fatal(err)
				}
				c, err := h.secureDevice(testclient.Config{Endpoint: "device"}, h.dtls, pskConfig("identity", key))
				if err != nil {
					t.Fatal(err)
				}
				if operation == "update" {
					if _, err := c.Register(h.ctx); err != nil {
						t.Fatal(err)
					}
					h.ev.wait(t, func(e server.Event) bool { _, ok := e.(server.Registered); return ok })
					select {
					case <-completed:
					case <-time.After(5 * time.Second):
						t.Fatal("initial request did not complete")
					}
				}
				armed.Store(true)
				go func() {
					if operation == "register" {
						_, _ = c.Register(h.ctx)
					} else {
						_, _ = c.Update(h.ctx, nil, nil)
					}
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("request did not reach commit")
				}
				if mutation == "put" {
					if err := h.srv.Security().Put(server.SecurityInfo{Endpoint: "device", PSKIdentity: "identity", PSKKey: []byte("fedcba9876543210")}); err != nil {
						t.Fatal(err)
					}
				}
				if _, ok := h.srv.RevokeSecurity("device"); !ok {
					t.Fatal("missing credential")
				}
				close(resume)
				select {
				case <-completed:
				case <-time.After(5 * time.Second):
					t.Fatal("dispatched request did not complete")
				}
				if _, ok := h.srv.Store().ByEndpoint("device"); ok {
					t.Fatal("stale registration committed after revocation")
				}
				for {
					select {
					case ev := <-h.ev.ch:
						switch ev.(type) {
						case server.Registered, server.Updated:
							t.Fatal("stale registration emitted event after revocation")
						}
					default:
						return
					}
				}
			})
		}
	}
}

// Event handlers must schedule revocation rather than re-enter the guard.
func TestEventRevocationFromGoroutine(t *testing.T) {
	done := make(chan bool, 1)
	var srv *server.Server
	h := newHarness(t, func(cfg *server.Config) {
		previous := cfg.OnEvent
		cfg.OnEvent = func(e server.Event) {
			previous(e)
			if _, ok := e.(server.Registered); ok {
				go func() { _, found := srv.RevokeSecurity("device"); done <- found }()
			}
		}
	})
	srv = h.srv
	key := []byte("0123456789abcdef")
	if err := srv.Security().Put(server.SecurityInfo{Endpoint: "device", PSKIdentity: "identity", PSKKey: key}); err != nil {
		t.Fatal(err)
	}
	c, err := h.secureDevice(testclient.Config{Endpoint: "device"}, h.dtls, pskConfig("identity", key))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Register(h.ctx)
	select {
	case found := <-done:
		if !found {
			t.Fatal("credential not removed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("asynchronous revocation deadlocked")
	}
	if _, ok := srv.Store().ByEndpoint("device"); ok {
		t.Fatal("registration survived revocation")
	}
}
