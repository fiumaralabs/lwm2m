package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v4"
)

func TestSQLiteSecurityRestart(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpAddr := tcp.Addr().String()
	_ = tcp.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dtlsAddr := udp.LocalAddr().String()
	_ = udp.Close()
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	start := func() *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary, "-store", "sqlite:"+filepath.Join(dir, "devices.db"), "-http", httpAddr, "-coap=", "-coaps="+dtlsAddr, "-bs-coap=", "-bs-coaps=", "-bs-http=")
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
			res, err := client.Get("http://" + httpAddr + "/readyz")
			if err == nil {
				_ = res.Body.Close()
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
			t.Fatalf("daemon shutdown: %v", err)
		}
	}
	request := func(method, body string) []byte {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+httpAddr+"/api/security/clients", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("%s security: status %s, body %s, error %v", method, res.Status, b, err)
		}
		return b
	}
	cmd := start()
	request(http.MethodPut, `{"endpoint":"persisted","tls":{"mode":"psk","details":{"identity":"persisted-id","key":"30313233343536373839616263646566"}}}`)
	stop(cmd)
	cmd = start()
	defer stop(cmd)
	var entries []struct {
		Endpoint string `json:"endpoint"`
		TLS      struct {
			Mode    string `json:"mode"`
			Details struct {
				Identity string `json:"identity"`
				Key      string `json:"key"`
			} `json:"details"`
		} `json:"tls"`
	}
	if err := json.Unmarshal(request(http.MethodGet, ""), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Endpoint != "persisted" || entries[0].TLS.Mode != "psk" || entries[0].TLS.Details.Identity != "persisted-id" || entries[0].TLS.Details.Key != "30313233343536373839616263646566" {
		t.Fatalf("credential not restored: %+v", entries)
	}
	device := testclient.New(testclient.Config{Endpoint: "persisted"})
	defer device.Close()
	opts := append(testclient.PSKConfig("persisted-id", []byte("0123456789abcdef")), piondtls.WithFlightInterval(50*time.Millisecond))
	if err := device.DialDTLS(dtlsAddr, opts...); err != nil {
		t.Fatalf("DTLS dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := device.Register(ctx)
	if err != nil || response.Code.String() != "Created" {
		t.Fatalf("DTLS registration: response %v, error %v", response, err)
	}
}
