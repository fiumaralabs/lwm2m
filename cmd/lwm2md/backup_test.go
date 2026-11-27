package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
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

	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestOnlineBackupRestore(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lwm2md")
	src := filepath.Join(dir, "source.db")
	backup := filepath.Join(dir, "backup.db")
	if b, e := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, b)
	}
	const token = "lwm_backup_synthetic"
	keys := filepath.Join(dir, "keys")
	if e := os.WriteFile(keys, []byte(fmt.Sprintf("key_backup sha256:%x admin,devices:read,devices:write,operations:write", sha256.Sum256([]byte(token)))), 0600); e != nil {
		t.Fatal(e)
	}
	tcp, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := tcp.Addr().String()
	tcp.Close()
	udp, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	coap := udp.LocalAddr().String()
	udp.Close()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	start := func(path string) *exec.Cmd {
		t.Helper()
		c := exec.Command(bin, "-store=sqlite:"+path, "-registration-store=sqlite", "-http="+addr, "-api-keys="+keys, "-coap="+coap, "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
		if e := c.Start(); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if c.ProcessState == nil {
				_ = c.Process.Kill()
				_ = c.Wait()
			}
		})
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			r, e := client.Get("http://" + addr + "/readyz")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return c
				}
			}
		}
		t.Fatal("daemon not ready")
		return nil
	}
	req := func(method, path, body string, want int) []byte {
		t.Helper()
		r, e := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(resp.Body)
		if e != nil || resp.StatusCode != want {
			t.Fatalf("%s %s: status %d: %s %v", method, path, resp.StatusCode, b, e)
		}
		return b
	}
	first := start(src)
	req("POST", "/v1/devices", `{"endpoint":"pump","credentials":{"psk":{"identity":"pump","key":"hex:000102030405060708090a0b0c0d0e0f"}}}`, 201)
	var op struct{ ID string }
	if e := json.Unmarshal(req("POST", "/v1/devices/pump/operations", `{"type":"read","paths":["/3/0/0"],"ttlSeconds":3600}`, 202), &op); e != nil || op.ID == "" {
		t.Fatalf("operation: %v %+v", e, op)
	}
	c := testclient.New(testclient.Config{Endpoint: "registered"})
	defer c.Close()
	c.AddObject(3)
	if e := c.Dial(coap); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if r, e := c.Register(ctx); e != nil || r.Code != codes.Created {
		t.Fatalf("register: %+v %v", r, e)
	}
	if b, e := exec.Command(bin, "backup", "-store", src, "-out", backup).CombinedOutput(); e != nil {
		t.Fatalf("backup: %v %s", e, b)
	}
	before, e := os.ReadFile(backup)
	if e != nil {
		t.Fatal(e)
	}
	if b, e := exec.Command(bin, "backup", "-store", src, "-out", backup).CombinedOutput(); e == nil {
		t.Fatalf("overwrote backup: %s", b)
	}
	after, e := os.ReadFile(backup)
	if e != nil || string(before) != string(after) {
		t.Fatal("existing backup changed", e)
	}
	if e := first.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = first.Wait()
	second := start(backup)
	req("GET", "/v1/operations/"+op.ID, "", 200)
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	if e := json.Unmarshal(req("GET", "/v1/devices/pump/events", "", 200), &page); e != nil || len(page.Items) == 0 {
		t.Fatalf("events: %v %s", e, page.Items)
	}
	if r, e := c.Update(ctx, nil, nil); e != nil || r.Code != codes.Changed {
		t.Fatalf("restored registration: %+v %v", r, e)
	}
	if e := second.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = second.Wait()
	db, e := sqlite.Open(backup)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	si, ok := db.Security().ByEndpoint("pump")
	if !ok || si.PSKIdentity != "pump" || len(si.PSKKey) != 16 {
		t.Fatal("missing restored security info")
	}
}

func TestBackupRejectsMissingSourceAndExistingEmptyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.db")
	dst := filepath.Join(dir, "out.db")
	if err := runBackup([]string{"-store", src, "-out", dst}); err == nil {
		t.Fatal("missing source accepted")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("source was created", err)
	}
	d, err := sqlite.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	if err := os.WriteFile(dst, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runBackup([]string{"-store", src, "-out", dst}); err == nil {
		t.Fatal("existing empty file accepted")
	}
	if st, err := os.Stat(dst); err != nil || st.Size() != 0 {
		t.Fatal("existing file modified", err)
	}
}

func TestDaemonRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future.db")
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=9999"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "-store=sqlite:"+path, "-http=", "-coap=", "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "unsupported SQLite schema version 9999") {
		t.Fatalf("schema check: %v %s", err, out)
	}
}
