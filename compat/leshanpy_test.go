package compat

// A Go port of the parts of zephyr tests/net/lib/lwm2m/interop/pytest/leshan.py
// that build requests and parse responses. Tests drive the REST API through
// it, so passing tests mean the harness would read our output the same way.
// Python values map to Go as: int -> int, bool -> bool, str -> string,
// bytes -> []byte, datetime -> time.Time, dict with int keys -> D.

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// D is a Python dict keyed by ints (decoded objects/instances/resources).
type D = map[int]any

// kv is one ordered Python dict entry (requests keeps dict order).
type kv struct {
	k string
	v any
}

type leshan struct {
	t       *testing.T
	apiURL  string
	timeout int
	format  string // "" is Python None
	s       *http.Client
}

// newLeshan is Leshan.__init__: GET /security/clients must be a list.
func newLeshan(t *testing.T, u string) *leshan {
	l := &leshan{t: t, apiURL: u, timeout: 10, format: "SENML_CBOR", s: &http.Client{}}
	resp, err := l.get("/security/clients")
	if err != nil {
		t.Fatalf("Leshan not responding: %v", err)
	}
	if _, ok := resp.([]any); !ok {
		t.Fatalf("Did not receive list of endpoints: %#v", resp)
	}
	return l
}

// pyLoads is json.loads: integral numbers become int.
func pyLoads(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return pyConv(v), nil
}

func pyConv(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return int(i)
		}
		f, _ := t.Float64()
		return f
	case map[string]any:
		for k, x := range t {
			t[k] = pyConv(x)
		}
	case []any:
		for i, x := range t {
			t[i] = pyConv(x)
		}
	}
	return v
}

// pyDumps is json.dumps for the values the harness builds.
func pyDumps(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// handleResponse: non-2xx raises; an empty body is None.
func handleResponse(resp *http.Response, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 || resp.StatusCode < 200 {
		return nil, fmt.Errorf("Error %d: %s", resp.StatusCode, b)
	}
	if len(b) == 0 {
		return nil, nil
	}
	return pyLoads(b)
}

func (l *leshan) do(method, u, body string, headers map[string]string) (any, error) {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c := *l.s
	c.Timeout = time.Duration(l.timeout) * time.Second
	return handleResponse(c.Do(req))
}

func query(params []kv) string {
	var parts []string
	for _, p := range params {
		parts = append(parts, url.QueryEscape(p.k)+"="+url.QueryEscape(fmt.Sprint(p.v)))
	}
	return strings.Join(parts, "&")
}

func (l *leshan) get(path string) (any, error) {
	params := []kv{{"timeout", l.timeout}}
	if l.format != "" {
		params = append(params, kv{"format", l.format})
	}
	return l.do("GET", l.apiURL+path+"?"+query(params), "", nil)
}

func (l *leshan) putRaw(path, data string, headers map[string]string, params []kv) (any, error) {
	u := l.apiURL + path
	if len(params) > 0 {
		u += "?" + query(params)
	}
	return l.do("PUT", u, data, headers)
}

func (l *leshan) put(path string, data any, uriOptions string) (any, error) {
	s, ok := data.(string)
	if !ok {
		s = pyDumps(data)
	}
	return l.putRaw(fmt.Sprintf("%s?timeout=%d&format=%s%s", path, l.timeout, l.format, uriOptions), s,
		map[string]string{"content-type": "application/json"}, nil)
}

// post: data nil sends no params, no content-type and no body.
func (l *leshan) post(path string, data any) (any, error) {
	if data == nil {
		return l.do("POST", l.apiURL+path, "", nil)
	}
	s, ok := data.(string)
	if !ok {
		s = pyDumps(data)
	}
	return l.do("POST", fmt.Sprintf("%s%s?timeout=%d&format=%s", l.apiURL, path, l.timeout, l.format), s,
		map[string]string{"content-type": "application/json"})
}

func (l *leshan) deleteRaw(path string) (any, error) { return l.do("DELETE", l.apiURL+path, "", nil) }

func (l *leshan) delete(ep, path string) (any, error) {
	return l.deleteRaw("/clients/" + ep + "/" + path)
}
func (l *leshan) execute(ep, path string) (any, error) {
	return l.post("/clients/"+ep+"/"+path, nil)
}

func (l *leshan) write(ep, path string, value any) (any, error) {
	kind := "resourceInstance"
	if len(strings.Split(path, "/")) == 3 {
		kind = "singleResource"
	}
	segs := strings.Split(path, "/")
	return l.put("/clients/"+ep+"/"+path, defineResource(segs[len(segs)-1], value, kind), "")
}

func (l *leshan) writeAttributes(ep, path string, attrs []kv) (any, error) {
	return l.putRaw("/clients/"+ep+"/"+path+"/attributes", "", nil, attrs)
}

func (l *leshan) removeAttributes(ep, path string, names []string) (any, error) {
	return l.putRaw("/clients/"+ep+"/"+path+"/attributes?"+strings.Join(names, "&"), "", nil, nil)
}

func (l *leshan) updateObjInstance(ep, path string, res []kv) (any, error) {
	return l.put("/clients/"+ep+"/"+path, defineObjInst(path, res), "&replace=false")
}

func (l *leshan) replaceObjInstance(ep, path string, res []kv) (any, error) {
	return l.put("/clients/"+ep+"/"+path, defineObjInst(path, res), "&replace=true")
}

func (l *leshan) createObjInstance(ep, path string, res []kv) (any, error) {
	data := defineObjInst(path, res)
	segs := strings.Split(path, "/")
	return l.post("/clients/"+ep+"/"+strings.Join(segs[:len(segs)-1], "/"), data)
}

func typeToString(v any) string {
	switch v.(type) {
	case bool:
		return "boolean"
	case int:
		return "integer"
	case time.Time:
		return "time"
	case []byte:
		return "opaque"
	}
	return "string"
}

func convertType(v any) any {
	switch t := v.(type) {
	case time.Time:
		return int(t.Unix())
	case []byte:
		return hex.EncodeToString(t)
	}
	return v
}

// orderedMap marshals as a JSON object in insertion order, like json.dumps.
type orderedMap []kv

func (m orderedMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, e := range m {
		if i > 0 {
			b.WriteString(", ")
		}
		k, _ := json.Marshal(e.k)
		v, err := json.Marshal(e.v)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteString(": ")
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func defineObjInst(path string, res []kv) orderedMap {
	segs := strings.Split(path, "/")
	id, _ := strconv.Atoi(segs[len(segs)-1])
	var resources []any
	for _, r := range res {
		kind := "singleResource"
		if _, ok := r.v.([]kv); ok {
			kind = "multiResource"
		}
		k, _ := strconv.Atoi(r.k)
		resources = append(resources, defineResource(k, r.v, kind))
	}
	return orderedMap{{"kind", "instance"}, {"id", id}, {"resources", resources}}
}

func defineResource(rid any, value any, kind string) orderedMap {
	switch kind {
	case "singleResource", "resourceInstance":
		return orderedMap{{"id", rid}, {"kind", kind}, {"value", convertType(value)}, {"type", typeToString(value)}}
	case "multiResource":
		vals := value.([]kv)
		return orderedMap{{"id", rid}, {"kind", kind}, {"values", orderedMap(vals)}, {"type", typeToString(vals[0].v)}}
	}
	panic("Unhandled type " + kind)
}

// --- decoding --------------------------------------------------------------

func (l *leshan) fatalf(f string, a ...any) {
	l.t.Helper()
	l.t.Fatalf("leshan.py would raise: "+f, a...)
}

func pyTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case float64:
		return t != 0
	case map[string]any:
		return len(t) > 0
	case []any:
		return len(t) > 0
	}
	return true
}

func (l *leshan) pyInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			l.fatalf("int(%q)", t)
		}
		return n
	}
	l.fatalf("int(%#v)", v)
	return 0
}

func (l *leshan) decodeValue(typ any, value any) any {
	switch typ {
	case "BOOLEAN":
		return pyTruthy(value)
	case "INTEGER":
		return l.pyInt(value)
	}
	return value
}

func (l *leshan) field(m any, k string) any {
	l.t.Helper()
	d, ok := m.(map[string]any)
	if !ok {
		l.fatalf("TypeError: %#v is not a dict", m)
	}
	v, ok := d[k]
	if !ok {
		l.fatalf("KeyError: %q in %#v", k, m)
	}
	return v
}

func (l *leshan) list(v any) []any {
	s, ok := v.([]any)
	if !ok {
		l.fatalf("TypeError: %#v is not iterable", v)
	}
	return s
}

func (l *leshan) decodeResource(c any) D {
	switch kind := l.field(c, "kind"); kind {
	case "singleResource", "resourceInstance":
		return D{l.pyInt(l.field(c, "id")): l.decodeValue(l.field(c, "type"), l.field(c, "value"))}
	case "multiResource":
		vals, ok := l.field(c, "values").(map[string]any)
		if !ok {
			l.fatalf("values is not a dict")
		}
		out := D{}
		for riid, v := range vals {
			out[l.pyInt(riid)] = l.decodeValue(l.field(c, "type"), v)
		}
		return D{l.pyInt(l.field(c, "id")): out}
	default:
		l.fatalf("Unhandled type %v", kind)
	}
	return nil
}

func (l *leshan) decodeObjInst(c any) D {
	res := D{}
	for _, r := range l.list(l.field(c, "resources")) {
		for k, v := range l.decodeResource(r) {
			res[k] = v
		}
	}
	return D{l.pyInt(l.field(c, "id")): res}
}

func (l *leshan) decodeObj(c any) D {
	insts := D{}
	for _, i := range l.list(l.field(c, "instances")) {
		for k, v := range l.decodeObjInst(i) {
			insts[k] = v
		}
	}
	return D{l.pyInt(l.field(c, "id")): insts}
}

// read returns the decoded content, or the whole response if !success.
func (l *leshan) read(ep, path string) any {
	l.t.Helper()
	resp, err := l.get("/clients/" + ep + "/" + path)
	if err != nil {
		l.fatalf("%v", err)
	}
	if !pyTruthy(l.field(resp, "success")) {
		return resp
	}
	content := l.field(resp, "content")
	switch kind := l.field(content, "kind"); kind {
	case "obj":
		return l.decodeObj(content)
	case "instance":
		return l.decodeObjInst(content)
	case "singleResource", "resourceInstance":
		return l.decodeValue(l.field(content, "type"), l.field(content, "value"))
	case "multiResource":
		return l.decodeResource(content)
	default:
		l.fatalf("Unhandled type %v", kind)
	}
	return nil
}

func subdict(d D, k int) D {
	if _, ok := d[k]; !ok {
		d[k] = D{}
	}
	return d[k].(D)
}

func (l *leshan) parseComposite(payload any) D {
	l.t.Helper()
	data := D{}
	m, ok := payload.(map[string]any)
	if !ok {
		l.fatalf("payload %#v is not a dict", payload)
	}
	if _, ok := m["status"]; ok {
		if _, has := m["content"]; m["status"] != "CONTENT(205)" || !has {
			l.fatalf("No content received: %v", m)
		}
		m, ok = m["content"].(map[string]any)
		if !ok {
			l.fatalf("content is not a dict")
		}
	}
	for path, content := range m {
		if path == "/" {
			for _, o := range l.list(l.field(content, "objects")) {
				for k, v := range l.decodeObj(o) {
					data[k] = v
				}
			}
			continue
		}
		var keys []int
		for _, s := range strings.Split(strings.TrimLeft(path, "/"), "/") {
			keys = append(keys, l.pyInt(s))
		}
		switch len(keys) {
		case 1:
			for k, v := range l.decodeObj(content) {
				data[k] = v
			}
		case 2:
			for k, v := range l.decodeObjInst(content) {
				subdict(data, keys[0])[k] = v
			}
		case 3:
			for k, v := range l.decodeResource(content) {
				subdict(subdict(data, keys[0]), keys[1])[k] = v
			}
		case 4:
			for k, v := range l.decodeResource(content) {
				subdict(subdict(subdict(data, keys[0]), keys[1]), keys[2])[k] = v
			}
		default:
			l.fatalf("Unhandled path %s", path)
		}
	}
	return data
}

func (l *leshan) compositeParams(paths []string) []kv {
	p := []kv{{"pathformat", l.format}, {"nodeformat", l.format}, {"timeout", l.timeout}}
	if paths != nil {
		var ps []string
		for _, s := range paths {
			if !strings.HasPrefix(s, "/") {
				s = "/" + s
			}
			ps = append(ps, s)
		}
		p = append(p, kv{"paths", strings.Join(ps, ",")})
	}
	return p
}

func (l *leshan) compositeRead(ep string, paths []string) D {
	l.t.Helper()
	resp, err := l.do("GET", l.apiURL+"/clients/"+ep+"/composite?"+query(l.compositeParams(paths)), "", nil)
	if err != nil {
		l.fatalf("%v", err)
	}
	return l.parseComposite(resp)
}

func (l *leshan) compositeWrite(ep string, resources []kv) (any, error) {
	var data orderedMap
	for _, r := range resources {
		path := r.k
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		segs := strings.Split(path, "/")
		level := len(segs) - 1
		rid, _ := strconv.Atoi(segs[len(segs)-1])
		var v orderedMap
		switch level {
		case 3:
			if _, ok := r.v.([]kv); ok {
				v = defineResource(rid, r.v, "multiResource")
			} else {
				v = defineResource(rid, r.v, "singleResource")
			}
		case 4:
			v = defineResource(rid, r.v, "resourceInstance")
		default:
			l.fatalf("Unhandled path %s", path)
		}
		data = append(data, kv{path, v})
	}
	return l.do("PUT", l.apiURL+"/clients/"+ep+"/composite?"+query(l.compositeParams(nil)), pyDumps(data),
		map[string]string{"Content-Type": "application/json"})
}

func (l *leshan) discover(ep, path string) map[string]any {
	l.t.Helper()
	resp, err := l.do("GET", l.apiURL+"/clients/"+ep+"/"+path+"/discover", "", nil)
	if err != nil {
		l.fatalf("%v", err)
	}
	data := map[string]any{}
	for _, o := range l.list(l.field(resp, "objectLinks")) {
		data[l.field(o, "url").(string)] = l.field(o, "attributes")
	}
	return data
}

func (l *leshan) createPSKDevice(ep, passwd string) (any, error) {
	psk := hex.EncodeToString([]byte(passwd))
	return l.put("/security/clients/", fmt.Sprintf(`{"endpoint":"%s","tls":{"mode":"psk","details":{"identity":"%s","key":"%s"} } }`, ep, ep, psk), "")
}

func (l *leshan) deleteDevice(ep string) (any, error) { return l.deleteRaw("/security/clients/" + ep) }

func (l *leshan) observe(ep, path string) (any, error) {
	return l.post("/clients/"+ep+"/"+path+"/observe", "")
}

func (l *leshan) cancelObserve(ep, path string) (any, error) {
	return l.deleteRaw("/clients/" + ep + "/" + path + "/observe?active")
}

func (l *leshan) passiveCancelObserve(ep, path string) (any, error) {
	return l.deleteRaw("/clients/" + ep + "/" + path + "/observe")
}

func slashed(paths []string) string {
	var ps []string
	for _, p := range paths {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		ps = append(ps, p)
	}
	return strings.Join(ps, ",")
}

func (l *leshan) compositeObserve(ep string, paths []string) D {
	l.t.Helper()
	resp, err := l.do("POST", l.apiURL+"/clients/"+ep+"/composite/observe?"+query(l.compositeParams(paths)), "", nil)
	if err != nil {
		l.fatalf("%v", err)
	}
	return l.parseComposite(resp)
}

func (l *leshan) cancelCompositeObserve(ep string, paths []string) (any, error) {
	return l.deleteRaw("/clients/" + ep + "/composite/observe?paths=" + slashed(paths) + "&active")
}

// --- event stream ----------------------------------------------------------

// events is LeshanEventsIterator over requests' iter_lines(chunk_size=1).
type events struct {
	l       *leshan
	body    io.ReadCloser
	bytes   chan byte
	timeout time.Duration
	pending *string
	queue   []string
}

// getEventStream opens "/event?<ep>" (the harness's malformed query).
func (l *leshan) getEventStream(ep string, timeout int) *events {
	l.t.Helper()
	req, _ := http.NewRequest("GET", l.apiURL+"/event?"+ep, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.fatalf("%v", err)
	}
	e := &events{l: l, body: resp.Body, bytes: make(chan byte, 1<<16), timeout: time.Duration(timeout) * time.Second}
	go func() {
		r := bufio.NewReader(resp.Body)
		for {
			b, err := r.ReadByte()
			if err != nil {
				close(e.bytes)
				return
			}
			e.bytes <- b
		}
	}()
	l.t.Cleanup(func() { resp.Body.Close() })
	return e
}

func (e *events) close() { e.body.Close() }

// splitlines is str.splitlines for \r, \n and \r\n.
func splitlines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' || s[i] == '\n' {
			out = append(out, s[start:i])
			if s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// nextLine follows requests.Response.iter_lines; a read that sees no byte
// for the stream timeout raises ConnectionError, as requests does.
func (e *events) nextLine() (string, error) {
	for len(e.queue) == 0 {
		var b byte
		var ok bool
		select {
		case b, ok = <-e.bytes:
			if !ok {
				return "", io.EOF
			}
		case <-time.After(e.timeout):
			return "", fmt.Errorf("ConnectionError: read timed out")
		}
		chunk := string([]byte{b})
		if e.pending != nil {
			chunk = *e.pending + chunk
		}
		lines := splitlines(chunk)
		if n := len(lines); n > 0 && lines[n-1] != "" && lines[n-1][len(lines[n-1])-1] == chunk[len(chunk)-1] {
			p := lines[n-1]
			e.pending = &p
			lines = lines[:n-1]
		} else {
			e.pending = nil
		}
		e.queue = append(e.queue, lines...)
	}
	line := e.queue[0]
	e.queue = e.queue[1:]
	return line, nil
}

// nextEvent returns the parsed data of the next event, or nil on timeout.
// An error is what would escape the harness (the uncaught ConnectionError).
func (e *events) nextEvent(event string) (any, error) {
	deadline := time.Now().Add(e.timeout)
	for {
		line, err := e.nextLine()
		if err != nil {
			return nil, err
		}
		if line == "event: "+event {
			for {
				line, err := e.nextLine()
				if err != nil {
					return nil, err
				}
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data, err := pyLoads([]byte(strings.TrimPrefix(line, "data: ")))
				if err != nil {
					return nil, err
				}
				if event == "SEND" || (event == "NOTIFICATION" && e.l.field(data, "kind") == "composite") {
					return e.l.parseComposite(e.l.field(data, "val")), nil
				}
				if event == "NOTIFICATION" {
					return e.l.parseComposite(map[string]any{e.l.field(data, "res").(string): e.l.field(data, "val")}), nil
				}
				return data, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, nil
		}
	}
}
