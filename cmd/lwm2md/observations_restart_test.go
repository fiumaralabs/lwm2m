package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestSQLiteObservationsRestartAndNotification(t *testing.T) {
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
	request("POST", "/v1/devices", `{"endpoint":"restart-observer"}`, 201)
	var created store.Observation
	if err := json.Unmarshal(request("POST", "/v1/devices/restart-observer/observations", `{"paths":["/3/0/0"],"persistent":true}`, 201), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != "pending" {
		t.Fatalf("created: %+v", created)
	}
	path := "/v1/observations/" + created.ID
	first := testclient.New(testclient.Config{Endpoint: "restart-observer"})
	first.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("before-restart"))
	first.Set(lwm2m.MustParsePath("/3/0/1"), lwm2m.String("transient"))
	if err := first.Dial(coapAddr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if res, err := first.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("initial registration: %+v %v", res, err)
	}
	var recovered store.Observation
	for {
		if err := json.Unmarshal(request("GET", path, "", 200), &recovered); err != nil {
			t.Fatal(err)
		}
		if recovered.Status == "active" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("initial observation not active: %+v", recovered)
		case <-time.After(10 * time.Millisecond):
		}
	}
	var transient store.Observation
	if err := json.Unmarshal(request("POST", "/v1/devices/restart-observer/observations", `{"paths":["/3/0/1"],"persistent":false}`, 201), &transient); err != nil {
		t.Fatal(err)
	}
	transientPath := "/v1/observations/" + transient.ID
	for {
		if err := json.Unmarshal(request("GET", transientPath, "", 200), &transient); err != nil {
			t.Fatal(err)
		}
		if transient.Status == "active" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("transient observation not attempted: %+v", transient)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	first.Close()
	cmd = start()
	if err := json.Unmarshal(request("GET", transientPath, "", 200), &transient); err != nil {
		t.Fatal(err)
	}
	if transient.Status != "ended" {
		t.Fatalf("session-bound observation after restart: %+v", transient)
	}
	if err := json.Unmarshal(request("GET", path, "", 200), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.ID != created.ID || recovered.Device != created.Device || recovered.Status != "active" || len(recovered.Paths) != 1 || recovered.Paths[0] != "/3/0/0" {
		t.Fatalf("recovered: %+v", recovered)
	}
	c := testclient.New(testclient.Config{Endpoint: "restart-observer"})
	defer c.Close()
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("restart-value"))
	if err := c.Dial(coapAddr); err != nil {
		t.Fatal(err)
	}
	if res, err := c.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("register: %+v %v", res, err)
	}
	for {
		if err := json.Unmarshal(request("GET", path, "", 200), &recovered); err != nil {
			t.Fatal(err)
		}
		if recovered.Status == "active" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("observation not active: %+v", recovered)
		case <-time.After(10 * time.Millisecond):
		}
	}
	var req testclient.Request
	for {
		var ok bool
		req, ok = c.LastRequest()
		if ok && req.Observe != nil && *req.Observe == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("missing observe: %+v", req)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err := c.Notify(ctx, req.Token); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(request("GET", transientPath, "", 200), &transient); err != nil {
		t.Fatal(err)
	}
	if transient.Status != "ended" {
		t.Fatalf("session-bound observation after re-registration: %+v", transient)
	}
	for {
		var page struct {
			Items []store.Event `json:"items"`
		}
		if err := json.Unmarshal(request("GET", "/v1/devices/restart-observer/events?type=lwm2m.notification", "", 200), &page); err != nil {
			t.Fatal(err)
		}
		for _, event := range page.Items {
			if event.Type != "lwm2m.notification" {
				continue
			}
			var payload struct {
				Observation string `json:"observation"`
				Values      []struct {
					Path  string          `json:"path"`
					Value json.RawMessage `json:"value"`
				} `json:"values"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Observation == created.ID && len(payload.Values) == 1 && payload.Values[0].Path == "/3/0/0" && string(payload.Values[0].Value) == `"restart-value"` {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("notification not delivered: %+v", page.Items)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
