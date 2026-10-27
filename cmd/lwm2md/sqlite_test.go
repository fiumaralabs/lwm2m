package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"

	"github.com/fiumaralabs/lwm2m/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestSQLiteOperationsAndEventsRestart(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "lwm2md")
	if out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	keys := filepath.Join(dir, "keys")
	const token = "lwm_sqlite_synthetic"
	if err := os.WriteFile(keys, []byte(fmt.Sprintf("key_sqlite sha256:%x devices:read,devices:write,operations:write", sha256.Sum256([]byte(token)))), 0600); err != nil {
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
		t.Cleanup(func() { _ = logFile.Close() })
		cmd.Stdout, cmd.Stderr = logFile, logFile
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
				if res.StatusCode == 200 {
					return cmd
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		b, _ := os.ReadFile(logFile.Name())
		t.Fatalf("daemon did not become ready: %s", b)
		return nil
	}
	request := func(method, path, body string, want int) string {
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
			t.Fatalf("%s: status %d, %s, %v", path, res.StatusCode, b, err)
		}
		return string(b)
	}
	decode := func(body string) operations.Operation {
		t.Helper()
		var op operations.Operation
		if err := json.Unmarshal([]byte(body), &op); err != nil {
			t.Fatal(err)
		}
		return op
	}
	cmd := start()
	// Two events for one endpoint make its pagination cursor meaningful.
	request("POST", "/v1/devices", `{"endpoint":"persisted"}`, 201)
	request("DELETE", "/v1/devices/persisted", "", 204)
	request("POST", "/v1/devices", `{"endpoint":"persisted","name":"pump","labels":{"site":"lab"}}`, 201)
	request("POST", "/v1/devices", `{"endpoint":"unrelated"}`, 201)
	type eventPage struct {
		Items      []store.Event `json:"items"`
		NextCursor string        `json:"nextCursor"`
	}
	history := func(path string) eventPage {
		t.Helper()
		var page eventPage
		if err := json.Unmarshal([]byte(request("GET", path, "", 200)), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	stream := func(since string, count int) []store.Event {
		t.Helper()
		req, err := http.NewRequest("GET", "http://"+addr+"/v1/events?since="+url.QueryEscape(since), nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("event stream: %s, %v", res.Status, res.Header)
		}
		var events []store.Event
		var id, kind string
		var event store.Event
		scanner := bufio.NewScanner(res.Body)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				kind = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
					t.Fatal(err)
				}
			case line == "":
				if id != event.Sequence || kind != event.Type || !store.ValidSequence(id) {
					t.Fatalf("invalid SSE frame: id=%q kind=%q event=%+v", id, kind, event)
				}
				events = append(events, event)
				if len(events) == count {
					return events
				}
			}
		}
		t.Fatalf("stream returned %d/%d events: %v", len(events), count, scanner.Err())
		return nil
	}
	global := stream("", 4)
	paths := []string{"/v1/devices/persisted/events"}
	full, first, rest := map[string]eventPage{}, map[string]eventPage{}, map[string]eventPage{}
	for i, path := range paths {
		full[path] = history(path)
		if len(full[path].Items) != 3+i {
			t.Fatalf("%s: unexpected initial history: %+v", path, full[path])
		}
		first[path] = history(path + "?limit=1")
		if len(first[path].Items) != 1 || first[path].NextCursor == "" {
			t.Fatalf("%s: missing first page/cursor: %+v", path, first[path])
		}
		rest[path] = history(path + "?limit=1&cursor=" + url.QueryEscape(first[path].NextCursor))
		if len(rest[path].Items) != 1 || rest[path].Items[0].ID != full[path].Items[1].ID {
			t.Fatalf("%s: cursor did not advance: %+v", path, rest[path])
		}
	}
	before := request("GET", "/v1/devices/persisted", "", 200)
	queued := decode(request("POST", "/v1/devices/persisted/operations", `{"type":"read","paths":["/3/0/0"],"ttlSeconds":120}`, 202))
	if queued.ID == "" || queued.Status != operations.Queued {
		t.Fatalf("not queued: %+v", queued)
	}
	operationPath := "/v1/operations/" + queued.ID
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	cmd = start()
	if got := stream("", len(global)); !reflect.DeepEqual(got, global) {
		t.Fatalf("global replay changed after restart: before=%+v after=%+v", global, got)
	}
	if got := stream(global[1].Sequence, 2); !reflect.DeepEqual(got, global[2:]) {
		t.Fatalf("global replay cursor changed after restart: %+v", got)
	}
	for _, path := range paths {
		for _, check := range []struct {
			query string
			want  eventPage
		}{
			{"", full[path]},
			{"?limit=1", first[path]},
			{"?limit=1&cursor=" + url.QueryEscape(first[path].NextCursor), rest[path]},
		} {
			if got := history(path + check.query); !reflect.DeepEqual(got, check.want) {
				t.Fatalf("%s%s changed after kill/restart: before=%+v after=%+v", path, check.query, check.want, got)
			}
		}
	}
	request("POST", "/v1/devices", `{"endpoint":"after-restart"}`, 201)
	continued := stream(global[len(global)-1].Sequence, 1)[0]
	if continued.Type != "lwm2m.device.created" || continued.Subject != "after-restart" ||
		continued.Sequence <= global[len(global)-1].Sequence {
		t.Fatalf("event sequence did not continue after restart: %+v", continued)
	}
	after := request("GET", "/v1/devices/persisted", "", 200)
	if before != after {
		t.Fatalf("device changed after kill/restart: before=%s after=%s", before, after)
	}
	recovered := decode(request("GET", operationPath, "", 200))
	if recovered.ID != queued.ID || recovered.Status != operations.Queued || recovered.Device != queued.Device || string(recovered.Request) != string(queued.Request) {
		t.Fatalf("queued operation changed: before=%+v after=%+v", queued, recovered)
	}
	c := testclient.New(testclient.Config{Endpoint: "persisted"})
	defer c.Close()
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("restart-pump"))
	delivered := make(chan testclient.Request, 4)
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		select {
		case delivered <- r:
		default:
		}
		return 0, nil, nil, false
	})
	if err := c.Dial(coapAddr); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if res, err := c.Register(ctx); err != nil || res.Code != codes.Created {
		t.Fatalf("register: %+v %v", res, err)
	}
	select {
	case r := <-delivered:
		if r.Code != codes.GET || r.Path != "/3/0/0" {
			t.Fatalf("unexpected delivery: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("recovered read not delivered")
	}
	var completed operations.Operation
	for {
		completed = decode(request("GET", operationPath, "", 200))
		if completed.Status != operations.Queued && completed.Status != operations.Sent {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("operation not completed: %+v", completed)
		case <-time.After(10 * time.Millisecond):
		}
	}
	var result struct {
		Code   string
		Values []struct{ Path, Type, Value string }
	}
	if err := json.Unmarshal(completed.Result, &result); err != nil {
		t.Fatal(err)
	}
	if completed.ID != queued.ID || completed.Status != operations.Succeeded || completed.Error != nil || completed.CompletedAt == nil || result.Code != "2.05" || len(result.Values) != 1 || result.Values[0].Path != "/3/0/0" || result.Values[0].Type != "string" || result.Values[0].Value != "restart-pump" {
		t.Fatalf("unexpected completion: %+v result=%s", completed, completed.Result)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("graceful exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
		t.Fatal("SIGTERM shutdown exceeded deadline")
	}
}
