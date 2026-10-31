package apiv1

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestBootstrapGeneratedPSKRegistersOverDTLS(t *testing.T) {
	models := server.NewModels(model.Default())
	var binding *coap.Binding
	security := coap.NewRevokingSecurityStore(server.NewMemorySecurityStore(), func(id string) { binding.RevokePSKIdentity(id) })
	srv := server.New(server.Config{Security: security, ExpiryCheck: time.Hour, RequestTimeout: 5 * time.Second, Schema: models.Schema, Validator: models})
	binding = coap.New(srv)
	t.Cleanup(func() { _ = binding.Close(); _ = srv.Close() })
	keys := deviceKeys()
	c := keys.entries["test"]
	c.scopes += "bootstrap:write,"
	keys.entries["test"] = c
	h := New(Options{Server: srv, Keys: keys})
	if w := deviceRequest(h, "POST", "/v1/devices", `{"endpoint":"generated"}`); w.Code != 201 {
		t.Fatal(w.Code)
	}
	w := deviceRequest(h, "PUT", "/v1/devices/generated/bootstrap", `{"servers":[{"server":"self","shortServerId":1,"security":{"psk":{"generate":true}}}]}`)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var result struct {
		Servers []struct {
			Security struct {
				PSK struct {
					Identity string `json:"identity"`
					Key      string `json:"key"`
				} `json:"psk"`
			} `json:"security"`
		} `json:"servers"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Servers) != 1 {
		t.Fatal("invalid generated config")
	}
	p := result.Servers[0].Security.PSK
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(p.Key, "base64:"))
	if err != nil {
		t.Fatal(err)
	}
	dc, err := binding.DTLSConfig(coap.CertificateModes{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := binding.ListenDTLS("127.0.0.1:0", dc)
	if err != nil {
		t.Fatal(err)
	}
	client := testclient.New(testclient.Config{Endpoint: "generated"})
	client.Set(lwm2m.MustParsePath("/1/0/0"), lwm2m.Integer(1))
	client.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(86400))
	client.Set(lwm2m.MustParsePath("/1/0/7"), lwm2m.String("U"))
	client.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	client.Set(lwm2m.MustParsePath("/3/0/1"), lwm2m.String("Lightweight M2M Client"))
	if err = client.DialDTLS(listener.String(), testclient.PSKConfig(p.Identity, key)...); err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if r, err := client.Register(ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("register: %v %v", r, err)
	}
}
