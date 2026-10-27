package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWebhooksContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		storetest.Webhooks(t, func(*testing.T) store.Webhooks { return &memory.Webhooks{} })
	})
	t.Run("sqlite", func(t *testing.T) {
		storetest.Webhooks(t, func(t *testing.T) store.Webhooks {
			d, err := sqlite.Open(filepath.Join(t.TempDir(), "hooks.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := d.Close(); err != nil {
					t.Error(err)
				}
			})
			return d.Webhooks()
		})
	})
}
func TestWebhooksReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s := d.Webhooks()
	s.Now = func() time.Time { return now }
	created, err := s.Create("a", store.Webhook{ID: "hook", Secret: "old", Status: "enabled", Types: []string{"device.*"}, Selector: "x=y", URL: "https://example.test"}, "create")
	if err != nil {
		t.Fatal(err)
	}
	want, err := s.Rotate("a", "hook", "new", "rotate")
	if err != nil {
		t.Fatal(err)
	}
	disabled := "disabled"
	want, err = s.Patch("a", "hook", store.WebhookPatch{Status: &disabled}, store.WebhookMatch{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("a", "hook"); err == nil {
		t.Fatal("closed store succeeded")
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s = d.Webhooks()
	s.Now = func() time.Time { return now }
	got, err := s.Get("a", "hook")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	replay, ok := s.Replay("a", "", "create")
	if !ok || !reflect.DeepEqual(replay, created) {
		t.Fatalf("replay: %+v %v", replay, ok)
	}
	replay, ok = s.Replay("a", "hook", "rotate")
	if !ok || replay.Secret != "new" || replay.Status != "enabled" {
		t.Fatalf("rotation replay: %+v %v", replay, ok)
	}
	now = now.Add(24 * time.Hour)
	if _, ok := s.Replay("a", "", "create"); ok {
		t.Fatal("replay did not expire")
	}
	got, err = s.Rotate("a", "hook", "third", "")
	if err != nil || len(got.PreviousSecrets) != 1 || got.PreviousSecrets[0].Secret != "new" {
		t.Fatalf("overlap expiry: %+v %v", got, err)
	}
}
