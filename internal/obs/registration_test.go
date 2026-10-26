package obs_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/obs"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestRegistrationGauge(t *testing.T) {
	srv := server.New(server.Config{})
	t.Cleanup(func() { _ = srv.Close() })
	binding := coap.New(srv)
	t.Cleanup(func() { _ = binding.Close() })
	addr, err := binding.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := obs.New(nil, func() int { return len(srv.Store().All()) }, true)
	scrape := func(want int) {
		t.Helper()
		w := probe(h, "GET", "/metrics")
		if w.Code != 200 || w.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
			t.Fatalf("scrape: %d %v", w.Code, w.Header())
		}
		lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
		if len(lines) != 3 || !strings.HasPrefix(lines[0], "# HELP lwm2m_registrations ") || lines[1] != "# TYPE lwm2m_registrations gauge" {
			t.Fatalf("exposition: %q", w.Body.String())
		}
		var name string
		var value float64
		if n, err := fmt.Sscanf(lines[2], "%s %f", &name, &value); err != nil || n != 2 || name != "lwm2m_registrations" || value != float64(want) {
			t.Fatalf("sample: %q; want %d", lines[2], want)
		}
	}
	scrape(0)
	client := testclient.New(testclient.Config{Endpoint: "metrics-test"})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		r, err := client.Register(ctx)
		if err != nil || r.Code != 65 {
			t.Fatalf("register: %v %v", r, err)
		}
		scrape(1)
	}
	r, err := client.Deregister(ctx)
	if err != nil || r.Code != 66 {
		t.Fatalf("deregister: %v %v", r, err)
	}
	scrape(0)
}
