package leshanapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/transport/coap"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

type env struct {
	t    *testing.T
	srv  *server.Server
	api  *API
	http *httptest.Server
	udp  string
	dtls string
	l    *leshan
	ctx  context.Context
}

func newEnv(t *testing.T, mod func(*server.Config, *Options)) *env {
	t.Helper()
	models := server.NewModels(model.Default())
	opts := Options{Schema: models.Schema, Heartbeat: 200 * time.Millisecond}
	cfg := server.Config{Schema: models.Schema, RequestTimeout: 5 * time.Second}
	if mod != nil {
		mod(&cfg, &opts)
	}
	e := &env{t: t, api: New(opts)}
	cfg.OnEvent = e.api.OnEvent
	e.srv = server.New(cfg)
	e.api.Attach(e.srv)
	cb := coap.New(e.srv)
	a, err := cb.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	e.udp = a.String()
	d, err := cb.ListenDTLS("127.0.0.1:0", coap.DTLSConfig{CIDLength: 6})
	if err != nil {
		t.Fatal(err)
	}
	e.dtls = d.String()
	e.http = httptest.NewServer(e.api)
	var cancel context.CancelFunc
	e.ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(func() {
		cancel()
		e.http.CloseClientConnections()
		e.http.Close()
		_ = cb.Close()
		_ = e.srv.Close()
	})
	e.l = newLeshan(t, e.http.URL+"/api")
	return e
}

func mp(s string) lwm2m.Path { return lwm2m.MustParsePath(s) }

// device is a client with the Zephyr interop sample's objects (I/src).
func (e *env) device(cfg testclient.Config) *testclient.Client {
	e.t.Helper()
	c := testclient.New(cfg)
	for p, v := range map[string]lwm2m.Value{
		"/1/0/0": lwm2m.Integer(1), "/1/0/1": lwm2m.Integer(86400), "/1/0/2": lwm2m.Integer(1),
		"/1/0/3": lwm2m.Integer(10), "/1/0/5": lwm2m.Integer(86400), "/1/0/6": lwm2m.Boolean(false),
		"/1/0/7": lwm2m.String("U"),
		"/3/0/0": lwm2m.String("Zephyr"), "/3/0/1": lwm2m.String("client-1"), "/3/0/2": lwm2m.String("serial-1"),
		"/3/0/3": lwm2m.String("1.2.3"), "/3/0/6/0": lwm2m.Integer(1), "/3/0/6/1": lwm2m.Integer(5),
		"/3/0/7/0": lwm2m.Integer(3800), "/3/0/7/1": lwm2m.Integer(5000), "/3/0/11/0": lwm2m.Integer(0),
		"/3/0/16":   lwm2m.String("U"),
		"/16/0/0/0": lwm2m.String("Host Device ID #1"), "/5/0/3": lwm2m.Integer(0),
	} {
		c.Set(mp(p), v)
	}
	addr := e.udp
	if cfg.PSKIdentity != "" {
		addr = e.dtls
	}
	if err := c.Dial(addr); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = c.Close() })
	r, err := c.Register(e.ctx)
	if err != nil || r.Code != codes.Created {
		e.t.Fatalf("register: %v %v", r, err)
	}
	return c
}

// must fails the test where the harness would raise.
func must(t *testing.T) func(any, error) any {
	return func(v any, err error) any {
		t.Helper()
		if err != nil {
			t.Fatalf("harness would raise: %v", err)
		}
		return v
	}
}

func status(t *testing.T, v any) string {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not a response object: %#v", v)
	}
	s, _ := m["status"].(string)
	return s
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got %#v\nwant %#v", what, got, want)
	}
}

// rawJSON fetches a URL and returns its decoded JSON body and HTTP status.
func (e *env) raw(method, path, body string) (int, any) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.http.URL+"/api"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var v any
	if len(b) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(b, &v); err != nil {
			e.t.Fatalf("%s: %v", b, err)
		}
	} else if len(b) > 0 {
		v = string(b)
	}
	return resp.StatusCode, v
}

// golden compares v with a JSON document semantically.
func golden(t *testing.T, what string, v any, want string) {
	t.Helper()
	var w, gv any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad golden %s: %v", what, err)
	}
	g, _ := json.Marshal(v)
	_ = json.Unmarshal(g, &gv)
	if !reflect.DeepEqual(gv, w) {
		t.Fatalf("%s:\n got %s\nwant %s", what, g, want)
	}
}

func TestSecurityClients(t *testing.T) {
	e := newEnv(t, nil)
	l := e.l
	eq(t, "empty list", must(t)(l.get("/security/clients")), []any{})

	// create_psk_device: hand-built JSON with extra spaces, trailing slash.
	eq(t, "create", must(t)(l.createPSKDevice("client_a3", "abcdefghijklmnop")), nil)
	_, v := e.raw("GET", "/security/clients", "")
	golden(t, "psk list", v, `[{"endpoint":"client_a3","tls":{"mode":"psk","details":{"identity":"client_a3","key":"6162636465666768696a6b6c6d6e6f70"}}}]`)

	// A non-unique identity is HTTP 400, which raises.
	if _, err := l.put("/security/clients/", `{"endpoint":"other","tls":{"mode":"psk","details":{"identity":"client_a3","key":"00"}}}`, ""); err == nil {
		t.Fatal("duplicate PSK identity accepted")
	}

	// The stored key authenticates a DTLS client (CID on, like Zephyr).
	c := e.device(testclient.Config{Endpoint: "client_a3", PSKIdentity: "client_a3", PSKKey: []byte("abcdefghijklmnop"), CID: true})
	reg := must(t)(l.get("/clients/client_a3")).(map[string]any)
	eq(t, "secure", reg["secure"], true)
	eq(t, "registrationId", reg["registrationId"], c.Location())

	// X.509 shape; RPK is refused (HTTP 400): pion/dtls has no RFC 7250.
	if _, err := l.put("/security/clients", `{"endpoint":"rpk1","tls":{"mode":"rpk","details":{"key":"3059"}}}`, ""); err == nil {
		t.Fatal("RPK credential accepted")
	}
	eq(t, "x509", must(t)(l.put("/security/clients", `{"endpoint":"x1","tls":{"mode":"x509"}}`, "")), nil)
	_, v = e.raw("GET", "/security/clients", "")
	golden(t, "all modes", v, `[
		{"endpoint":"client_a3","tls":{"mode":"psk","details":{"identity":"client_a3","key":"6162636465666768696a6b6c6d6e6f70"}}},
		{"endpoint":"x1","tls":{"mode":"x509"}}]`)
	if _, err := l.put("/security/clients", `{"endpoint":"bad","tls":{"mode":"psk","details":{"identity":"bad","key":"zz"}}}`, ""); err == nil {
		t.Fatal("non-hex key accepted")
	}

	// delete_device: 200 empty, then 200 {"message":"not_found"}.
	eq(t, "delete", must(t)(l.deleteDevice("x1")), nil)
	golden(t, "delete missing", must(t)(l.deleteDevice("x1")), `{"message":"not_found"}`)
	_, v = e.raw("GET", "/security/server", "")
	golden(t, "server security", v, `{}`)
}

func TestRegistrationJSON(t *testing.T) {
	e := newEnv(t, nil)
	c := e.device(testclient.Config{Endpoint: "client_5e", Lifetime: 30})
	before := time.Now().UnixMilli()
	reg := must(t)(e.l.get("/clients/client_5e")).(map[string]any)
	// Dynamic fields: ms epoch numbers, the /rd/<id> segment, the address.
	if lu, ok := reg["lastUpdate"].(int); !ok || int64(lu) > before || int64(lu) < before-10_000 {
		t.Fatalf("lastUpdate %#v is not ms now", reg["lastUpdate"])
	}
	eq(t, "registrationId", reg["registrationId"], c.Location())
	_, v := e.raw("GET", "/clients/client_5e", "")
	m := v.(map[string]any)
	for _, k := range []string{"registrationDate", "lastUpdate"} {
		m[k] = 0.0
	}
	eq(t, "address", m["address"], c.LocalAddr().String())
	m["address"], m["registrationId"] = "A", "ID"
	golden(t, "registration", m, `{"endpoint":"client_5e","registrationId":"ID","registrationDate":0,"lastUpdate":0,
		"address":"A","lwM2mVersion":"1.1","lifetime":30,"bindingMode":"U","rootPath":"/",
		"objectLinks":[{"url":"/1/0","attributes":{}},{"url":"/3/0","attributes":{}},{"url":"/5/0","attributes":{}},{"url":"/16/0","attributes":{}}],
		"secure":false,"additionalRegistrationAttributes":{},"queuemode":false,
		"availableInstances":{"1":[0],"3":[0],"5":[0],"16":[0]}}`)

	// Unknown endpoint: HTTP 400, which raises.
	if _, err := e.l.get("/clients/nobody"); err == nil || !strings.Contains(err.Error(), "No registered client with id 'nobody'") {
		t.Fatalf("unknown endpoint: %v", err)
	}
	all := must(t)(e.l.get("/clients")).([]any)
	eq(t, "all registrations", len(all), 1)
}

func TestReadShapes(t *testing.T) {
	e := newEnv(t, nil)
	l := e.l
	c := e.device(testclient.Config{Endpoint: "ep"})
	c.Set(mp("/3/0/13"), lwm2m.Time(1700000000))
	c.Set(mp("/19/0/0/0"), lwm2m.Opaque([]byte{0xde, 0xad}))
	c.Set(mp("/6/0/0"), lwm2m.Float(48.5))

	// Golden node JSON: int ids, INTEGER as strings, JSON booleans, TIME in
	// ms, OPAQUE hex, FLOAT as Java's Double.toString.
	_, v := e.raw("GET", "/clients/ep/1/0?format=SENML_CBOR", "")
	golden(t, "instance", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"id":0,"kind":"instance","resources":[
		{"id":0,"kind":"singleResource","type":"INTEGER","value":"1"},
		{"id":1,"kind":"singleResource","type":"INTEGER","value":"86400"},
		{"id":2,"kind":"singleResource","type":"INTEGER","value":"1"},
		{"id":3,"kind":"singleResource","type":"INTEGER","value":"10"},
		{"id":5,"kind":"singleResource","type":"INTEGER","value":"86400"},
		{"id":6,"kind":"singleResource","type":"BOOLEAN","value":false},
		{"id":7,"kind":"singleResource","type":"STRING","value":"U"}]}}`)
	_, v = e.raw("GET", "/clients/ep/3/0/7?format=TLV", "")
	golden(t, "multiResource", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"id":7,"kind":"multiResource","type":"INTEGER","values":{"0":"3800","1":"5000"}}}`)
	_, v = e.raw("GET", "/clients/ep/3/0/11/0", "")
	golden(t, "resourceInstance", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"id":0,"kind":"resourceInstance","type":"INTEGER","value":"0"}}`)
	_, v = e.raw("GET", "/clients/ep/3/0/13?format=SENML_JSON", "")
	golden(t, "time", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"id":13,"kind":"singleResource","type":"TIME","value":1700000000000}}`)
	_, v = e.raw("GET", "/clients/ep/19/0/0?format=SENML_CBOR", "")
	golden(t, "opaque", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"id":0,"kind":"multiResource","type":"OPAQUE","values":{"0":"dead"}}}`)
	_, v = e.raw("GET", "/clients/ep/6/0/0?format=SENML_CBOR", "")
	golden(t, "float", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"id":0,"kind":"singleResource","type":"FLOAT","value":"48.5"}}`)
	// A device error is HTTP 200 + "NAME(code)", with no content.
	code, v := e.raw("GET", "/clients/ep/3/0/99", "")
	eq(t, "http status", code, 200)
	golden(t, "not found", v, `{"status":"NOT_FOUND(404)","valid":true,"success":false,"failure":true}`)

	// What the tests assert, through the leshan.py decoders.
	for _, f := range []string{"SENML_CBOR", "SENML_JSON", "TLV", "JSON", ""} { // int-203/204/231/232/237
		l.format = f
		d := l.read("ep", "3/0").(D)[0].(D)
		eq(t, f+" /3/0/0", d[0], "Zephyr")
		eq(t, f+" /3/0/11", d[11], D{0: 0})
		eq(t, f+" /3/0/16", d[16], "U")
		s := l.read("ep", "1").(D)[1].(D)[0].(D) // int-222
		eq(t, f+" /1/0", s, D{0: 1, 1: 86400, 2: 1, 3: 10, 5: 86400, 6: false, 7: "U"})
		eq(t, f+" /3/0/7", l.read("ep", "3/0/7"), D{7: D{0: 3800, 1: 5000}})
		eq(t, f+" /3/0/11/0", l.read("ep", "3/0/11/0"), 0) // int-225
		eq(t, f+" /1/0/6", l.read("ep", "1/0/6"), false)   // int-224
	}
	l.format = "TEXT" // int-201
	eq(t, "text", l.read("ep", "3/0/0"), "Zephyr")
	eq(t, "text int", l.read("ep", "1/0/1"), 86400)
	l.format = "CBOR" // int-211
	eq(t, "cbor", l.read("ep", "1/0/0"), 1)
	l.format = "SENML_CBOR"

	// int-221: the Security object answers UNAUTHORIZED(401), HTTP 200.
	eq(t, "read /0/0", status(t, l.read("ep", "0/0")), "UNAUTHORIZED(401)")
	eq(t, "write /0/0/0", status(t, must(t)(l.write("ep", "0/0/0", "coap://localhost"))), "UNAUTHORIZED(401)")
	eq(t, "write-attributes /0", status(t, must(t)(l.writeAttributes("ep", "0", []kv{{"pmin", 10}}))), "UNAUTHORIZED(401)")
}

func TestWriteCreateDeleteExecute(t *testing.T) {
	e := newEnv(t, nil)
	l := e.l
	c := e.device(testclient.Config{Endpoint: "ep"})

	eq(t, "write int", status(t, must(t)(l.write("ep", "1/0/1", 63))), "CHANGED(204)") // int-227
	eq(t, "read back", l.read("ep", "1/0/1"), 63)
	eq(t, "write bool", status(t, must(t)(l.write("ep", "1/0/6", true))), "CHANGED(204)")
	eq(t, "bool", l.read("ep", "1/0/6"), true)
	eq(t, "write res inst", status(t, must(t)(l.write("ep", "16/0/0/0", "test"))), "CHANGED(204)") // int-228
	eq(t, "res inst", l.read("ep", "16/0/0/0"), "test")
	l.format = "OPAQUE" // test_blockwise_1
	fw := []byte(strings.Repeat("1234567890", 500))
	eq(t, "write opaque", status(t, must(t)(l.write("ep", "5/0/0", fw))), "CHANGED(204)")
	got, _ := c.Get(mp("/5/0/0"))
	eq(t, "firmware", got.Bytes, fw)
	l.format = "TEXT" // int-205
	eq(t, "text write", status(t, must(t)(l.write("ep", "1/0/2", 101))), "CHANGED(204)")
	eq(t, "text read", l.read("ep", "1/0/2"), 101)

	// int-215/220/226: partial update is POST, replace is PUT.
	for _, f := range []string{"TLV", "JSON", "SENML_CBOR"} {
		l.format = f
		r := must(t)(l.updateObjInstance("ep", "1/0", []kv{{"2", 101}, {"3", 1010}, {"5", 2000}, {"6", true}, {"7", "U"}}))
		eq(t, f+" update", status(t, r), "CHANGED(204)")
		last, _ := c.LastRequest()
		eq(t, f+" update method", last.Code, codes.POST)
		s := l.read("ep", "1/0").(D)[0].(D)
		eq(t, f+" updated", []any{s[2], s[3], s[5], s[6], s[7]}, []any{101, 1010, 2000, true, "U"})
		r = must(t)(l.replaceObjInstance("ep", "1/0", []kv{{"1", 86400}, {"2", 1}, {"3", 10}, {"5", 86400}, {"6", false}, {"7", "U"}}))
		eq(t, f+" replace", status(t, r), "CHANGED(204)")
		last, _ = c.LastRequest()
		eq(t, f+" replace method", last.Code, codes.PUT)
	}
	l.format = "SENML_CBOR"

	// int-256: the device's 4.05 is reported as METHOD_NOT_ALLOWED(405).
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		return codes.MethodNotAllowed, nil, nil, r.Code == codes.PUT && r.Path == "/1/0/0"
	})
	eq(t, "read-only write", status(t, must(t)(l.write("ep", "1/0/0", 123))), "METHOD_NOT_ALLOWED(405)")
	c.SetOverride(nil)

	// int-228/1630: create (id in the body, not the URL), then delete.
	r := must(t)(l.createObjInstance("ep", "16/1", []kv{{"0", []kv{{"0", "Host Device ID #2"}, {"1", "Host Device Model #2"}}}}))
	golden(t, "create", r, `{"status":"CREATED(201)","valid":true,"success":true,"failure":false,"location":"/16/1"}`)
	eq(t, "portfolio", l.read("ep", "16"), D{16: D{0: D{0: D{0: "test"}}, 1: D{0: D{0: "Host Device ID #2", 1: "Host Device Model #2"}}}})
	golden(t, "delete", must(t)(l.delete("ep", "16/1")), `{"status":"DELETED(202)","valid":true,"success":true,"failure":false}`)
	if _, ok := c.Get(mp("/16/1/0/0")); ok {
		t.Fatal("instance not deleted")
	}

	// Execute: POST with 4 segments, no query, no body.
	golden(t, "execute", must(t)(l.execute("ep", "1/0/8")), `{"status":"CHANGED(204)","valid":true,"success":true,"failure":false}`)
	if ex := c.Executed(); len(ex) != 1 || ex[0].Path != "/1/0/8" || len(ex[0].Body) != 0 {
		t.Fatalf("executed %+v", ex)
	}

	// int-230/257: Write-Composite with a multi resource and a time.
	for _, f := range []string{"SENML_JSON", "SENML_CBOR"} {
		l.format = f
		r := must(t)(l.compositeWrite("ep", []kv{{"/1/0/1", 60}, {"/1/0/6", true},
			{"/16/0/0", []kv{{"0", "aa"}, {"1", "bb"}, {"2", "cc"}, {"3", "dd"}}}, {"/3/0/13", time.Unix(0, 0)}}))
		eq(t, f+" composite write", status(t, r), "CHANGED(204)")
		s := l.read("ep", "1/0").(D)[0].(D)
		eq(t, f+" /1/0/1", s[1], 60)
		eq(t, f+" /1/0/6", s[6], true)
		eq(t, f+" /16/0/0", l.read("ep", "16/0/0"), D{0: D{0: "aa", 1: "bb", 2: "cc", 3: "dd"}})
		tv, _ := c.Get(mp("/3/0/13"))
		eq(t, f+" time", tv, lwm2m.Time(0))
	}
}

func TestAttributesAndDiscover(t *testing.T) {
	e := newEnv(t, nil)
	l := e.l
	c := e.device(testclient.Config{Endpoint: "ep"})

	eq(t, "write attrs", status(t, must(t)(l.writeAttributes("ep", "3/0/7", []kv{{"lt", 1}, {"gt", 6}, {"st", 1}}))), "CHANGED(204)")
	eq(t, "client attrs", c.Attributes(mp("/3/0/7")), []string{"lt=1", "gt=6", "st=1"})
	must(t)(l.removeAttributes("ep", "3/0/7", []string{"gt", "lt", "st"}))
	eq(t, "removed", len(c.Attributes(mp("/3/0/7"))), 0)

	// The device decides on attributes for a string resource; HTTP stays 2xx.
	c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		return codes.BadRequest, nil, []byte("gt on string"), r.Path == "/3/0/0"
	})
	r := must(t)(l.writeAttributes("ep", "3/0/0", []kv{{"gt", 1}}))
	golden(t, "rejected attrs", r, `{"status":"BAD_REQUEST(400)","valid":true,"success":false,"failure":true,"errormessage":"gt on string"}`)
	c.SetOverride(nil)

	// int-260: discover, objectLinks as {url: attributes} with string values.
	_, v := e.raw("GET", "/clients/ep/3/0/discover", "")
	golden(t, "discover", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"objectLinks":[
		{"url":"/3/0","attributes":{}},{"url":"/3/0/0","attributes":{}},{"url":"/3/0/1","attributes":{}},
		{"url":"/3/0/2","attributes":{}},{"url":"/3/0/3","attributes":{}},{"url":"/3/0/6","attributes":{"dim":"2"}},
		{"url":"/3/0/7","attributes":{"dim":"2"}},{"url":"/3/0/11","attributes":{"dim":"1"}},{"url":"/3/0/16","attributes":{}}]}`)
	d := l.discover("ep", "3/0")
	if l.pyInt(d["/3/0/7"].(map[string]any)["dim"]) != 2 {
		t.Fatal("int(dim)")
	}
}

func TestComposite(t *testing.T) {
	e := newEnv(t, nil)
	l := e.l
	e.device(testclient.Config{Endpoint: "ep"})
	for _, f := range []string{"SENML_JSON", "SENML_CBOR"} {
		l.format = f
		// int-280
		r := l.compositeRead("ep", []string{"/3/0/16", "/3/0/11/0", "/1/0"})
		eq(t, f+" int-280", r, D{3: D{0: D{16: "U", 11: D{0: 0}}}, 1: D{0: D{0: 1, 1: 86400, 2: 1, 3: 10, 5: 86400, 6: false, 7: "U"}}})
		// int-281: /1/0/8 has no value and is omitted.
		eq(t, f+" int-281", l.compositeRead("ep", []string{"/1/0/1", "/1/0/7", "/1/0/8"}), D{1: D{0: D{1: 86400, 7: "U"}}})
		// int-236: unknown objects are absent from content, never null.
		r = l.compositeRead("ep", []string{"1/0", "/3/0/11/0", "/3339/0/5522", "/3353/0/6030"})
		eq(t, f+" int-236 len", len(r), 2)
		// int-235: root.
		r = l.compositeRead("ep", []string{"/"})
		if r[1] == nil || r[3] == nil || r[16] == nil {
			t.Fatalf("root composite %v", r)
		}
	}
	_, v := e.raw("GET", "/clients/ep/composite?pathformat=SENML_CBOR&nodeformat=SENML_CBOR&paths=/3/0/11/0,/3339/0/5522,/1/0/8", "")
	golden(t, "partial composite", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"/3/0/11/0":{"id":0,"kind":"resourceInstance","type":"INTEGER","value":"0"}}}`)
	_, v = e.raw("GET", "/clients/ep/composite?pathformat=SENML_CBOR&nodeformat=SENML_CBOR&paths=/16", "")
	golden(t, "object composite", v, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"/16":{"id":16,"kind":"obj","instances":[{"id":0,"kind":"instance","resources":[
		{"id":0,"kind":"multiResource","type":"STRING","values":{"0":"Host Device ID #1"}}]}]}}}`)
}

func TestObserveAndNotifications(t *testing.T) {
	e := newEnv(t, nil)
	l := e.l
	c := e.device(testclient.Config{Endpoint: "ep"})
	ev := l.getEventStream("ep", 5) // "/event?ep": malformed, unfiltered

	r := must(t)(l.observe("ep", "3/0/7"))
	golden(t, "observe", r, `{"status":"CONTENT(205)","valid":true,"success":true,"failure":false,"content":
		{"id":7,"kind":"multiResource","type":"INTEGER","values":{"0":"3800","1":"5000"}}}`)
	c.Set(mp("/3/0/7/0"), lwm2m.Integer(3000))
	n := must(t)(ev.nextEvent("NOTIFICATION"))
	eq(t, "notification", n.(D)[3].(D)[0].(D)[7].(D)[0], 3000) // int-301

	// int-302: passive cancel forgets; the next notification gets a Reset.
	eq(t, "passive cancel", must(t)(l.passiveCancelObserve("ep", "3/0/7")), nil)
	c.Set(mp("/3/0/7/0"), lwm2m.Integer(3500))
	waitFor(t, "client observer reset", func() bool { return c.Observers() == 0 })

	// int-303: active cancel; with no observation it is HTTP 404 (raises).
	if _, err := l.cancelObserve("ep", "3/0/7"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("active cancel without observation: %v", err)
	}
	eq(t, "observe /3/0/6", status(t, must(t)(l.observe("ep", "3/0/6"))), "CONTENT(205)")
	waitFor(t, "observe registered", func() bool { return c.Observers() == 1 })
	if s := status(t, must(t)(l.cancelObserve("ep", "3/0/6"))); s != "CONTENT(205)" {
		t.Fatalf("active cancel: %s", s)
	}
	eq(t, "server observations", len(e.srv.Observations("ep")), 0)
	eq(t, "client observations", c.Observers(), 0)

	// int-304/305: composite observe, a composite notification, cancel by
	// the exact (ordered) path list.
	paths := []string{"/1/0/1", "/3/0/11/0", "/3/0/16"}
	obs := l.compositeObserve("ep", paths)
	eq(t, "composite observe", obs, D{1: D{0: D{1: 86400}}, 3: D{0: D{11: D{0: 0}, 16: "U"}}})
	c.Set(mp("/1/0/1"), lwm2m.Integer(61))
	n = must(t)(ev.nextEvent("NOTIFICATION"))
	eq(t, "composite notification", n, D{1: D{0: D{1: 61}}, 3: D{0: D{11: D{0: 0}, 16: "U"}}})
	if _, err := l.cancelCompositeObserve("ep", []string{"/3/0/16", "/1/0/1", "/3/0/11/0"}); err == nil {
		t.Fatal("cancel with a reordered path list matched")
	}
	if s := status(t, must(t)(l.cancelCompositeObserve("ep", paths))); s != "CONTENT(205)" {
		t.Fatalf("composite cancel: %s", s)
	}
	eq(t, "composite cancelled", len(e.srv.Observations("ep")), 0)
}

func waitFor(t *testing.T, what string, f func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSendEvent(t *testing.T) {
	e := newEnv(t, nil)
	c := e.device(testclient.Config{Endpoint: "ep"})
	ev := e.l.getEventStream("ep", 5)
	nodes := []lwm2m.Node{
		lwm2m.ValueNode(mp("/1/0/1"), lwm2m.Integer(86400)),
		lwm2m.ValueNode(mp("/3/0/11/0"), lwm2m.Integer(0)),
		lwm2m.ValueNode(mp("/3/0/0"), lwm2m.String("Zephyr")),
	}
	if r, err := c.Send(e.ctx, nodes, lwm2m.FormatSenMLCBOR); err != nil || r.Code != codes.Changed {
		t.Fatalf("send: %v %v", r, err)
	}
	d := must(t)(ev.nextEvent("SEND"))
	eq(t, "send", d, D{3: D{0: D{0: "Zephyr", 11: D{0: 0}}}, 1: D{0: D{1: 86400}}}) // int-311
}

// rawStream reads SSE bytes from path until want appears.
func rawStream(t *testing.T, e *env, path string, trigger func(), want ...string) string {
	t.Helper()
	resp, err := http.Get(e.http.URL + "/api" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	got := make(chan string)
	go func() {
		var b strings.Builder
		br := bufio.NewReader(resp.Body)
		for {
			c, err := br.ReadByte()
			if err != nil {
				return
			}
			b.WriteByte(c)
			done := true
			for _, w := range want {
				done = done && strings.Contains(b.String(), w)
			}
			if done {
				got <- b.String()
				return
			}
		}
	}()
	trigger()
	select {
	case s := <-got:
		return s
	case <-time.After(5 * time.Second):
		t.Fatalf("stream never carried %q", want)
	}
	return ""
}

func TestEventStreamFraming(t *testing.T) {
	e := newEnv(t, nil)
	// Jetty framing with CRLF, one-line JSON; and the CRLF heartbeat.
	s := rawStream(t, e, "/event?client_1", func() { e.device(testclient.Config{Endpoint: "client_1"}) },
		"event: REGISTRATION\r\ndata: {", "}\r\n\r\n", "\r\n\r\n\r\n")
	i := strings.Index(s, "event: REGISTRATION\r\ndata: ")
	line := s[i+len("event: REGISTRATION\r\ndata: "):]
	line = line[:strings.Index(line, "\r\n")]
	var reg map[string]any
	if err := json.Unmarshal([]byte(line), &reg); err != nil || reg["endpoint"] != "client_1" {
		t.Fatalf("registration event %q: %v", line, err)
	}

	// The ep filter: a stream for another endpoint sees heartbeats only.
	s = rawStream(t, e, "/event?ep=other", func() { e.device(testclient.Config{Endpoint: "client_2"}) }, "\r\n\r\n\r\n")
	if strings.Contains(s, "event:") {
		t.Fatalf("filtered stream got %q", s)
	}
}

func TestEventStreamHeartbeat(t *testing.T) {
	// With no events, next_event must return None at its timeout: that
	// needs bytes on the wire, or requests raises ConnectionError.
	e := newEnv(t, func(_ *server.Config, o *Options) { o.Heartbeat = 300 * time.Millisecond })
	ev := e.l.getEventStream("nobody", 1)
	start := time.Now()
	v, err := ev.nextEvent("NOTIFICATION")
	if err != nil || v != nil {
		t.Fatalf("got %v, %v; want None", v, err)
	}
	if d := time.Since(start); d < time.Second || d > 3*time.Second {
		t.Fatalf("returned after %v", d)
	}
	// Without a heartbeat inside the read timeout the harness would crash.
	e2 := newEnv(t, func(_ *server.Config, o *Options) { o.Heartbeat = time.Hour })
	if _, err := e2.l.getEventStream("nobody", 1).nextEvent("NOTIFICATION"); err == nil {
		t.Fatal("port does not model the read timeout")
	}
}

func TestQueueModeBlocks(t *testing.T) {
	// Spec C3: a request to a sleeping client waits for it, then answers
	// with the device's status; Leshan's {"delayed":true} never appears.
	e := newEnv(t, func(c *server.Config, _ *Options) { c.QueueAwake = 300 * time.Millisecond })
	c := e.device(testclient.Config{Endpoint: "q", Queue: true})
	waitFor(t, "client asleep", func() bool { return !e.srv.Awake("q") })
	reg := must(t)(e.l.get("/clients/q")).(map[string]any)
	eq(t, "queuemode", reg["queuemode"], true)
	eq(t, "sleeping", reg["sleeping"], true)

	done := make(chan any, 1)
	go func() {
		v, err := e.l.get("/clients/q/3/0/0")
		if err != nil {
			v = err
		}
		done <- v
	}()
	time.Sleep(200 * time.Millisecond)
	select {
	case v := <-done:
		t.Fatalf("answered while asleep: %v", v)
	default:
	}
	if _, err := c.Update(e.ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	v := <-done
	m, ok := v.(map[string]any)
	if !ok || m["status"] != "CONTENT(205)" || m["delayed"] != nil {
		t.Fatalf("queued read: %v", v)
	}

	// Still asleep at the request timeout: HTTP 504, as Leshan's timeout.
	waitFor(t, "client asleep", func() bool { return !e.srv.Awake("q") })
	if code, v := e.raw("GET", "/clients/q/3/0/0?timeout=1", ""); code != http.StatusGatewayTimeout || v != "Request timeout" {
		t.Fatalf("timeout: %d %v", code, v)
	}
}

func TestStatusAndFloat(t *testing.T) {
	for c, want := range map[codes.Code]string{codes.Content: "CONTENT(205)", codes.Created: "CREATED(201)",
		codes.Unauthorized: "UNAUTHORIZED(401)", codes.Code(137): "UNKNOWN(409)", codes.Continue: "UNKNOWN(231)"} {
		eq(t, "status", statusString(c), want)
	}
	for f, want := range map[float64]string{48.5: "48.5", 1: "1.0", 0: "0.0", -2.25: "-2.25", 1e7: "1.0E7",
		123456789: "1.23456789E8", 1.5e-5: "1.5E-5", 0.001: "0.001"} {
		eq(t, "javaDouble", javaDouble(f), want)
	}
}

// sseEvents yields (name, data) pairs from a raw stream.
func sseEvents(t *testing.T, e *env, path string) <-chan [2]string {
	t.Helper()
	resp, err := http.Get(e.http.URL + "/api" + path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	ch := make(chan [2]string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		name := ""
		for sc.Scan() {
			line := strings.TrimSuffix(sc.Text(), "\r")
			if n, ok := strings.CutPrefix(line, "event: "); ok {
				name = n
			} else if d, ok := strings.CutPrefix(line, "data: "); ok {
				ch <- [2]string{name, d}
			}
		}
	}()
	return ch
}

func nextSSE(t *testing.T, ch <-chan [2]string, name string) any {
	t.Helper()
	for end := time.After(5 * time.Second); ; {
		select {
		case ev := <-ch:
			if ev[0] == name {
				var v any
				if err := json.Unmarshal([]byte(ev[1]), &v); err != nil {
					t.Fatalf("%s data %q: %v", name, ev[1], err)
				}
				return v
			}
		case <-end:
			t.Fatalf("no %s event", name)
		}
	}
}

func TestEventGolden(t *testing.T) {
	e := newEnv(t, nil)
	ch := sseEvents(t, e, "/event")
	c := e.device(testclient.Config{Endpoint: "ep"})
	reg := nextSSE(t, ch, "REGISTRATION").(map[string]any)
	eq(t, "registration event", reg["registrationId"], c.Location())

	must(t)(e.l.observe("ep", "3/0/16"))
	c.Set(mp("/3/0/16"), lwm2m.String("UQ"))
	golden(t, "single notification", nextSSE(t, ch, "NOTIFICATION"),
		`{"ep":"ep","kind":"single","res":"/3/0/16","val":{"id":16,"kind":"singleResource","type":"STRING","value":"UQ"}}`)

	e.l.compositeObserve("ep", []string{"/1/0/1", "/3339/0/5522"})
	c.Set(mp("/1/0/1"), lwm2m.Integer(70))
	golden(t, "composite notification", nextSSE(t, ch, "NOTIFICATION"),
		`{"ep":"ep","kind":"composite","val":{"/1/0/1":{"id":1,"kind":"singleResource","type":"INTEGER","value":"70"}},"paths":["/1/0/1","/3339/0/5522"]}`)

	if _, err := c.Send(e.ctx, []lwm2m.Node{lwm2m.ValueNode(mp("/3/0/0"), lwm2m.String("Zephyr")),
		lwm2m.ValueNode(mp("/3/0/11/0"), lwm2m.Integer(0))}, lwm2m.FormatSenMLJSON); err != nil {
		t.Fatal(err)
	}
	golden(t, "send", nextSSE(t, ch, "SEND"), `{"ep":"ep","val":{
		"/3/0/0":{"id":0,"kind":"singleResource","type":"STRING","value":"Zephyr"},
		"/3/0/11/0":{"id":0,"kind":"resourceInstance","type":"INTEGER","value":"0"}}}`)

	if _, err := c.Deregister(e.ctx); err != nil {
		t.Fatal(err)
	}
	eq(t, "deregistration event", nextSSE(t, ch, "DEREGISTRATION").(map[string]any)["endpoint"], "ep")
}
