package main

import (
	"context"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestDaemonSQLiteRegistrationStoreRestart(t *testing.T) {
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
	tcp.Close()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	coapAddr := udp.LocalAddr().String()
	udp.Close()
	httpClient := &http.Client{Timeout: time.Second}
	defer httpClient.CloseIdleConnections()
	start := func() *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary, "-store=sqlite:"+filepath.Join(dir, "db"), "-registration-store=sqlite", "-http="+httpAddr, "-coap="+coapAddr, "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			res, err := httpClient.Get("http://" + httpAddr + "/readyz")
			if err == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					return cmd
				}
			}
		}
		t.Fatal("daemon did not become ready")
		return nil
	}
	first := start()
	c := testclient.New(testclient.Config{Endpoint: "restart-registration"})
	defer c.Close()
	c.AddObject(3)
	if err := c.Dial(coapAddr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if res, err := c.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("register: %+v %v", res, err)
	}
	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.Wait()
	second := start()
	if res, err := c.Update(ctx, nil, nil); err != nil || res.Code != codes.Changed {
		t.Fatalf("update after restart: %+v %v", res, err)
	}
	if err := second.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = second.Wait()
}
