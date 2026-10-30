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
	"strings"
	"testing"
	"time"
)

func TestSQLiteWebhookSettingsModelRestart(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	const token = "lwm_restart_synthetic"
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(fmt.Sprintf("key_restart sha256:%x admin,devices:read,devices:write", sha256.Sum256([]byte(token)))), 0600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	start := func() *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary, "-store", "sqlite:"+filepath.Join(dir, "devices.db"), "-http", addr, "-api-keys", keys, "-coap=", "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
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
				_ = res.Body.Close()
				if res.StatusCode == 200 {
					return cmd
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("daemon did not become ready")
		return nil
	}
	request := func(method, path, body, contentType string, want int) string {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", contentType)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != want {
			t.Errorf("%s %s: got %d want %d: %s", method, path, res.StatusCode, want, b)
		}
		return string(b)
	}
	cmd := start()
	created := request("POST", "/v1/webhooks", `{"url":"https://example.com/hook","types":["lwm2m.registration.*"]}`, "application/json", 201)
	var hook struct{ ID, Secret string }
	if err := json.Unmarshal([]byte(created), &hook); err != nil || hook.ID == "" || hook.Secret == "" {
		t.Fatalf("invalid created webhook: %v", err)
	}
	settings := request("PATCH", "/v1/settings", `{"events":{"retentionDays":30}}`, "application/merge-patch+json", 200)
	const model = `<LWM2M><Object ObjectType="MODefinition"><Name>Vendor</Name><Description1></Description1><ObjectID>50000</ObjectID><ObjectURN>urn:oma:lwm2m:x:50000</ObjectURN><MultipleInstances>Multiple</MultipleInstances><Mandatory>Optional</Mandatory><Resources><Item ID="0"><Name>Value</Name><Operations>R</Operations><MultipleInstances>Single</MultipleInstances><Mandatory>Optional</Mandatory><Type>String</Type><RangeEnumeration></RangeEnumeration><Units></Units><Description></Description></Item></Resources><Description2></Description2></Object></LWM2M>`
	request("POST", "/v1/models", model, "application/xml", 201)
	beforeModel := request("GET", "/v1/models/50000", "", "", 200)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	start()
	gotHook := request("GET", "/v1/webhooks/"+hook.ID, "", "", 200)
	if strings.Contains(gotHook, hook.Secret) {
		t.Error("GET exposed webhook secret")
	}
	if !strings.Contains(gotHook, hook.ID) {
		t.Error("webhook missing after restart")
	}
	if got := request("GET", "/v1/settings", "", "", 200); got != settings {
		t.Errorf("settings changed after restart: %s", got)
	}
	if got := request("GET", "/v1/models/50000", "", "", 200); got != beforeModel {
		t.Errorf("model changed after restart: %s", got)
	}
}
