package server_test

import (
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestForcedWriteFormatValidation(t *testing.T) {
	h := newHarness(t, func(c *server.Config) { c.Validator = nil })
	for _, tc := range []struct {
		name, version, op, path string
		format                  lwm2m.ContentFormat
		allowed                 bool
	}{
		{"write-instance-text", "1.0", "write", "/1/0", lwm2m.FormatText, false},
		{"write-instance-tlv", "1.0", "write", "/1/0", lwm2m.FormatTLV, true},
		{"write-instance-cbor-old", "1.1", "write", "/1/0", lwm2m.FormatLwM2MCBOR, false},
		{"write-instance-cbor", "1.2", "write", "/1/0", lwm2m.FormatLwM2MCBOR, true},
		{"write-resource-text", "1.1", "write", "/1/0/1", lwm2m.FormatText, true},
		{"create-text", "1.0", "create", "/1", lwm2m.FormatText, false},
		{"create-tlv", "1.0", "create", "/1", lwm2m.FormatTLV, true},
		{"create-cbor-old", "1.1", "create", "/1", lwm2m.FormatLwM2MCBOR, false},
		{"create-cbor", "1.2", "create", "/1", lwm2m.FormatLwM2MCBOR, true},
		{"composite-tlv", "1.1", "composite", "/", lwm2m.FormatTLV, false},
		{"composite-senml", "1.1", "composite", "/", lwm2m.FormatSenMLJSON, true},
		{"composite-cbor-old", "1.1", "composite", "/", lwm2m.FormatLwM2MCBOR, false},
		{"composite-cbor", "1.2", "composite", "/", lwm2m.FormatLwM2MCBOR, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := h.device(testclient.Config{Endpoint: tc.name, Version: tc.version})
			mustCode(mustRegister(h, c))
			calls := 0
			c.SetOverride(func(r testclient.Request) (codes.Code, *lwm2m.ContentFormat, []byte, bool) {
				calls++
				return codes.Changed, nil, nil, true
			})
			node := lwm2m.ValueNode(p("/1/0/1"), lwm2m.Integer(123))
			var err error
			switch tc.op {
			case "write":
				_, err = h.srv.Write(h.ctx, tc.name, p(tc.path), []lwm2m.Node{node}, server.WriteOptions{Format: &tc.format})
			case "create":
				_, err = h.srv.Create(h.ctx, tc.name, p(tc.path), []lwm2m.Node{node}, &tc.format)
			case "composite":
				_, err = h.srv.WriteComposite(h.ctx, tc.name, []lwm2m.Node{node}, &tc.format)
			}
			if !tc.allowed {
				if !errors.Is(err, server.ErrBadRequest) || calls != 0 {
					t.Fatalf("err=%v calls=%d, want bad request and no exchange", err, calls)
				}
				return
			}
			if err != nil || calls != 1 {
				t.Fatalf("err=%v calls=%d, want one exchange", err, calls)
			}
			req, ok := c.LastRequest()
			if !ok || req.Format == nil || *req.Format != tc.format {
				t.Fatalf("request: %+v", req)
			}
		})
	}
}
