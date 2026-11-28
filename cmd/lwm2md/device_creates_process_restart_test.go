package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDeviceCreateOutcomeSIGKILLRestart(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	const token = "lwm_test_synthetic"
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(fmt.Sprintf("key_test sha256:%x devices:read,devices:write,credentials:write", sha256.Sum256([]byte(token)))), 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	coap := udp.LocalAddr().String()
	udp.Close()
	path := filepath.Join(dir, "devices.db")
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	start := func() *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary, "-store", "sqlite:"+path, "-http", addr, "-api-keys", keys, "-coap="+coap, "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
		log, err := os.CreateTemp(dir, "daemon-*.log")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = log.Close() })
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			res, err := client.Get("http://" + addr + "/readyz")
			if err == nil {
				res.Body.Close()
				if res.StatusCode == http.StatusOK {
					return cmd
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		b, _ := os.ReadFile(log.Name())
		t.Fatalf("daemon not ready: %s", b)
		return nil
	}
	request := func(method, route, body string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+addr+route, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want {
			t.Fatalf("%s %s: status=%d body=%s error=%v", method, route, res.StatusCode, b, err)
		}
		return b
	}
	const route = "/v1/device-creates/kill-restart"
	const body = `{"endpoint":"pump","credentials":{"psk":{"identity":"pump-id","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`
	cmd := start()
	first := request("PUT", route, body, http.StatusCreated)
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("daemon did not exit from SIGKILL: %v", cmd.ProcessState)
	}
	start()
	got := request("GET", route, "", http.StatusOK)
	var outcome struct {
		OperationID string `json:"operationId"`
		Status      string `json:"status"`
		Endpoint    string `json:"endpoint"`
	}
	if err := json.Unmarshal(got, &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.OperationID != "kill-restart" || outcome.Status != "committed" || outcome.Endpoint != "pump" {
		t.Fatalf("outcome: %+v", outcome)
	}
	if !bytes.Equal(first, got) {
		t.Fatalf("outcome changed after restart: %s vs %s", first, got)
	}
	request("GET", "/v1/devices/pump", "", http.StatusOK)
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshot := func() ([]store.Device, int, int) {
		t.Helper()
		devices, _, err := db.List("default", store.DeviceFilter{}, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		sec, ok := db.Security().ByEndpoint("pump")
		if !ok || sec.PSKIdentity != "pump-id" || !bytes.Equal(sec.PSKKey, []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}) {
			t.Fatal("persisted security information mismatch")
		}
		audit, err := db.Audit().List("default")
		if err != nil {
			t.Fatal(err)
		}
		events, _, err := db.Events().List("default", store.EventFilter{Type: "lwm2m.device.created", Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		return devices, len(audit), len(events)
	}
	devices, audits, events := snapshot()
	if len(devices) != 1 || devices[0].Endpoint != "pump" || audits != 1 || events != 1 {
		t.Fatalf("before replay: devices=%d audit=%d events=%d", len(devices), audits, events)
	}
	replay := request("PUT", route, body, http.StatusOK)
	if !bytes.Equal(first, replay) {
		t.Fatalf("replay changed outcome: %s", replay)
	}
	after, auditsAfter, eventsAfter := snapshot()
	if !reflect.DeepEqual(devices, after) || audits != auditsAfter || events != eventsAfter {
		t.Fatalf("replay changed state: devices=%d audit=%d events=%d", len(after), auditsAfter, eventsAfter)
	}
}
