package memory

import (
	"github.com/fiumaralabs/lwm2m/store"

	"sync"
	"testing"
	"time"
)

func TestWebhooksOwnedCopies(t *testing.T) {
	s := &Webhooks{}
	input := store.Webhook{ID: "wh_1", URL: "original", Types: []string{"lwm2m.*"}, Secret: "current", PreviousSecrets: []store.WebhookSecret{{Secret: "expired", ExpiresAt: time.Now().Add(-time.Hour)}}}
	if _, e := s.Create("a", input, ""); e != nil {
		t.Fatal(e)
	}
	input.Types[0] = "changed"
	input.PreviousSecrets[0].Secret = "changed"
	if _, e := s.Create("a", store.Webhook{ID: "wh_1", URL: "overwrite"}, ""); e == nil {
		t.Fatal("duplicate create overwrote webhook")
	}
	items, _, e := s.List("a", "", 50)
	if e != nil || len(items) != 1 || items[0].URL != "original" || items[0].Types[0] != "lwm2m.*" {
		t.Fatal(items, e)
	}
	items[0].Types[0] = "changed"
	types := []string{"lwm2m.notification"}
	patched, e := s.Patch("a", "wh_1", store.WebhookPatch{Types: &types})
	if e != nil {
		t.Fatal(e)
	}
	types[0] = "changed"
	patched.Types[0] = "changed"
	rotated, e := s.Rotate("a", "wh_1", "new")
	if e != nil || len(rotated.PreviousSecrets) != 1 || rotated.PreviousSecrets[0].Secret != "current" || rotated.Types[0] != "lwm2m.notification" {
		t.Fatal(rotated, e)
	}
}

func TestWebhooksRotationRetention(t *testing.T) {
	s := &Webhooks{}
	v, e := s.Create("a", store.Webhook{ID: "wh_1", Types: []string{"lwm2m.*"}, Secret: "initial"}, "")
	if e != nil {
		t.Fatal(e)
	}
	v.Types[0] = "mutated"
	before := time.Now()
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			secret := string(rune('a' + i))
			if _, e := s.Rotate("a", "wh_1", secret); e != nil {
				t.Error(e)
			}
			url := secret
			if _, e := s.Patch("a", "wh_1", store.WebhookPatch{URL: &url}); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	v, e = s.Get("a", "wh_1")
	if e != nil || v.Types[0] != "lwm2m.*" {
		t.Fatal("unowned create/get", v, e)
	}
	previous := v.PreviousSecrets
	if len(previous) != 10 {
		t.Fatal("rapid rotations must retain all ten previous secrets")
	}
	for _, p := range previous {
		expiry := p.ExpiresAt
		if expiry.Before(before.Add(24*time.Hour)) || expiry.After(time.Now().Add(24*time.Hour)) {
			t.Fatal("invalid overlap", expiry)
		}
	}
	previous[0].Secret = "tampered"
	again, _ := s.Get("a", "wh_1")
	if again.PreviousSecrets[0].Secret == "tampered" {
		t.Fatal("unowned previous secrets")
	}
	if _, e = s.Rotate("b", "wh_1", "foreign"); e != store.ErrWebhookNotFound {
		t.Fatal(e)
	}
}
