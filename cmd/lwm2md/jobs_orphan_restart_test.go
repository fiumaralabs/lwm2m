package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDaemonRecoversKilledJobDispatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.db")
	// Overlay the runner only in this test's daemon binary. The released daemon
	// has no test hook; the overlay pauses after ClaimTasks has committed.
	runner, err := filepath.Abs("../../apiv1/jobs_runner.go")
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(runner)
	if err != nil {
		t.Fatal(err)
	}
	const anchor = "				var admissionErr error"
	if strings.Count(string(source), anchor) != 1 {
		t.Fatal("job runner claim hook anchor changed")
	}
	instrumented := strings.Replace(string(source), "\"bytes\"", "\"bytes\"\n	\"os\"", 1)
	instrumented = strings.Replace(instrumented, anchor, `				if marker := os.Getenv("LWM2M_TEST_CLAIM_MARKER"); marker != "" && len(claimed) > 0 {
					if err := os.WriteFile(marker, []byte("claimed"), 0600); err != nil { panic(err) }
					select {}
				}
`+anchor, 1)
	overlaySource := filepath.Join(dir, "jobs_runner.go")
	if err := os.WriteFile(overlaySource, []byte(instrumented), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{runner: overlaySource}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-overlay", overlayPath, "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const count = 200
	devices := make([]string, count)
	for i := range devices {
		devices[i] = fmt.Sprintf("device-%03d", i)
	}
	job := apiv1.Job{ID: "orphan-job", Target: apiv1.JobTarget{Devices: devices}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read","paths":["/3/0/1"],"ttlSeconds":1}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: count, FailureThresholdPercent: 100}, TTLSeconds: 60}
	if err := db.Jobs().Create("default", job); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.Jobs().StartJob("default", job.ID, devices, time.Now()); !ok || err != nil {
		t.Fatalf("start job: %v %v", ok, err)
	}
	// Keep the initialized connection open across both daemon runs. Opening a
	// second store while the daemon writes can race SQLite migrations.
	defer db.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	marker := filepath.Join(dir, "claimed")
	start := func(pause bool) *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary, "-job-tick-interval=500ms", "-store", "sqlite:"+path, "-http", addr, "-coap=", "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
		for _, env := range os.Environ() {
			if !strings.HasPrefix(env, "LWM2M_TEST_CLAIM_MARKER=") {
				cmd.Env = append(cmd.Env, env)
			}
		}
		if pause {
			cmd.Env = append(cmd.Env, "LWM2M_TEST_CLAIM_MARKER="+marker)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		client := &http.Client{Timeout: time.Second}
		defer client.CloseIdleConnections()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			res, err := client.Get("http://" + addr + "/readyz")
			if err == nil {
				res.Body.Close()
				if res.StatusCode == http.StatusOK {
					return cmd
				}
			}
		}
		t.Fatal("daemon did not become ready")
		return nil
	}
	cmd := start(true)
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not signal a persisted claim")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(db.Jobs().ListTasks("default", job.ID, "dispatching")) == 0 {
		t.Fatal("signaled claim was not persisted")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	claims := len(db.Jobs().ListTasks("default", job.ID, "dispatching"))
	if claims == 0 {
		t.Fatal("process was not killed mid-dispatch")
	}
	restarted := start(false)
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		res, err := client.Get("http://" + addr + "/readyz")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	var tasks []apiv1.JobTask
	for deadline = time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		tasks = db.Jobs().ListTasks("default", job.ID, "")
		terminal := 0
		for _, task := range tasks {
			if task.Status == "succeeded" || task.Status == "failed" || task.Status == "expired" || task.Status == "cancelled" {
				terminal++
			}
		}
		if terminal == count {
			break
		}
	}
	if len(tasks) != count {
		t.Fatalf("tasks: got %d want %d", len(tasks), count)
	}
	for _, task := range tasks {
		if task.Status != "succeeded" && task.Status != "failed" && task.Status != "expired" && task.Status != "cancelled" {
			t.Fatalf("nonterminal task: %+v (killed with %d claims)", task, claims)
		}
	}
	// Stop the owned daemon before inspecting the database with a raw connection.
	// Unlike sqlite.Open, that connection has no busy timeout for concurrent writes.
	if err := restarted.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = restarted.Wait()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var duplicate int
	if err := raw.QueryRow(`SELECT count(*) FROM (SELECT device FROM operations WHERE tenant='default' GROUP BY device HAVING count(*) > 1)`).Scan(&duplicate); err != nil {
		t.Fatal(err)
	}
	if duplicate != 0 {
		t.Fatalf("%d devices got duplicate operations", duplicate)
	}
}
