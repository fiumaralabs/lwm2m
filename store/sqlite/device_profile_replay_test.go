package sqlite_test

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDeviceProfileHTTPReplayAfterReopen(t *testing.T) {
	hash := sha256.Sum256([]byte("lwm_test_secret"))
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write\n", hash)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profiles.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := apiv1.New(apiv1.Options{Keys: keys, DeviceProfiles: db.DeviceProfiles()})
	request := func(h http.Handler, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/device-profiles", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		r.Header.Set("Idempotency-Key", "once")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := request(h, `{"name":"pump"}`)
	if first.Code != 201 {
		t.Fatalf("create: %d %s", first.Code, first.Body)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h = apiv1.New(apiv1.Options{Keys: keys, DeviceProfiles: db.DeviceProfiles()})
	got := request(h, `not JSON`)
	if got.Code != 201 || got.Body.String() != first.Body.String() || got.Header().Get("ETag") != first.Header().Get("ETag") || got.Header().Get("Location") != first.Header().Get("Location") || len(db.DeviceProfiles().List("default")) != 1 {
		t.Fatalf("replay: %d %s", got.Code, got.Body)
	}
}

func TestDeviceProfileConcurrentCreateReplay(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := db.DeviceProfiles()
	const n = 16
	var wg sync.WaitGroup
	results := make(chan apiv1.DeviceProfile, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := store.CreateIdempotent("a", apiv1.DeviceProfile{ID: fmt.Sprint(i), Name: "pump"}, "once")
			results <- p
			errs <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for p := range results {
		if id == "" {
			id = p.ID
		}
		if p.ID != id {
			t.Fatalf("different ids %q %q", id, p.ID)
		}
	}
	if len(store.List("a")) != 1 {
		t.Fatal("duplicate profiles")
	}
}

func TestDeviceProfileReplayAtomicRollbackAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Exec(`CREATE TRIGGER fail_profile_replay BEFORE INSERT ON device_profile_replays BEGIN SELECT RAISE(FAIL, 'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	profiles := db.DeviceProfiles()
	if _, err := profiles.CreateIdempotent("a", apiv1.DeviceProfile{ID: "first", Name: "pump"}, "key"); err == nil {
		t.Fatal("injected failure accepted")
	}
	if _, ok := profiles.Get("a", "first"); ok {
		t.Fatal("profile survived rollback")
	}
	if _, err = raw.Exec(`DROP TRIGGER fail_profile_replay`); err != nil {
		t.Fatal(err)
	}
	first, err := profiles.CreateIdempotent("a", apiv1.DeviceProfile{ID: "first", Name: "pump"}, "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := profiles.Replay("other", "key"); ok {
		t.Fatal("tenant leaked")
	}
	if ok, _ := profiles.Delete("a", first.ID); !ok {
		t.Fatal("delete failed")
	}
	if got, ok := profiles.Replay("a", "key"); !ok || got.ID != first.ID {
		t.Fatal("deleted profile lost replay")
	}
	if _, err = raw.Exec(`UPDATE device_profile_replays SET expires=0`); err != nil {
		t.Fatal(err)
	}
	second, err := profiles.CreateIdempotent("a", apiv1.DeviceProfile{ID: "second", Name: "new"}, "key")
	if err != nil || second.ID != "second" {
		t.Fatalf("expired replay: %+v %v", second, err)
	}
}
