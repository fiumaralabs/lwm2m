package memory

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// Boundary/ownership coverage for the store replay contract; no sleeps needed.
func TestWebhooksReplayExpiryAndIsolation(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &Webhooks{Now: func() time.Time { return now }}
	original := make(map[string]store.Webhook)
	rotated := make(map[string]store.Webhook)
	for _, tenant := range []string{"a", "b"} {
		v, err := s.Create(tenant, store.Webhook{ID: "wh_same", Secret: tenant, Types: []string{"lwm2m.*"}}, "same")
		if err != nil {
			t.Fatal(err)
		}
		original[tenant] = v
		v, err = s.Rotate(tenant, "wh_same", tenant+"-rotated", "same")
		if err != nil {
			t.Fatal(err)
		}
		rotated[tenant] = v
	}
	// Replay snapshots are owned copies, independent of live records and callers.
	v, _ := s.Create("a", store.Webhook{ID: "ignored"}, "same")
	v.Types[0] = "tampered"
	v, _ = s.Rotate("a", "wh_same", "ignored", "same")
	v.PreviousSecrets[0].Secret = "tampered"
	now = now.Add(24*time.Hour - time.Nanosecond)
	for _, tenant := range []string{"a", "b"} {
		v, err := s.Create(tenant, store.Webhook{ID: "wh_new"}, "same")
		if err != nil || !reflect.DeepEqual(v, original[tenant]) {
			t.Fatal("create replay changed before expiry", err)
		}
		v, err = s.Rotate(tenant, "wh_same", "ignored", "same")
		if err != nil || !reflect.DeepEqual(v, rotated[tenant]) {
			t.Fatal("rotation replay changed before expiry", err)
		}
	}
	// Reading a replay does not extend its original retention period.
	now = now.Add(time.Nanosecond)
	for _, tenant := range []string{"a", "b"} {
		v, err := s.Create(tenant, store.Webhook{ID: "wh_new"}, "same")
		if err != nil || v.ID != "wh_new" {
			t.Fatal("create did not expire at 24h", err)
		}
		v, err = s.Rotate(tenant, "wh_same", "after-expiry", "same")
		if err != nil || v.Secret != "after-expiry" || v.Version != rotated[tenant].Version+1 || len(v.PreviousSecrets) != 1 {
			t.Fatal("rotation did not expire at 24h", err)
		}
	}
}

func TestWebhooksRotateReplaySingleEffect(t *testing.T) {
	s := &Webhooks{}
	if _, err := s.Create("a", store.Webhook{ID: "wh_one", Secret: "original"}, ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			if _, err := s.Rotate("a", "wh_one", string(rune('a'+i)), "retry"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	v, err := s.Get("a", "wh_one")
	if err != nil || v.Version != 2 || len(v.PreviousSecrets) != 1 {
		t.Fatal("retry produced multiple rotation effects", err)
	}
	// The same key on a different resource must rotate that resource too.
	if _, err := s.Create("a", store.Webhook{ID: "wh_two", Secret: "second"}, ""); err != nil {
		t.Fatal(err)
	}
	v, err = s.Rotate("a", "wh_two", "next", "retry")
	if err != nil || v.ID != "wh_two" || v.Secret != "next" || len(v.PreviousSecrets) != 1 {
		t.Fatal("rotation endpoint scope collided", err)
	}
}
