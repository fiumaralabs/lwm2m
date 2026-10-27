package sqlite_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestSettingsContract(t *testing.T) {
	storetest.Settings(t, func(t *testing.T) store.Settings {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "settings.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.Settings()
	})
}

func TestSettingsReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { d.Close() }()
	want, err := d.Settings().Patch("a", []byte(`{"registration":{"autoCreateDevices":true,"allowNoSec":true,"defaultProfile":"p"},"queueMode":{"smsWakeUp":true},"operations":{"defaultTtlSeconds":9007199254740993},"events":{"retentionDays":23},"registrationProfiles":{"p":[{"id":3,"version":"1.1"}]}}`), store.WebhookMatch{})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Settings().Get("a")
	if err != nil || !reflect.DeepEqual(got, want) || got.ETag() != want.ETag() {
		t.Fatalf("reopen: %+v want %+v: %v", got, want, err)
	}
	next, err := d.Settings().Patch("a", []byte(`{}`), store.WebhookMatch{Required: true, Tags: []string{want.ETag()}})
	if err != nil || next.Version != want.Version+1 {
		t.Fatalf("version continuity: %+v %v", next, err)
	}
}
