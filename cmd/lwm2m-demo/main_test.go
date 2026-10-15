package main

import (
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
)

func TestClampLifetime(t *testing.T) {
	s := &clampStore{Store: server.NewMemoryStore(), max: time.Hour}
	for _, lt := range []time.Duration{0, 86400 * time.Second} {
		r := &server.Registration{ID: "a", Endpoint: "a", Lifetime: lt}
		s.Add(r)
		if got, _ := s.ByID("a"); got.Lifetime != time.Hour {
			t.Errorf("lt %v stored as %v", lt, got.Lifetime)
		}
	}
	r := &server.Registration{ID: "a", Endpoint: "a", Lifetime: 30 * time.Second}
	s.Update(r)
	if got, _ := s.ByID("a"); got.Lifetime != 30*time.Second {
		t.Errorf("short lifetime changed to %v", got.Lifetime)
	}
}

func TestValidEndpoint(t *testing.T) {
	for ep, want := range map[string]bool{
		"urn:imei:123": true, "my-zephyr_1.0": true, "": false, "<script>": false,
		"a b": true, "ü": false, "x\n": false, string(make([]byte, 65)): false,
	} {
		if validEndpoint(ep) != want {
			t.Errorf("validEndpoint(%q) = %v", ep, !want)
		}
	}
}

func TestLimiter(t *testing.T) {
	store := server.NewMemoryStore()
	l := &regLimiter{max: 3, perIP: 2, rate: 100, store: store, hits: map[string][]time.Time{}}
	nosec := func(addr string) server.Identity { return server.Identity{Mode: server.ModeNoSec, Addr: addr} }
	add := func(ep, addr string) {
		store.Add(&server.Registration{ID: ep, Endpoint: ep, Identity: nosec(addr)})
	}
	add("a", "1.2.3.4:1000")
	add("b", "1.2.3.4:1001")
	if l.authorize("c", nosec("1.2.3.4:1002")) {
		t.Error("per-IP cap not applied")
	}
	if !l.authorize("a", nosec("1.2.3.4:1003")) {
		t.Error("re-register of an existing endpoint refused")
	}
	add("c", "5.6.7.8:1")
	if l.authorize("d", nosec("9.9.9.9:1")) {
		t.Error("global cap not applied")
	}
	if !l.authorize("c", nosec("5.6.7.8:2")) {
		t.Error("re-register at cap refused")
	}
	l2 := &regLimiter{max: 100, perIP: 100, rate: 2, store: server.NewMemoryStore(), hits: map[string][]time.Time{}}
	l2.authorize("x", nosec("1.1.1.1:1"))
	l2.authorize("x", nosec("1.1.1.1:1"))
	if l2.authorize("x", nosec("1.1.1.1:1")) {
		t.Error("rate limit not applied")
	}
}
