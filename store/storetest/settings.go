package storetest

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
)

// Settings checks tenant isolation, merge semantics, ownership and atomic conditions.
func Settings(t *testing.T, factory func(*testing.T) store.Settings) {
	t.Helper()
	s := factory(t)
	get := func(tenant string) store.SettingsValue {
		t.Helper()
		v, err := s.Get(tenant)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	patch := func(p string) store.SettingsValue {
		t.Helper()
		v, err := s.Patch("a", []byte(p), store.WebhookMatch{})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if got := get("a"); !reflect.DeepEqual(got, store.DefaultSettings()) {
		t.Fatalf("defaults: %+v", got)
	}
	first := patch(`{"registration":{"autoCreateDevices":true,"allowNoSec":true,"defaultProfile":"p"},"queueMode":{"smsWakeUp":true},"operations":{"defaultTtlSeconds":9007199254740993},"events":{"retentionDays":13},"registrationProfiles":{"p":[{"id":65535,"version":"1.1"}]}}`)
	if first.Version != 2 || first.ETag() == store.DefaultSettings().ETag() {
		t.Fatalf("version: %+v", first)
	}
	if got := get("b"); !reflect.DeepEqual(got, store.DefaultSettings()) {
		t.Fatalf("tenant leak: %+v", got)
	}
	*first.Registration.DefaultProfile = "changed"
	*first.RegistrationProfiles["p"][0].Version = "changed"
	first.RegistrationProfiles["p"][0].ID = 0
	delete(first.RegistrationProfiles, "p")
	owned := get("a")
	if *owned.Registration.DefaultProfile != "p" || owned.RegistrationProfiles["p"][0].ID != 65535 || *owned.RegistrationProfiles["p"][0].Version != "1.1" {
		t.Fatalf("write aliases: %+v", owned)
	}
	*owned.RegistrationProfiles["p"][0].Version = "changed"
	*owned.Registration.DefaultProfile = "changed"
	if reflect.DeepEqual(owned, get("a")) {
		t.Fatal("read aliases")
	}
	merged := patch(`{"registration":{"allowNoSec":false}}`)
	if merged.Operations.DefaultTTLSeconds != 9007199254740993 || !merged.Registration.AutoCreateDevices || merged.Registration.AllowNoSec || merged.Events.RetentionDays != 13 {
		t.Fatalf("merge: %+v", merged)
	}
	for _, p := range []string{`null`, `[]`, `{`, `{} {}`, `{"unknown":1}`, `{"registration":null}`, `{"queueMode":{"smsWakeUp":null}}`, `{"events":{"retentionDays":0}}`, `{"operations":{"defaultTtlSeconds":1.5}}`, `{"registrationProfiles":{"p":[{"id":65536}]}}`, `{"registrationProfiles":{"p":[{"id":1,"version":""}]}}`} {
		if _, err := s.Patch("a", []byte(p), store.WebhookMatch{}); !errors.Is(err, store.ErrSettingsInvalid) {
			t.Fatalf("accepted %s: %v", p, err)
		}
		if !reflect.DeepEqual(merged, get("a")) {
			t.Fatalf("rejected patch changed state: %s", p)
		}
	}
	if _, err := s.Patch("a", []byte(`{}`), store.WebhookMatch{Required: true, Tags: []string{`"stale"`}}); !errors.Is(err, store.ErrSettingsPrecondition) || !reflect.DeepEqual(merged, get("a")) {
		t.Fatalf("stale condition: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Patch("a", []byte(`{}`), store.WebhookMatch{Required: true, Tags: []string{merged.ETag()}})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, store.ErrSettingsPrecondition) {
			t.Fatal(err)
		}
	}
	if successes != 1 || get("a").Version != merged.Version+1 {
		t.Fatalf("conditional winners: %d", successes)
	}
	cleared, err := s.Patch("a", []byte(`{"registration":{"defaultProfile":null},"registrationProfiles":{"p":null}}`), store.WebhookMatch{Required: true, Any: true})
	if err != nil || cleared.Registration.DefaultProfile != nil || len(cleared.RegistrationProfiles) != 0 {
		t.Fatalf("nullable merge: %+v %v", cleared, err)
	}
}
