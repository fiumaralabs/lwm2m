package apiv1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

type pausedLifecycleStore struct {
	BootstrapConfigStore
	pause   string
	once    atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (s *pausedLifecycleStore) stop() {
	if s.once.CompareAndSwap(false, true) {
		close(s.entered)
		<-s.release
	}
}
func (s *pausedLifecycleStore) Get(t, e string) (json.RawMessage, bool, error) {
	b, ok, err := s.BootstrapConfigStore.Get(t, e)
	if s.pause == "get" {
		s.stop()
	}
	return b, ok, err
}
func (s *pausedLifecycleStore) Put(t, e string, b json.RawMessage) error {
	err := s.BootstrapConfigStore.Put(t, e, b)
	if s.pause == "put" {
		s.stop()
	}
	return err
}
func asyncLifecycle(h interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, method, path, body string) <-chan *httptest.ResponseRecorder {
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() { ch <- deviceRequest(h, method, path, body) }()
	return ch
}

// Wait until the competing request has joined the lifecycle lock queue. This
// proves it reached the interleaving point rather than merely being unscheduled.
func assertWaiting(t *testing.T, ch <-chan *httptest.ResponseRecorder) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		bootstrapLifecycle.mu.Lock()
		entry := bootstrapLifecycle.locks["default"]
		queued := entry != nil && entry.refs >= 2
		bootstrapLifecycle.mu.Unlock()
		if queued {
			return
		}
		select {
		case w := <-ch:
			t.Fatalf("competing mutation completed while first was paused: %d", w.Code)
		case <-deadline:
			t.Fatal("competing mutation did not reach lifecycle lock")
		default:
			runtime.Gosched()
		}
	}
}
func TestConcurrentDeleteDoesNotRestoreConfig(t *testing.T) {
	base := NewMemoryBootstrapConfigStore()
	s := &pausedLifecycleStore{BootstrapConfigStore: base, pause: "get", entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-s.release:
		default:
			close(s.release)
		}
	}()
	devices := &memory.Devices{}
	h := New(Options{Keys: lifecycleKeys(), BootstrapConfigs: s, Devices: devices})
	h2 := New(Options{Keys: lifecycleKeys(), BootstrapConfigs: s, Devices: devices})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if err := base.Put("default", "a", json.RawMessage(`{"servers":[{"shortServerId":1}]}`)); err != nil {
		t.Fatal(err)
	}
	a := asyncLifecycle(h, "DELETE", "/v1/devices/a", "")
	<-s.entered
	b := asyncLifecycle(h2, "DELETE", "/v1/devices/a", "")
	assertWaiting(t, b)
	close(s.release)
	if w := <-a; w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := <-b; w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", ""); w.Code != 404 {
		t.Fatalf("stale config after recreation: %d", w.Code)
	}
}
func TestBootstrapWriteWaitsForDeviceDeletion(t *testing.T) {
	base := NewMemoryBootstrapConfigStore()
	s := &pausedLifecycleStore{BootstrapConfigStore: base, pause: "get", entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-s.release:
		default:
			close(s.release)
		}
	}()
	devices := &memory.Devices{}
	h := New(Options{Keys: lifecycleKeys(), BootstrapConfigs: s, Devices: devices})
	h2 := New(Options{Keys: lifecycleKeys(), BootstrapConfigs: s, Devices: devices})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if err := base.Put("default", "a", json.RawMessage(`{"servers":[{"shortServerId":1}]}`)); err != nil {
		t.Fatal(err)
	}
	deletion := asyncLifecycle(h, "DELETE", "/v1/devices/a", "")
	<-s.entered
	update := asyncLifecycle(h2, "PUT", "/v1/devices/a/bootstrap", `{"servers":[{"shortServerId":2}]}`)
	assertWaiting(t, update)
	close(s.release)
	if w := <-deletion; w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := <-update; w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if w := deviceRequest(h, "GET", "/v1/devices/a/bootstrap", ""); w.Code != 404 {
		t.Fatalf("stale bootstrap after recreation: %d", w.Code)
	}
}
func TestConcurrentGeneratedBootstrapKeepsCredentialConsistent(t *testing.T) {
	srv := server.New(server.Config{})
	defer srv.Close()
	base := NewMemoryBootstrapConfigStore()
	s := &pausedLifecycleStore{BootstrapConfigStore: base, pause: "put", entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-s.release:
		default:
			close(s.release)
		}
	}()
	h := New(Options{Keys: lifecycleKeys(), Server: srv, BootstrapConfigs: s})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"a"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	body := `{"servers":[{"server":"self","shortServerId":1,"security":{"psk":{"generate":true}}}]}`
	a := asyncLifecycle(h, "PUT", "/v1/devices/a/bootstrap", body)
	<-s.entered
	b := asyncLifecycle(h, "PUT", "/v1/devices/a/bootstrap", body)
	assertWaiting(t, b)
	close(s.release)
	if w := <-a; w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := <-b; w.Code != 200 {
		t.Fatal(w.Code)
	}
	raw, ok, err := base.Get("default", "a")
	if err != nil || !ok {
		t.Fatal(err)
	}
	var config struct {
		Servers []struct {
			Security struct {
				PSK struct {
					Identity string `json:"identity"`
					Key      string `json:"key"`
				} `json:"psk"`
			} `json:"security"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	p := config.Servers[0].Security.PSK
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(p.Key, "base64:"))
	if err != nil {
		t.Fatal(err)
	}
	cred, ok := srv.Security().ByEndpoint("a")
	if !ok || cred.PSKIdentity != p.Identity || !bytes.Equal(cred.PSKKey, key) {
		t.Fatal("config and server credential differ")
	}
}
