package coap_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/apiv1"
	_ "github.com/fiumaralabs/lwm2m/codec/all"
	"github.com/fiumaralabs/lwm2m/model"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	piondtls "github.com/pion/dtls/v4"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestAPIPSKRegisterAndRevoke(t *testing.T) {
	models := server.NewModels(model.Default())
	var binding *coap.Binding
	security := coap.NewRevokingSecurityStore(server.NewMemorySecurityStore(), func(id string) { binding.RevokePSKIdentity(id) })
	srv := server.New(server.Config{Security: security, Now: func() time.Time { return time.Unix(1_700_000_000, 0) }, ExpiryCheck: time.Hour, RequestTimeout: 5 * time.Second, Schema: models.Schema, Validator: models})
	binding = coap.New(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(func() { cancel(); _ = binding.Close(); _ = srv.Close() })
	sum := sha256.Sum256([]byte("lwm_test_secret"))
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write\n", sum)))
	if err != nil {
		t.Fatal(err)
	}
	api := apiv1.New(apiv1.Options{Server: srv, Keys: keys})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer lwm_test_secret")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		return w
	}
	if w := request(http.MethodPost, "/v1/devices", `{"endpoint":"api-device"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(http.MethodPut, "/v1/devices/api-device/credentials", `{"psk":{"identity":"api-id","key":"hex:30313233343536373839616263646566"}}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	dc, err := binding.DTLSConfig(coap.CertificateModes{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := binding.ListenDTLS("127.0.0.1:0", dc)
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.String()
	client := testclient.New(testclient.Config{Endpoint: "api-device"})
	client.Set(lwm2m.MustParsePath("/1/0/0"), lwm2m.Integer(1))
	client.Set(lwm2m.MustParsePath("/1/0/1"), lwm2m.Integer(86400))
	client.Set(lwm2m.MustParsePath("/1/0/7"), lwm2m.String("U"))
	client.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("Open Mobile Alliance"))
	client.Set(lwm2m.MustParsePath("/3/0/1"), lwm2m.String("Lightweight M2M Client"))
	psk := testclient.PSKConfig("api-id", []byte("0123456789abcdef"))
	if err := client.DialDTLS(addr, psk...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if r, err := client.Register(ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("register: %v %v", r, err)
	}
	if w := request(http.MethodDelete, "/v1/devices/api-device/credentials", ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, ok := srv.Store().ByEndpoint("api-device"); ok {
		t.Fatal("registration survived revocation")
	}
	ctx1, cancel1 := context.WithTimeout(ctx, 3*time.Second)
	defer cancel1()
	if r, err := client.Register(ctx1); err == nil && r.Code == codes.Created {
		t.Fatal("revoked live session registered")
	}
	peer, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := piondtls.Dial("udp", peer, psk...)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx2, cancel2 := context.WithTimeout(ctx, 3*time.Second)
	defer cancel2()
	if err = conn.HandshakeContext(ctx2); err == nil {
		t.Fatal("revoked PSK authenticated")
	}
}
