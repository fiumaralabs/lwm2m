package sqlite

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

func TestDeviceReplayExpiryAndWriteFailure(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	calls := 0
	run := func() (int, http.Header, []byte) {
		calls++
		return 201, http.Header{"Location": {"first"}}, []byte("body")
	}
	if _, _, _, err = db.ReplayDeviceCreate(context.Background(), "a", "k", run); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`UPDATE device_replays SET expires=0`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = db.ReplayDeviceCreate(context.Background(), "a", "k", run); err != nil || calls != 2 {
		t.Fatalf("expired: calls=%d err=%v", calls, err)
	}
	if _, err = db.db.Exec(`CREATE TRIGGER fail_device_replay BEFORE INSERT ON device_replays BEGIN SELECT RAISE(FAIL, 'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = db.ReplayDeviceCreate(context.Background(), "a", "failure", run); err == nil {
		t.Fatal("expected persistence error")
	}
	if _, err = db.db.Exec(`DROP TRIGGER fail_device_replay`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = db.ReplayDeviceCreate(context.Background(), "a", "failure", run); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceReplayCancelledWaiter(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, _, _, err := db.ReplayDeviceCreate(context.Background(), "a", "k", func() (int, http.Header, []byte) { close(started); <-release; return 201, nil, nil })
		done <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err = db.ReplayDeviceCreate(ctx, "a", "k", func() (int, http.Header, []byte) { t.Error("cancelled waiter ran"); return 0, nil, nil })
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if err != context.Canceled {
		t.Fatalf("waiter error: %v", err)
	}
}
