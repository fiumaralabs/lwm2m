package main

import (
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
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

func TestSQLiteAuditRestart(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	const token = "lwm_test_synthetic"
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(fmt.Sprintf("key_test sha256:%x admin,devices:read,devices:write,operations:write", sha256.Sum256([]byte(token)))), 0600); err != nil {
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
	coapAddr := udp.LocalAddr().String()
	udp.Close()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	start := func() *exec.Cmd {
		cmd := exec.Command(binary, "-store", "sqlite:"+filepath.Join(dir, "devices.db"), "-http", addr, "-api-keys", keys, "-coap="+coapAddr, "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
		logFile, err := os.CreateTemp(dir, "daemon-*.log")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { logFile.Close() })
		cmd.Stdout, cmd.Stderr = logFile, logFile
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		})
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			res, err := client.Get("http://" + addr + "/readyz")
			if err == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					return cmd
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		b, _ := os.ReadFile(logFile.Name())
		t.Fatalf("daemon not ready: %s", b)
		return nil
	}
	request := func(method, path, body string, want int) []byte {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
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
			t.Fatalf("%s: status %d body %s: %v", path, res.StatusCode, b, err)
		}
		return b
	}
	cmd := start()
	request("POST", "/v1/devices", `{"endpoint":"audit-restart"}`, 201)
	request("DELETE", "/v1/devices/audit-restart", "", 204)
	get := func() []apiv1.AuditEntry {
		t.Helper()
		var p struct {
			Items []apiv1.AuditEntry `json:"items"`
		}
		if err := json.Unmarshal(request("GET", "/v1/audit-log", "", 200), &p); err != nil {
			t.Fatal(err)
		}
		return p.Items
	}
	before := get()
	if len(before) != 2 || before[0].Action != "device.delete" || before[1].Action != "device.create" {
		t.Fatalf("before restart: %+v", before)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	start()
	after := get()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("restart lost entries: before=%+v after=%+v", before, after)
	}
	request("POST", "/v1/devices", `{"endpoint":"after-restart"}`, 201)
	after = get()
	if len(after) != 3 || after[0].Action != "device.create" || !reflect.DeepEqual(before, after[1:]) {
		t.Fatalf("append after restart: %+v", after)
	}
}
