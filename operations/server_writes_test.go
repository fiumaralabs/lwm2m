package operations_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestExecutorServerWrites(t *testing.T) {
	for _, tc := range []struct {
		kind, body, path string
		value            lwm2m.Value
	}{
		{"set-lifetime", `{"lifetimeSeconds":300}`, "/1/3/1", lwm2m.Integer(300)},
		{"set-binding", `{"binding":"UQ"}`, "/1/3/7", lwm2m.String("UQ")},
		{"set-preferred-transport", `{"binding":"U"}`, "/1/3/22", lwm2m.String("U")},
		{"set-profile-hash-algorithm", `{"suite":6}`, "/1/3/27", lwm2m.Unsigned(6)},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			h := setup(t, operations.ExecutorOptions{}, func(c *server.Config) { c.ShortServerID = 101 })
			c := h.register(t, testclient.Config{Version: "1.2"})
			c.Set(lwm2m.MustParsePath("/1/0/0"), lwm2m.Integer(5))
			c.Set(lwm2m.MustParsePath("/1/3/0"), lwm2m.Integer(101))
			o := h.submit(t, tc.kind, tc.body)
			gotOp, err := h.e.Wait(h.ctx, "t", o.ID)
			if err != nil || gotOp.Status != operations.Succeeded {
				t.Fatalf("status %s error %+v wait %v", gotOp.Status, gotOp.Error, err)
			}
			req := c.Requests()
			if len(req) != 2 || req[0].Code != codes.GET || req[0].Path != "/1" || req[1].Path != tc.path || (req[1].Code != codes.PUT && req[1].Code != codes.POST) {
				t.Fatalf("requests: %+v", req)
			}
			got, ok := c.Get(lwm2m.MustParsePath(tc.path))
			if !ok || !reflect.DeepEqual(got, tc.value) {
				t.Fatalf("got %+v, %v; want %+v", got, ok, tc.value)
			}
		})
	}
}

func TestExecutorServerWritesInvalid(t *testing.T) {
	h := setup(t, operations.ExecutorOptions{})
	for _, tc := range []struct{ kind, body string }{
		{"set-lifetime", `{}`}, {"set-lifetime", `{"lifetimeSeconds":-1}`}, {"set-lifetime", `{"lifetimeSeconds":4294967296}`}, {"set-lifetime", `{"lifetimeSeconds":1.5}`},
		{"set-binding", `{}`}, {"set-binding", `{"binding":"???"}`},
		{"set-preferred-transport", `{}`}, {"set-preferred-transport", `{"binding":"UQ"}`}, {"set-preferred-transport", `{"binding":"X"}`},
		{"set-profile-hash-algorithm", `{}`}, {"set-profile-hash-algorithm", `{"suite":-1}`}, {"set-profile-hash-algorithm", `{"suite":256}`},
	} {
		t.Run(tc.kind+tc.body, func(t *testing.T) {
			_, err := h.e.Submit(h.ctx, "t", operations.New("t", "dev", tc.kind, json.RawMessage(tc.body), nil, time.Now()))
			if !errors.Is(err, operations.ErrInvalid) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
