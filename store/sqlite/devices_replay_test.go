package sqlite

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
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

func TestDeviceReplayAfterCommittedCreateCancellation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	run := func() (int, http.Header, []byte) {
		calls++
		if _, err := db.Create("tenant", store.Device{Endpoint: "device"}); err != nil {
			t.Errorf("create: %v", err)
			return 409, nil, nil
		}
		cancel()
		return 201, http.Header{"Location": {"/v1/devices/device"}}, []byte("created")
	}
	status, _, _, err := db.ReplayDeviceCreate(ctx, "tenant", "key", run)
	if err != nil || status != 201 {
		t.Fatalf("first create: status=%d err=%v", status, err)
	}
	status, h, body, err := db.ReplayDeviceCreate(context.Background(), "tenant", "key", run)
	if err != nil || status != 201 || h.Get("Location") != "/v1/devices/device" || string(body) != "created" || calls != 1 {
		t.Fatalf("replay: status=%d header=%v body=%q calls=%d err=%v", status, h, body, calls, err)
	}
}

func TestDeviceReplayAcrossIndependentHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	started, release, entered := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	type result struct {
		status int
		body   string
		err    error
	}
	done := make(chan result, 2)
	go func() {
		status, _, body, err := first.ReplayDeviceCreate(context.Background(), "tenant", "key", func() (int, http.Header, []byte) {
			close(started)
			<-release
			if _, err := first.Create("tenant", store.Device{Endpoint: "device"}); err != nil {
				return 409, nil, []byte(err.Error())
			}
			return 201, http.Header{"Location": {"device"}}, []byte("created")
		})
		done <- result{status, string(body), err}
	}()
	<-started
	go func() {
		status, _, body, err := second.ReplayDeviceCreate(context.Background(), "tenant", "key", func() (int, http.Header, []byte) {
			entered <- struct{}{}
			if _, err := second.Create("tenant", store.Device{Endpoint: "device"}); err != nil {
				return 409, nil, []byte(err.Error())
			}
			return 201, nil, []byte("wrong callback")
		})
		done <- result{status, string(body), err}
	}()
	select {
	case <-entered:
		t.Error("second callback ran before first completed")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		r := <-done
		if r.err != nil || r.status != 201 || r.body != "created" {
			t.Errorf("result: %+v", r)
		}
	}
}
