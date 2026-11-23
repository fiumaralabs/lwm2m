package main

import (
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestDaemonRecoversKilledJobDispatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.db")
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
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
	db.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	start := func() *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary, "-job-tick-interval=500ms", "-store", "sqlite:"+path, "-http", addr, "-coap=", "-coaps=", "-bs-coap=", "-bs-coaps=", "-bs-http=")
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
	cmd := start()
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		if len(db.Jobs().ListTasks("default", job.ID, "dispatching")) > 0 {
			observed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !observed {
		db.Close()
		t.Fatal("never observed dispatching claim")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	claims := len(db.Jobs().ListTasks("default", job.ID, "dispatching"))
	db.Close()
	if claims == 0 {
		t.Fatal("process was not killed mid-dispatch")
	}
	restarted := start()
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
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
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
