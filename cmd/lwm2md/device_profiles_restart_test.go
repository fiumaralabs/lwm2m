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
	"syscall"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestSQLiteDeviceProfilesRestart(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	const token = "lwm_test_synthetic"
	keys := filepath.Join(dir, "keys")
	if err := os.WriteFile(keys, []byte(fmt.Sprintf("key_test sha256:%x admin,devices:read", sha256.Sum256([]byte(token)))), 0600); err != nil {
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
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		deadline := time.Now().Add(5 * time.Second)
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
		t.Fatal("daemon did not become ready")
		return nil
	}
	stop := func(cmd *exec.Cmd) {
		t.Helper()
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	}
	cmd := start()
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := apiv1.DeviceProfile{ID: "restart-profile", Name: "Restart profile", Observations: json.RawMessage(`[{"path":"/3/0/0"}]`)}
	if err := db.DeviceProfiles().Create("default", want); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	stop(cmd)
	start()
	req, err := http.NewRequest("GET", "http://"+addr+"/v1/device-profiles/"+want.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET profile: status %d: %s", res.StatusCode, body)
	}
	var got apiv1.DeviceProfile
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profile: got %+v, want %+v", got, want)
	}
}
