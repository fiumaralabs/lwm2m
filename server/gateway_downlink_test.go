package server_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestGatewayPrefixDownlinks(t *testing.T) {
	h := newHarness(t)
	gw := gatewayClient(h, "gw")
	h.registered("plain")
	gw.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
		if r.Path == "/25" {
			return 0, nil, nil, false
		}
		return codes.Changed, nil, nil, true
	})
	for _, tc := range []struct {
		name, path string
		call       func(string, string) (*server.Response, error)
	}{
		{"discover", "/d01/3/0", func(ep, prefix string) (*server.Response, error) {
			return h.srv.DiscoverWithPrefix(h.ctx, ep, p("/3/0"), nil, prefix)
		}},
		{"write", "/d01/1/0/1", func(ep, prefix string) (*server.Response, error) {
			return h.srv.Write(h.ctx, ep, p("/1/0/1"), []lwm2m.Node{lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(3))}, server.WriteOptions{Prefix: prefix})
		}},
		{"execute", "/d01/3/0/4", func(ep, prefix string) (*server.Response, error) {
			return h.srv.ExecuteWithPrefix(h.ctx, ep, p("/3/0/4"), "", prefix)
		}},
		{"delete", "/d01/3303/0", func(ep, prefix string) (*server.Response, error) {
			return h.srv.DeleteWithPrefix(h.ctx, ep, p("/3303/0"), prefix)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := tc.call("gw", "d01")
			mustResp(t, r, err, "2.04")
			if req, _ := gw.LastRequest(); req.Path != tc.path {
				t.Fatalf("path = %q, want %q", req.Path, tc.path)
			}
			for _, bad := range []struct{ ep, prefix string }{{"plain", "d01"}, {"gw", "42"}, {"gw", "unknown"}} {
				if _, err := tc.call(bad.ep, bad.prefix); !errors.Is(err, server.ErrBadRequest) {
					t.Errorf("%s/%s: %v", bad.ep, bad.prefix, err)
				}
			}
			for _, req := range gw.Requests() {
				if strings.HasPrefix(req.Path, "/unknown/") {
					t.Errorf("sent request to unknown prefix: %s", req.Path)
				}
			}
			gw.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
				return codes.Forbidden, nil, nil, true
			})
			if _, err := tc.call("gw", "d01"); err == nil || errors.Is(err, server.ErrBadRequest) {
				t.Errorf("unreadable gateway table: %v", err)
			}
			if req, _ := gw.LastRequest(); req.Path != "/25" {
				t.Errorf("operation sent after registry failure: %s", req.Path)
			}
			gw.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
				if r.Path == "/25" {
					return 0, nil, nil, false
				}
				return codes.Changed, nil, nil, true
			})
		})
	}
}
