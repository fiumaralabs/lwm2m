package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
)

func TestRegistryLoads(t *testing.T) {
	r := demoRegistry()
	vs := r.Versions(3303)
	if len(vs) == 0 {
		t.Fatal("3303 not loaded")
	}
	o, _ := r.Get(3303, vs[0])
	if o.Name != "Temperature" || o.Resources[5700].Name != "Sensor Value" {
		t.Errorf("3303 = %q / %q", o.Name, o.Resources[5700].Name)
	}
	dev, _ := r.Get(3, r.Versions(3)[len(r.Versions(3))-1])
	if e := enumOf(dev.Resources[20]); e["1"] != "Charging" || e["6"] != "Unknown" {
		t.Errorf("battery status enum = %v", e)
	}
	if e := enumOf(dev.Resources[6]); e["1"] != "Internal Battery" || e["5"] != "USB" {
		t.Errorf("power source enum = %v", e)
	}
	if e := enumOf(dev.Resources[0]); e != nil {
		t.Errorf("string resource got enum %v", e)
	}
	conn, _ := r.Get(4, r.Versions(4)[len(r.Versions(4))-1])
	if e := enumOf(conn.Resources[0]); e["7"] != "NB-IoT" || e["10"] != "" {
		t.Errorf("bearer enum = %v", e)
	}
}

type rig struct {
	web  *webAPI
	srv  *server.Server
	http *httptest.Server
	c    *testclient.Client
}

func newRig(t *testing.T, lim webLimits) *rig {
	t.Helper()
	models := server.NewModels(demoRegistry())
	web := newWebAPI(models, lim)
	srv := server.New(server.Config{Store: server.NewMemoryStore(), Schema: models.Schema, Validator: models, OnEvent: web.onEvent})
	web.srv = srv
	cb := coap.New(srv)
	a, err := cb.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(web.routes())
	c := testclient.New(testclient.Config{Endpoint: "t1", Version: "1.1", Lifetime: 300, Binding: "U"})
	c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Acme"))
	c.Set(lwm2m.MustParsePath("/3/0/9"), lwm2m.Integer(87))
	c.Set(lwm2m.MustParsePath("/3303/0/5700"), lwm2m.Float(21.5))
	c.Set(lwm2m.MustParsePath("/3303/0/5701"), lwm2m.String("Cel"))
	if err := c.Dial(a.String()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if r, err := c.Register(ctx); err != nil || r.Code.String() != "Created" {
		t.Fatalf("register: %v %v", r, err)
	}
	t.Cleanup(func() { c.Close(); hs.Close(); cb.Close(); srv.Close() })
	return &rig{web: web, srv: srv, http: hs, c: c}
}

func defaultLimits() webLimits {
	return webLimits{maxReg: 10, maxLifetime: time.Hour, readsPerMin: 100, observeMax: 8, observePerIP: 2, observeFor: time.Minute}
}

func getJSON(t *testing.T, url string, v any) *http.Response {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if v != nil {
		if err := json.NewDecoder(r.Body).Decode(v); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
	}
	return r
}

func TestClientDetail(t *testing.T) {
	g := newRig(t, defaultLimits())
	var list struct {
		Clients []clientJSON `json:"clients"`
	}
	getJSON(t, g.http.URL+"/api/clients", &list)
	if len(list.Clients) != 1 || list.Clients[0].Endpoint != "t1" || list.Clients[0].ExpiresAt.IsZero() {
		t.Fatalf("list = %+v", list.Clients)
	}
	names := map[uint16]string{}
	for _, o := range list.Clients[0].Objects {
		names[o.ID] = o.Name
	}
	if names[3] != "Device" || names[3303] != "Temperature" {
		t.Errorf("object names = %v", names)
	}
	var d struct {
		Client  clientJSON   `json:"client"`
		Objects []objectJSON `json:"objects"`
	}
	getJSON(t, g.http.URL+"/api/clients/t1", &d)
	var temp *objectJSON
	for i := range d.Objects {
		if d.Objects[i].ID == 3303 {
			temp = &d.Objects[i]
		}
	}
	if temp == nil || temp.Model == nil || len(temp.Instances) != 1 {
		t.Fatalf("3303 = %+v", temp)
	}
	found := false
	for _, r := range temp.Model.Resources {
		if r.ID == 5700 && r.Name == "Sensor Value" && r.Ops == "R" && r.Type == "float" {
			found = true
		}
	}
	if !found {
		t.Errorf("5700 missing from %+v", temp.Model.Resources)
	}
	if r := getJSON(t, g.http.URL+"/api/clients/nobody", nil); r.StatusCode != 404 {
		t.Errorf("unknown endpoint: %d", r.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	nodes := []lwm2m.Node{{Path: lwm2m.MustParsePath("/3303/0/5700"), Kind: lwm2m.KindValue, Value: lwm2m.Float(19.5)}}
	if r, err := g.c.Send(ctx, nodes, lwm2m.FormatSenMLCBOR); err != nil || r.Code.String() != "Changed" {
		t.Fatalf("send: %v %v", r, err)
	}
	var d2 struct {
		LastSend *sendJSON `json:"last_send"`
	}
	getJSON(t, g.http.URL+"/api/clients/t1", &d2)
	if d2.LastSend == nil || len(d2.LastSend.Nodes) != 1 || d2.LastSend.Nodes[0].V != 19.5 {
		t.Errorf("last_send = %+v", d2.LastSend)
	}
}

func TestBlockedObjects(t *testing.T) {
	if !blockedObject(0) || !blockedObject(21) || blockedObject(3) {
		t.Error("blockedObject")
	}
	g := newRig(t, defaultLimits())
	for _, p := range []string{"/0", "/0/0/3", "/21/0"} {
		for _, op := range []string{"read", "observe"} {
			if r := getJSON(t, g.http.URL+"/api/clients/t1/"+op+"?path="+p, nil); r.StatusCode != 403 {
				t.Errorf("%s %s: %d", op, p, r.StatusCode)
			}
		}
	}
}

func TestReadTypedAndRateLimit(t *testing.T) {
	lim := defaultLimits()
	lim.readsPerMin = 2
	g := newRig(t, lim)
	var out struct {
		Code  string     `json:"code"`
		Nodes []nodeJSON `json:"nodes"`
		Hex   string     `json:"payload_hex"`
	}
	r := getJSON(t, g.http.URL+"/api/clients/t1/read?path=/3/0", &out)
	if out.Code != "2.05" || out.Hex == "" || r.Header.Get("X-RateLimit-Remaining") != "1" {
		t.Fatalf("read = %+v, remaining %q", out, r.Header.Get("X-RateLimit-Remaining"))
	}
	got := map[string]any{}
	for _, n := range out.Nodes {
		got[n.Path] = n.V
	}
	if got["/3/0/0"] != "Acme" || got["/3/0/9"] != float64(87) {
		t.Errorf("typed values = %v", got)
	}
	getJSON(t, g.http.URL+"/api/clients/t1/read?path=/3/0/9", nil)
	r = getJSON(t, g.http.URL+"/api/clients/t1/read?path=/3/0/9", nil)
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Errorf("third read: %d", r.StatusCode)
	}
}

type sse struct {
	body   io.ReadCloser
	events chan [2]string
}

func openSSE(t *testing.T, url string) (*sse, *http.Response) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, r
	}
	s := &sse{body: r.Body, events: make(chan [2]string, 16)}
	go func() {
		sc := bufio.NewScanner(r.Body)
		var ev, data string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = line[7:]
			case strings.HasPrefix(line, "data: "):
				data = line[6:]
			case line == "" && ev != "":
				s.events <- [2]string{ev, data}
				ev, data = "", ""
			}
		}
		close(s.events)
	}()
	t.Cleanup(func() { r.Body.Close() })
	return s, r
}

func (s *sse) next(t *testing.T, want string) string {
	t.Helper()
	for {
		select {
		case e, ok := <-s.events:
			if !ok {
				t.Fatalf("stream closed waiting for %q", want)
			}
			if e[0] == want {
				return e[1]
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %q event", want)
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func TestObserveStream(t *testing.T) {
	g := newRig(t, defaultLimits())
	url := g.http.URL + "/api/clients/t1/observe?path=/3303/0/5700"
	s, r := openSSE(t, url)
	if s == nil {
		t.Fatalf("observe: %d", r.StatusCode)
	}
	if ct := r.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q", ct)
	}
	s.next(t, "open")
	if v := s.next(t, "value"); !strings.Contains(v, `"v":21.5`) {
		t.Errorf("first value %s", v)
	}
	// a second visitor shares the one Observe on the client
	s2, _ := openSSE(t, url)
	if s2 == nil {
		t.Fatal("second visitor refused")
	}
	s2.next(t, "open")
	s2.next(t, "value")
	if n := len(g.srv.Observations("t1")); n != 1 {
		t.Errorf("%d observations on the client, want 1", n)
	}
	time.Sleep(300 * time.Millisecond) // past minGap
	g.c.Set(lwm2m.MustParsePath("/3303/0/5700"), lwm2m.Float(22.25))
	if v := s.next(t, "value"); !strings.Contains(v, `"v":22.25`) {
		t.Errorf("notification %s", v)
	}
	if v := s2.next(t, "value"); !strings.Contains(v, `"v":22.25`) {
		t.Errorf("notification to second visitor %s", v)
	}
	// the last visitor leaving cancels the Observe
	s.body.Close()
	s2.body.Close()
	waitFor(t, "observation cancelled", func() bool { return len(g.srv.Observations("t1")) == 0 })
	waitFor(t, "visitors released", func() bool { st, subs := g.web.hub.open(); return st == 0 && subs == 0 })
}

func TestObserveLimits(t *testing.T) {
	lim := defaultLimits()
	lim.observePerIP = 1
	g := newRig(t, lim)
	base := g.http.URL + "/api/clients/t1/observe?path="
	if r := getJSON(t, base+"/3", nil); r.StatusCode != 400 {
		t.Errorf("object-level observe: %d", r.StatusCode)
	}
	if r := getJSON(t, base+"/3304/0/5700", nil); r.StatusCode != 404 {
		t.Errorf("unregistered object: %d", r.StatusCode)
	}
	s, _ := openSSE(t, base+"/3303/0/5700")
	if s == nil {
		t.Fatal("first stream refused")
	}
	s.next(t, "value")
	if _, r := openSSE(t, base+"/3/0/9"); r.StatusCode != 429 {
		t.Errorf("second stream from one IP: %d", r.StatusCode)
	}
}

func TestObserveEnds(t *testing.T) {
	lim := defaultLimits()
	lim.observeFor = 700 * time.Millisecond
	g := newRig(t, lim)
	s, _ := openSSE(t, g.http.URL+"/api/clients/t1/observe?path=/3/0/9")
	if s == nil {
		t.Fatal("refused")
	}
	if end := s.next(t, "end"); !strings.Contains(end, "stop after") {
		t.Errorf("time limit end = %s", end)
	}
	waitFor(t, "observation cancelled after time limit", func() bool { return len(g.srv.Observations("t1")) == 0 })

	g.web.hub.dur = time.Minute
	s, _ = openSSE(t, g.http.URL+"/api/clients/t1/observe?path=/3/0/9")
	s.next(t, "value")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := g.c.Deregister(ctx); err != nil {
		t.Fatal(err)
	}
	if end := s.next(t, "end"); !strings.Contains(end, "deregistered") {
		t.Errorf("deregister end = %s", end)
	}
	waitFor(t, "hub empty", func() bool { st, subs := g.web.hub.open(); return st == 0 && subs == 0 })
}

func TestStaticHeaders(t *testing.T) {
	g := newRig(t, defaultLimits())
	r := getJSON(t, g.http.URL+"/", nil)
	csp := r.Header.Get("Content-Security-Policy")
	if r.StatusCode != 200 || !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "unsafe") {
		t.Errorf("index: %d CSP %q", r.StatusCode, csp)
	}
	etag := r.Header.Get("ETag")
	req, _ := http.NewRequest("GET", g.http.URL+"/", nil)
	req.Header.Set("If-None-Match", etag)
	if r2, err := http.DefaultClient.Do(req); err != nil || r2.StatusCode != 304 {
		t.Errorf("revalidation: %v %v", r2, err)
	}
	r = getJSON(t, g.http.URL+"/fonts/inter.woff2", nil)
	if r.Header.Get("Content-Type") != "font/woff2" || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("font headers %v", r.Header)
	}
	for _, p := range []string{"/app.js", "/style.css"} {
		if r := getJSON(t, g.http.URL+p, nil); r.StatusCode != 200 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	if r := getJSON(t, g.http.URL+"/nope", nil); r.StatusCode != 404 {
		t.Errorf("missing file: %d", r.StatusCode)
	}
}
