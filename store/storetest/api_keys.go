package storetest

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// APIKeys checks both implementations against the same persistence contract.
func APIKeys(t *testing.T, factory func(*testing.T) store.APIKeys) {
	t.Helper()
	s := factory(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
	expires := now.Add(time.Hour)
	want := store.APIKey{KeyMetadata: store.KeyMetadata{ID: "z", Name: "automation", Scopes: []string{"read", "write"}, CreatedAt: now, ExpiresAt: &expires}, Tenant: "a", Hash: [32]byte{1, 2, 3}}
	if err := s.Create(want); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("z"); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("get: %+v %v", got, err)
	}
	duplicate := want
	duplicate.Tenant = "b"
	if err := s.Create(duplicate); !errors.Is(err, store.ErrAPIKeyExists) {
		t.Fatalf("global duplicate: %v", err)
	}
	if err := s.Delete("b", "z"); !errors.Is(err, store.ErrAPIKeyNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if items, err := s.List("b"); err != nil || len(items) != 0 {
		t.Fatalf("tenant leak: %+v %v", items, err)
	}
	want.Scopes[0] = "tampered"
	*want.ExpiresAt = now
	got, err := s.Get("z")
	if err != nil || got.Scopes[0] != "read" || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("input ownership: %+v %v", got, err)
	}
	got.Scopes[0] = "tampered"
	*got.ExpiresAt = now
	if err := s.Create(store.APIKey{KeyMetadata: store.KeyMetadata{ID: "a"}, Tenant: "a"}); err != nil {
		t.Fatal(err)
	}
	items, err := s.List("a")
	if err != nil || len(items) != 2 || items[0].ID != "a" || items[1].ID != "z" || items[1].Scopes[0] != "read" || !items[1].ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("sorted owned list: %+v %v", items, err)
	}
	items[1].Scopes[0] = "tampered"
	*items[1].ExpiresAt = now
	at := now.Add(time.Minute)
	if err := s.Touch("z", at); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get("z")
	want.Scopes[0] = "read"
	expires = now.Add(time.Hour)
	want.LastUsedAt = &at
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("touch isolation: %+v %v", got, err)
	}
	*got.LastUsedAt = now
	items, err = s.List("a")
	if err != nil || !items[1].LastUsedAt.Equal(at) {
		t.Fatalf("last-use ownership: %+v %v", items, err)
	}
	*items[1].LastUsedAt = now
	got, err = s.Get("z")
	if err != nil || !got.LastUsedAt.Equal(at) {
		t.Fatalf("list last-use ownership: %+v %v", got, err)
	}
	if err := s.Delete("a", "z"); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch("z", at); !errors.Is(err, store.ErrAPIKeyNotFound) {
		t.Fatalf("touch revoked: %v", err)
	}
	if _, err := s.Get("z"); !errors.Is(err, store.ErrAPIKeyNotFound) {
		t.Fatalf("missing get: %v", err)
	}
	if err := s.Delete("a", "z"); !errors.Is(err, store.ErrAPIKeyNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
}
