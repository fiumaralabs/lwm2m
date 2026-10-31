package sqlite_test

import (
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"path/filepath"
	"testing"
)

func TestBootstrapSessionsRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err = d.BootstrapSessions().Append("tenant", "device", apiv1.BootstrapSession{ID: id, Kind: "request", Status: "succeeded"}); err != nil {
			t.Fatal(err)
		}
	}
	_, cursor, err := d.BootstrapSessions().ListForDevice("tenant", "device", "", 1)
	if err != nil || cursor == "" {
		t.Fatal(cursor, err)
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, next, err := d.BootstrapSessions().ListForDevice("tenant", "device", cursor, 1)
	if err != nil || next != "" || len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("restart: %+v %q %v", got, next, err)
	}
	if err = d.BootstrapSessions().Append("tenant", "device", apiv1.BootstrapSession{ID: "three", Kind: "pack", Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	got, _, err = d.BootstrapSessions().ListForDevice("tenant", "device", "", 3)
	if err != nil || len(got) != 3 || got[0].ID != "three" {
		t.Fatalf("after append: %+v %v", got, err)
	}
}
