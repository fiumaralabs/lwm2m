package apiv1

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestWSSRegistrationIdentity(t *testing.T) {
	caPEM, keyPEM := estTestPair(t)
	caPair, err := tls.X509KeyPair([]byte(caPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caPair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	clientCert := func(cn string, serial int64) tls.Certificate {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caPair.PrivateKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	s := server.New(server.Config{})
	defer s.Close()
	b := coap.New(s)
	defer b.Close()
	ws := httptest.NewUnstartedServer(b.WebSocketHandler())
	ws.TLS = &tls.Config{ClientAuth: tls.VerifyClientCertIfGiven, ClientCAs: pool}
	ws.StartTLS()
	defer ws.Close()
	url := "wss" + strings.TrimPrefix(ws.URL, "https")
	roots := x509.NewCertPool()
	roots.AddCert(ws.Certificate())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(ep string, certs []tls.Certificate) *testclient.TCPClient {
		t.Helper()
		c := testclient.NewTCP(testclient.Config{Endpoint: ep, Binding: "T"})
		c.Set(lwm2m.MustParsePath("/3/0/0"), lwm2m.String("device"))
		_, err := c.DialWebSocket(url, &tls.Config{RootCAs: roots, Certificates: certs})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	devices := &memory.Devices{}
	for _, ep := range []string{"cert-device", "plain-device"} {
		if _, err := devices.Create("default", store.Device{Endpoint: ep}); err != nil {
			t.Fatal(err)
		}
	}
	api := New(Options{Keys: deviceKeys(), Devices: devices, Server: s})
	assertIdentity := func(ep, mode, subject string) {
		t.Helper()
		w := deviceRequest(api, "GET", "/v1/devices/"+ep+"/registration", "")
		var body struct {
			Identity map[string]string `json:"identity"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 || body.Identity["mode"] != mode || body.Identity["certificateSubject"] != subject {
			t.Fatalf("registration: %d %s", w.Code, w.Body.String())
		}
	}
	if err := s.Security().Put(server.SecurityInfo{Endpoint: "cert-device", X509: true}); err != nil {
		t.Fatal(err)
	}
	first := dial("cert-device", []tls.Certificate{clientCert("cert-device", 2)})
	// Before the fix, registering with X509 credentials fails because WSS appears NoSec.
	r, err := first.Register(ctx)
	if err != nil || r.Code != codes.Created {
		t.Fatalf("certificate register: %v %v", r, err)
	}
	assertIdentity("cert-device", "x509", "cert-device")
	other := dial("cert-device", []tls.Certificate{clientCert("cert-device", 3)})
	r, err = other.Raw(ctx, codes.POST, "/rd/"+first.Location(), []string{"lt=600"}, nil, nil)
	if err != nil || r.Code != codes.Forbidden {
		t.Fatalf("different certificate update: %v %v", r, err)
	}
	r, err = first.Update(ctx, []string{"lt=600"})
	if err != nil || r.Code != codes.Changed {
		t.Fatalf("original update: %v %v", r, err)
	}
	plain := dial("plain-device", nil)
	r, err = plain.Register(ctx)
	if err != nil || r.Code != codes.Created {
		t.Fatalf("NoSec register: %v %v", r, err)
	}
	assertIdentity("plain-device", "none", "")
}
