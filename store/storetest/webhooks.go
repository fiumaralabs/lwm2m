package storetest

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
)

// Webhooks exercises the tenant-isolated webhook contract.
func Webhooks(t *testing.T, factory func(*testing.T) store.Webhooks) {
	t.Helper()
	s := factory(t)
	v, err := s.Create("a", store.Webhook{ID: "a", URL: "https://example.test", Types: []string{"device.*"}, Status: "enabled", Secret: "first"}, "create")
	if err != nil || v.Version != 1 || v.CreatedAt.IsZero() {
		t.Fatalf("create: %+v %v", v, err)
	}
	if _, err := s.Create("a", v, ""); !errors.Is(err, store.ErrWebhookExists) {
		t.Fatal(err)
	}
	if _, err := s.Get("b", "a"); !errors.Is(err, store.ErrWebhookNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Patch("b", "a", store.WebhookPatch{}, store.WebhookMatch{Required: true}); !errors.Is(err, store.ErrWebhookNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Rotate("b", "a", "x", ""); !errors.Is(err, store.ErrWebhookNotFound) {
		t.Fatal(err)
	}
	if err := s.Delete("b", "a", store.WebhookMatch{Required: true}); !errors.Is(err, store.ErrWebhookNotFound) {
		t.Fatal(err)
	}
	v.Types[0] = "mutated"
	got, err := s.Get("a", "a")
	if err != nil || got.Types[0] != "device.*" {
		t.Fatalf("owned create: %+v %v", got, err)
	}
	original := got
	disabled, empty, url := "disabled", "", "https://new.test"
	types := []string{"registration.*"}
	got, err = s.Patch("a", "a", store.WebhookPatch{Status: &disabled, Selector: &empty, URL: &url, Types: &types}, store.WebhookMatch{Required: true, Tags: []string{got.ETag()}})
	if err != nil || got.Status != disabled || got.URL != url || got.Version != 2 {
		t.Fatalf("patch: %+v %v", got, err)
	}
	types[0] = "mutated"
	rotated, err := s.Rotate("a", "a", "second", "rotate")
	if err != nil || rotated.Secret != "second" || len(rotated.PreviousSecrets) != 1 || rotated.PreviousSecrets[0].Secret != "first" || rotated.PreviousSecrets[0].ExpiresAt.IsZero() {
		t.Fatalf("rotate: %+v %v", rotated, err)
	}
	rotated.PreviousSecrets[0].Secret = "mutated"
	replay, ok := s.Replay("a", "", "create")
	if !ok || !reflect.DeepEqual(replay, original) {
		t.Fatalf("create replay: %+v %v", replay, ok)
	}
	replay, err = s.Create("a", store.Webhook{ID: "ignored"}, "create")
	if err != nil || !reflect.DeepEqual(replay, original) {
		t.Fatalf("create retry: %+v %v", replay, err)
	}
	replay, err = s.Rotate("a", "a", "ignored", "rotate")
	if err != nil || replay.Secret != "second" || replay.PreviousSecrets[0].Secret != "first" {
		t.Fatalf("rotate retry: %+v %v", replay, err)
	}
	if _, ok := s.Replay("b", "a", "rotate"); ok {
		t.Fatal("cross-tenant replay")
	}
	if _, err := s.Patch("a", "a", store.WebhookPatch{}, store.WebhookMatch{Required: true, Tags: []string{original.ETag()}}); !errors.Is(err, store.ErrWebhookPrecondition) {
		t.Fatal(err)
	}
	if err := s.Delete("a", "a", store.WebhookMatch{Required: true, Tags: []string{original.ETag()}}); !errors.Is(err, store.ErrWebhookPrecondition) {
		t.Fatal(err)
	}
	if _, err := s.Create("a", store.Webhook{ID: "b"}, ""); err != nil {
		t.Fatal(err)
	}
	page, cursor, err := s.List("a", "", 1)
	if err != nil || len(page) != 1 || page[0].ID != "a" || cursor == "" {
		t.Fatalf("page: %+v %s %v", page, cursor, err)
	}
	page[0].Types[0] = "mutated"
	page, next, err := s.List("a", cursor, 1)
	if err != nil || len(page) != 1 || page[0].ID != "b" || next != "" {
		t.Fatalf("next: %+v %s %v", page, next, err)
	}
	for _, c := range []string{cursor, "garbage"} {
		if _, _, err := s.List("b", c, 1); !errors.Is(err, store.ErrWebhookCursor) {
			t.Fatal(err)
		}
	}
	for _, n := range []int{0, 501} {
		if _, _, err := s.List("a", "", n); !errors.Is(err, store.ErrWebhookCursor) {
			t.Fatal(err)
		}
	}
	got, _ = s.Get("a", "a")
	if got.Types[0] != "registration.*" || got.PreviousSecrets[0].Secret != "first" {
		t.Fatal("aliased snapshot")
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.Patch("a", "a", store.WebhookPatch{}, store.WebhookMatch{Required: true, Tags: []string{got.ETag()}}); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, store.ErrWebhookPrecondition) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("conditional writes: %d", successes.Load())
	}
	if err := s.Delete("a", "a", store.WebhookMatch{Any: true, Required: true}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Replay("a", "a", "rotate"); !ok {
		t.Fatal("delete lost replay")
	}
	if _, err := s.Get("a", "a"); !errors.Is(err, store.ErrWebhookNotFound) {
		t.Fatal(err)
	}
}
