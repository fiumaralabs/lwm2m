package sqlite_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestTrustAnchorContract(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var s apiv1.TrustAnchorStore
			if backend == "memory" {
				s = apiv1.NewMemoryTrustAnchorStore(2)
			} else {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				s = db.TrustAnchors(2)
			}
			a := apiv1.TrustAnchor{ID: "a", Name: "name", PEM: "secret", Certificates: []apiv1.TrustAnchorCertificate{{Fingerprint: "fp"}}}
			if err := s.Create("one", a); err != nil {
				t.Fatal(err)
			}
			if got, ok := s.Get("one", "a"); !ok || !reflect.DeepEqual(got, a) {
				t.Fatalf("get: %+v %v", got, ok)
			}
			if _, ok := s.Get("two", "a"); ok {
				t.Fatal("tenant leak")
			}
			duplicate := a
			duplicate.ID = "b"
			if err := s.Create("one", duplicate); !errors.Is(err, apiv1.ErrTrustAnchorDuplicate) {
				t.Fatalf("duplicate: %v", err)
			}
			if err := s.Create("two", duplicate); err != nil {
				t.Fatal(err)
			}
			if err := s.Create("three", apiv1.TrustAnchor{ID: "c"}); !errors.Is(err, apiv1.ErrTrustAnchorLimit) {
				t.Fatalf("limit: %v", err)
			}
			if found, matched := s.DeleteMatch("one", "a", store.WebhookMatch{Required: true, Tags: []string{"bad"}}); !found || matched {
				t.Fatal(found, matched)
			}
			if !s.Delete("one", "a") || s.Delete("one", "a") {
				t.Fatal("delete")
			}
			if len(s.List("one")) != 0 || len(s.List("two")) != 1 {
				t.Fatal("list isolation")
			}
			result := s.CreateResult("one", "key", apiv1.TrustAnchorResult{Anchor: &a})
			if result.Anchor == nil || result.Anchor.PEM != "secret" {
				t.Fatalf("create result: %+v", result)
			}
			result.Anchor.Certificates[0].Fingerprint = "mutated"
			if a.Certificates[0].Fingerprint != "fp" {
				t.Fatal("result aliases caller input")
			}
			replay, ok := s.Replay("one", "key")
			if !ok || replay.Anchor == nil || replay.Anchor.PEM != "" {
				t.Fatalf("replay: %+v %v", replay, ok)
			}
			if _, ok := s.Replay("two", "key"); ok {
				t.Fatal("replay tenant leak")
			}
			if got := s.CreateResult("one", "key", apiv1.TrustAnchorResult{}); got.Anchor == nil || got.Anchor.ID != "a" || got.Anchor.PEM != "" {
				t.Fatalf("retry: %+v", got)
			}
			duplicateResult := s.CreateResult("one", "duplicate", apiv1.TrustAnchorResult{Anchor: &duplicate})
			if duplicateResult.Problem == nil || duplicateResult.Problem.Code != "duplicate_trust_anchor" || duplicateResult.Problem.Status != 409 {
				t.Fatalf("duplicate result: %+v", duplicateResult)
			}
			if replay, ok := s.Replay("one", "duplicate"); !ok || !reflect.DeepEqual(replay, duplicateResult) {
				t.Fatalf("duplicate replay: %+v %v", replay, ok)
			}
			limited := apiv1.TrustAnchor{ID: "other"}
			if got := s.CreateResult("other", "limit", apiv1.TrustAnchorResult{Anchor: &limited}); got.Problem == nil || got.Problem.Status != 503 {
				t.Fatalf("limit result: %+v", got)
			}
		})
	}
}

func TestTrustAnchorConcurrentConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	stores := []apiv1.TrustAnchorStore{a.TrustAnchors(10), b.TrustAnchors(10)}
	for n := 0; n < 20; n++ {
		anchor := apiv1.TrustAnchor{ID: fmt.Sprint(n), Certificates: []apiv1.TrustAnchorCertificate{{Fingerprint: fmt.Sprint(n)}}}
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range stores {
			wg.Add(1)
			go func(i int) { defer wg.Done(); <-start; errs[i] = stores[i].Create("tenant", anchor) }(i)
		}
		close(start)
		wg.Wait()
		if !((errs[0] == nil && errors.Is(errs[1], apiv1.ErrTrustAnchorDuplicate)) || (errs[1] == nil && errors.Is(errs[0], apiv1.ErrTrustAnchorDuplicate))) {
			t.Fatalf("concurrent create: %v", errs)
		}
		if !stores[0].Delete("tenant", anchor.ID) {
			t.Fatal("delete")
		}
	}
}

func TestTrustAnchorReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchors.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := apiv1.TrustAnchor{ID: "a", PEM: "pem", Certificates: []apiv1.TrustAnchorCertificate{{Fingerprint: "fp"}}}
	if err = db.TrustAnchors(2).Create("tenant", a); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.TrustAnchors(2)
	got, ok := s.Get("tenant", "a")
	if !ok || !reflect.DeepEqual(got, a) || !reflect.DeepEqual(s.List("tenant"), []apiv1.TrustAnchor{a}) {
		t.Fatalf("reopen: %+v %v", got, ok)
	}
}
