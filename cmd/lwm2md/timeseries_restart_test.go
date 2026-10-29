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
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestSQLiteTimeSeriesRestartTenantIsolation(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	const token = "lwm_test_synthetic"
	authToken := token
	const otherToken = "lwm_other_synthetic"
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
		req.Header.Set("Authorization", "Bearer "+authToken)
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
	request("POST", "/v1/devices", `{"endpoint":"series-restart"}`, 201)
	db, err := sqlite.Open(filepath.Join(dir, "devices.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.APIKeys().Create(store.APIKey{KeyMetadata: store.KeyMetadata{ID: "other", Scopes: []string{"devices:read", "devices:write"}}, Hash: sha256.Sum256([]byte(otherToken)), Tenant: "other"}); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Truncate(time.Second)
	for tenant, value := range map[string]int{"default": 42, "other": 999} {
		if err := db.TimeSeries().Append(tenant, "series-restart", "/3/0/1", stamp, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	authToken = otherToken
	request("POST", "/v1/devices", `{"endpoint":"series-restart"}`, 201)
	get := func(want float64) []apiv1.TimeSeriesPoint {
		t.Helper()
		var result struct {
			Series []struct {
				Points []apiv1.TimeSeriesPoint `json:"points"`
			} `json:"series"`
		}
		if err := json.Unmarshal(request("GET", "/v1/devices/series-restart/timeseries?path=/3/0/1", "", 200), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Series) != 1 || len(result.Series[0].Points) != 1 || result.Series[0].Points[0].V != want || !result.Series[0].Points[0].T.Equal(stamp) {
			t.Fatalf("unexpected points (including tenant isolation): %+v", result.Series)
		}
		return result.Series[0].Points
	}
	authToken = token
	before := get(42)
	authToken = otherToken
	otherBefore := get(999)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	start()
	authToken = token
	after := get(42)
	authToken = otherToken
	otherAfter := get(999)
	if !reflect.DeepEqual(otherBefore, otherAfter) {
		t.Fatalf("other tenant lost points: before=%+v after=%+v", otherBefore, otherAfter)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("restart lost points: before=%+v after=%+v", before, after)
	}
}
