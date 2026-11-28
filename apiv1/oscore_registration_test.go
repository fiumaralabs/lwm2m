package apiv1

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/security/oscore"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestOSCORERegistrationIdentity(t *testing.T) {
	srv := server.New(server.Config{})
	defer srv.Close()
	h := New(Options{Keys: deviceKeys(), Server: srv})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"pump"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	b := coap.New(srv)
	defer b.Close()
	params := oscore.Params{MasterSecret: []byte("0123456789abcdef"), IDContext: []byte{4}, SenderID: []byte{1, 2}, RecipientID: []byte("srv")}
	if err := b.EnableOSCORE().Put("pump", params.Reverse()); err != nil {
		t.Fatal(err)
	}
	addr, err := b.ListenUDP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := oscore.New(params)
	if err != nil {
		t.Fatal(err)
	}
	c := testclient.NewOSCORE(testclient.Config{Endpoint: "pump"}, ctx)
	defer c.Close()
	if err := c.Dial(addr.String()); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.Register(deadline)
	if err != nil || result.Code != codes.Created {
		t.Fatalf("register: %v %v", result, err)
	}
	for _, path := range []string{"/v1/devices/pump/registration", "/v1/devices/pump"} {
		w := deviceRequest(h, "GET", path, "")
		var body map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if path == "/v1/devices/pump" {
			if err := json.Unmarshal(body["registration"], &body); err != nil {
				t.Fatal(err)
			}
		}
		var identity map[string]any
		if err := json.Unmarshal(body["identity"], &identity); err != nil || identity["mode"] != "oscore" || identity["oscoreSenderId"] != "AQI=" {
			t.Fatal(w.Body.String())
		}
	}
}

func TestOSCOREEmptySenderIDJSON(t *testing.T) {
	o, r := registrationFixture(t)
	r.Identity = server.Identity{Mode: server.ModeOSCORE}
	w := deviceRequest(New(o), "GET", "/v1/devices/pump/registration", "")
	var body struct{ Identity map[string]any }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Identity["oscoreSenderId"] != "" {
		t.Fatal(w.Body.String())
	}
}
