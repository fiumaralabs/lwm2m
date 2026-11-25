package operations_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestExecutorDiscover(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	c.Set(lwm2m.MustParsePath("/3/0/6/0"), lwm2m.Integer(1))
	c.Set(lwm2m.MustParsePath("/3/0/6/1"), lwm2m.Integer(2))
	o := h.wait(t, h.submit(t, "discover", `{"path":"/3/0","depth":1}`), operations.Succeeded)
	var result struct {
		Code  string `json:"code"`
		Links []struct {
			Path string `json:"path"`
			Dim  *int   `json:"dim"`
		} `json:"links"`
	}
	if err := json.Unmarshal(o.Result, &result); err != nil || result.Code != "2.05" {
		t.Fatalf("result: %s, %v", o.Result, err)
	}
	found := false
	for _, l := range result.Links {
		if l.Path == "/3/0/6" && l.Dim != nil && *l.Dim == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing link with dim: %s", o.Result)
	}
	req := c.Requests()
	if len(req) != 1 || req[0].Code != codes.GET || req[0].Path != "/3/0" || len(req[0].Queries) != 1 || req[0].Queries[0] != "depth=1" || req[0].Accept == nil || *req[0].Accept != lwm2m.FormatLinkFormat {
		t.Fatalf("requests: %+v", req)
	}
}

func TestExecutorCreate(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	c := h.register(t)
	submitted := h.submit(t, "create", `{"path":"/1","values":[{"path":"/1/1/0","type":"integer","value":123},{"path":"/1/1/1","type":"integer","value":42},{"path":"/1/1/6","type":"boolean","value":false},{"path":"/1/1/7","type":"string","value":"U"}]}`)
	o, err := h.e.Wait(h.ctx, "t", submitted.ID)
	if err != nil || o.Status != operations.Succeeded {
		t.Fatalf("status=%s error=%+v wait=%v", o.Status, o.Error, err)
	}
	if !strings.Contains(string(o.Result), `"code":"2.01"`) || !strings.Contains(string(o.Result), `"location":"/1/1"`) {
		t.Fatalf("result: %s", o.Result)
	}
	req := c.Requests()
	if len(req) != 1 || req[0].Code != codes.POST || req[0].Path != "/1" || len(req[0].Body) == 0 {
		t.Fatalf("requests: %+v", req)
	}
	value, ok := c.Get(lwm2m.MustParsePath("/1/1/1"))
	if !ok || value.Int != 42 {
		t.Fatalf("created value: %+v, %v", value, ok)
	}
}

func TestExecutorDiscoverCreateInvalid(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	for _, tc := range []struct{ kind, body string }{
		{"discover", `{"path":"/3","depth":4}`},
		{"discover", `{"path":"/3","depth":-1}`},
		{"create", `{"path":"/1/0","values":[]}`},
		{"create", `{"path":"/1","values":[]}`},
	} {
		t.Run(tc.kind+tc.body, func(t *testing.T) {
			_, err := h.e.Submit(h.ctx, "t", operations.New("t", "dev", tc.kind, json.RawMessage(tc.body), nil, time.Now()))
			if !errors.Is(err, operations.ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
