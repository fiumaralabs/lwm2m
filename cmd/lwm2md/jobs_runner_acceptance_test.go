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
	"syscall"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// Exercise the executable's lifecycle, not a runner tick installed by the test.
func TestDaemonJobRunnerAcceptance(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build daemon: %v\n%s", err, out)
	}
	for _, scenario := range []string{"selector-completes", "pause-resume-cancel"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			const token = "lwm_acceptance_synthetic"
			keys := filepath.Join(dir, "keys")
			if err := os.WriteFile(keys, []byte(fmt.Sprintf("key_acceptance sha256:%x jobs:write,devices:read,devices:write", sha256.Sum256([]byte(token)))), 0600); err != nil {
				t.Fatal(err)
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
			coapAddr := udp.LocalAddr().String()
			_ = udp.Close()
			logPath := filepath.Join(dir, "daemon.log")
			logFile, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-job-tick-interval=50ms", "-store", "sqlite:"+filepath.Join(dir, "jobs.db"), "-http", httpAddr, "-api-keys", keys, "-coap", coapAddr, "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
			cmd.Stdout, cmd.Stderr = logFile, logFile
			if err := cmd.Start(); err != nil {
				_ = logFile.Close()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Errorf("signal daemon: %v", err)
				}
				exited := make(chan error, 1)
				go func() { exited <- cmd.Wait() }()
				select {
				case err := <-exited:
					if err != nil {
						t.Errorf("daemon shutdown: %v", err)
					}
				case <-time.After(5 * time.Second):
					_ = cmd.Process.Kill()
					<-exited
					t.Error("daemon did not shut down")
				}
				_ = logFile.Close()
				if t.Failed() {
					b, _ := os.ReadFile(logPath)
					t.Logf("daemon log:\n%s", b)
				}
			})
			client := &http.Client{Timeout: time.Second}
			t.Cleanup(client.CloseIdleConnections)
			base := "http://" + httpAddr
			ready := false
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				res, err := client.Get(base + "/readyz")
				if err == nil {
					_ = res.Body.Close()
					if res.StatusCode == http.StatusOK {
						ready = true
						break
					}
				}
			}
			if !ready {
				t.Fatal("daemon did not become ready")
			}
			request := func(method, path, body string, want int, out any) {
				t.Helper()
				req, err := http.NewRequest(method, base+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				if method == "POST" && path == "/v1/jobs" {
					req.Header.Set("Idempotency-Key", scenario)
				}
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
					t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, res.StatusCode, want, b)
				}
				if out != nil {
					if err := json.Unmarshal(b, out); err != nil {
						t.Fatalf("decode %s: %v: %s", path, err, b)
					}
				}
			}
			// Block actual responses so running is observable and maxConcurrent=1
			// leaves a second task pending while pause is exercised.
			release := make(chan struct{}, 2)
			clients := map[string]*testclient.Client{}
			t.Cleanup(func() {
				close(release)
				for _, c := range clients {
					_ = c.Close()
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, endpoint := range []string{"job-a", "job-b", "job-excluded"} {
				label := "prod"
				if endpoint == "job-excluded" {
					label = "dev"
				}
				request("POST", "/v1/devices", fmt.Sprintf(`{"endpoint":%q,"labels":{"env":%q}}`, endpoint, label), http.StatusCreated, nil)
				c := testclient.New(testclient.Config{Endpoint: endpoint})
				c.AddObject(3)
				c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
					if r.Code == codes.GET && r.Path == "/3/0/1" {
						<-release
						cf := lwm2m.FormatText
						return codes.Content, &cf, []byte("acceptance"), true
					}
					return 0, nil, nil, false
				})
				// Close gates before clients during cleanup, including fatal assertions.
				clients[endpoint] = c
				if err := c.Dial(coapAddr); err != nil {
					t.Fatal(err)
				}
				if res, err := c.Register(ctx); err != nil || res.Code != codes.Created {
					t.Fatalf("register %s: %+v %v", endpoint, res, err)
				}
			}

			reads := func(endpoint string) int {
				n := 0
				for _, r := range clients[endpoint].Requests() {
					if r.Code == codes.GET && r.Path == "/3/0/1" {
						n++
					}
				}
				return n
			}
			totalReads := func() int { return reads("job-a") + reads("job-b") + reads("job-excluded") }
			var job apiv1.Job
			// Future start guarantees a real scheduled state before the daemon owns it.
			start := time.Now().Add(300 * time.Millisecond).UTC().Format(time.RFC3339Nano)
			body := fmt.Sprintf(`{"name":%q,"target":{"selector":"env=prod"},"action":{"operation":{"type":"read","paths":["/3/0/1"]}},"rollout":{"maxConcurrent":1,"failureThresholdPercent":100,"startAt":%q},"ttlSeconds":60}`, scenario, start)
			request("POST", "/v1/jobs", body, http.StatusCreated, &job)
			if job.Status != "scheduled" {
				t.Fatalf("create status=%q want scheduled", job.Status)
			}
			path := "/v1/jobs/" + job.ID
			wait := func(description string, check func(apiv1.Job) bool) apiv1.Job {
				t.Helper()
				var got apiv1.Job
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
					request("GET", path, "", http.StatusOK, &got)
					if check(got) {
						return got
					}
				}
				t.Fatalf("%s: last job=%+v; CoAP reads a=%d b=%d excluded=%d", description, got, reads("job-a"), reads("job-b"), reads("job-excluded"))
				return got
			}
			wait("daemon must autonomously start selector job and dispatch first CoAP read", func(j apiv1.Job) bool { return j.Status == "running" && j.Counts.Total == 2 && totalReads() == 1 })
			if scenario == "selector-completes" {
				release <- struct{}{}
				release <- struct{}{}
				done := wait("daemon must reconcile terminal success", func(j apiv1.Job) bool { return j.Status == "succeeded" })
				if done.Counts.Total != 2 || done.Counts.Succeeded != 2 || done.CompletedAt == nil || reads("job-a") != 1 || reads("job-b") != 1 || reads("job-excluded") != 0 {
					t.Fatalf("selector completion: %+v; reads=%d/%d/%d", done, reads("job-a"), reads("job-b"), reads("job-excluded"))
				}
				return
			}
			request("POST", path+"/pause", "", http.StatusOK, &job)
			if job.Status != "paused" {
				t.Fatalf("pause response: %+v", job)
			}
			request("GET", path, "", http.StatusOK, &job)
			if job.Status != "paused" {
				t.Fatalf("pause not persisted: %+v", job)
			}
			release <- struct{}{}
			wait("paused job must reconcile its already-running task", func(j apiv1.Job) bool {
				return j.Status == "paused" && j.Counts.Succeeded == 1 && j.Counts.Pending == 1
			})
			// Multiple runner intervals: pending work must not dispatch.
			for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				request("GET", path, "", http.StatusOK, &job)
				if job.Status != "paused" || job.Counts.Pending != 1 || totalReads() != 1 {
					t.Fatalf("paused job dispatched pending work: %+v reads=%d", job, totalReads())
				}
			}
			request("POST", path+"/resume", "", http.StatusOK, &job)
			wait("resume must dispatch remaining task", func(j apiv1.Job) bool { return j.Status == "running" && totalReads() == 2 })
			request("POST", path+"/cancel", "", http.StatusOK, &job)
			if job.Status != "cancelled" {
				t.Fatalf("cancel response: %+v", job)
			}
			release <- struct{}{}
			done := wait("cancel must persist and reconcile in-flight task", func(j apiv1.Job) bool {
				return j.Status == "cancelled" && j.Counts.Succeeded == 2 && j.Counts.Running == 0
			})
			if reads("job-a") != 1 || reads("job-b") != 1 || reads("job-excluded") != 0 {
				t.Fatalf("cancelled selector job: %+v reads=%d/%d/%d", done, reads("job-a"), reads("job-b"), reads("job-excluded"))
			}
		})
	}
}
