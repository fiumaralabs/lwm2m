package coap

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

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/testclient"
	piondtls "github.com/pion/dtls/v4"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestAPIPSKRegisterAndRevoke(t *testing.T) {
	h := newHarness(t)
	sum := sha256.Sum256([]byte("lwm_test_secret"))
	keys, err := apiv1.LoadKeys(strings.NewReader(fmt.Sprintf("key_test sha256:%x devices:read,devices:write\n", sum)))
	if err != nil {
		t.Fatal(err)
	}
	api := apiv1.New(apiv1.Options{Server: h.srv, Keys: keys})
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
	addr := h.listenSecure(CertificateModes{})
	client := h.mustRegisterSecure(testclient.Config{Endpoint: "api-device"}, addr, pskConfig("api-id", []byte("0123456789abcdef")))
	if w := request(http.MethodDelete, "/v1/devices/api-device/credentials", ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, ok := h.srv.Store().ByEndpoint("api-device"); ok {
		t.Fatal("registration survived revocation")
	}
	ctx, cancel := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel()
	if r, err := client.Register(ctx); err == nil && r.Code == codes.Created {
		t.Fatal("revoked live session registered")
	}
	peer, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := piondtls.Dial("udp", peer, pskConfig("api-id", []byte("0123456789abcdef"))...)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx2, cancel2 := context.WithTimeout(h.ctx, 3*time.Second)
	defer cancel2()
	if err = conn.HandshakeContext(ctx2); err == nil {
		t.Fatal("revoked PSK authenticated")
	}
}
