package leshanapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/bootstrap"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

// pyBytes is Python str([ord(n) for n in s]).
func pyBytes(s string) string {
	parts := make([]string, len(s))
	for i, c := range []byte(s) {
		parts[i] = fmt.Sprint(c)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// createBSDevice is leshan.py create_bs_device, byte for byte.
func (l *leshan) createBSDevice(ep, serverURI, bsPasswd, passwd string) error {
	psk := fmt.Sprintf("%x", bsPasswd)
	if _, err := l.put("/security/clients/", fmt.Sprintf(`{"tls":{"mode":"psk","details":{"identity":"%s","key":"%s"}},"endpoint":"%s"}`, ep, psk, ep), ""); err != nil {
		return err
	}
	content := `{"servers":{"0":{"binding":"U","defaultMinPeriod":1,"lifetime":86400,"notifIfDisabled":false,"shortId":1}},"security":{"1":{"bootstrapServer":false,"clientOldOffTime":1,"publicKeyOrId":` + pyBytes(ep) + `,"secretKey":` + pyBytes(passwd) + `,"securityMode":"PSK","serverId":1,"serverSmsNumber":"","smsBindingKeyParam":[],"smsBindingKeySecret":[],"smsSecurityMode":"NO_SEC","uri":"` + serverURI + `"}},"oscore":{},"toDelete":["/0","/1"]}`
	_, err := l.post("/bootstrap/"+ep, content)
	return err
}

// deleteBSDevice is leshan.py delete_bs_device: both calls must be 2xx.
func (l *leshan) deleteBSDevice(ep string) error {
	if _, err := l.deleteRaw("/security/clients/" + ep); err != nil {
		return err
	}
	_, err := l.deleteRaw("/bootstrap/" + ep)
	return err
}

// The conftest endpoint_bootstrap fixture against NewBootstrap: the BS
// REST on its own listener, then a DTLS-PSK bootstrap that deletes /0
// and /1 but keeps the BS account, writes /0/1 and /1/0, and lets the
// client register at the LwM2M server with the provisioned PSK.
func TestBootstrapREST(t *testing.T) {
	e := newEnv(t, nil)
	configs := bootstrap.NewMemoryConfigStore()
	bs := bootstrap.New(bootstrap.Config{Configs: configs, RequestTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = bs.Close() })
	bsAddr, err := bs.ListenDTLS("127.0.0.1:0", bootstrap.DTLSConfig{CIDLength: 6})
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(NewBootstrap("", configs, bs.Security()))
	t.Cleanup(hs.Close)
	lb := newLeshan(t, hs.URL+"/api")

	const ep, bsPass, pass = "client_a3", "bsbsbsbsbsbsbsbs", "abcdefghijklmnop"
	if err := lb.createBSDevice(ep, "coaps://"+e.dtls, bsPass, pass); err != nil {
		t.Fatal(err)
	}
	must(t)(e.l.createPSKDevice(ep, pass))
	_, v := (&env{t: t, http: hs}).raw("GET", "/security/clients", "")
	golden(t, "bs security list", v, `[{"endpoint":"client_a3","tls":{"mode":"psk","details":{"identity":"client_a3","key":"62736273627362736273627362736273"}}}]`)
	if _, err := lb.post("/bootstrap/bad", `{"servers":{"0":{"shortId":0}}}`); err == nil {
		t.Fatal("invalid config accepted")
	}

	c := testclient.NewBootstrap(testclient.Config{Endpoint: ep})
	c.Set(mp("/0/0/0"), lwm2m.String("coaps://"+bsAddr.String()))
	c.Set(mp("/0/0/1"), lwm2m.Boolean(true))
	c.Set(mp("/0/0/2"), lwm2m.Integer(0))
	c.Set(mp("/0/0/3"), lwm2m.Opaque([]byte(ep)))
	c.Set(mp("/0/0/5"), lwm2m.Opaque([]byte(bsPass)))
	c.Set(mp("/1/2/0"), lwm2m.Integer(2)) // int-4: gone after bootstrap
	c.Set(mp("/3/0/0"), lwm2m.String("Zephyr"))
	if err := c.DialDTLS(bsAddr.String(), testclient.PSKConfig(ep, []byte(bsPass))...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	r, err := c.BootstrapRequest(e.ctx, c.BootstrapQuery(nil))
	if err != nil || r.Code != codes.Changed {
		t.Fatalf("bootstrap request: %v %v", r, err)
	}
	if fin, err := c.WaitFinish(e.ctx); err != nil || fin != codes.Changed {
		t.Fatalf("finish %v %v\n%s", fin, err, c.Dump())
	}
	if _, ok := c.Get(mp("/1/2/0")); ok {
		t.Fatal("/1/2 survived toDelete")
	}
	if v, _ := c.Get(mp("/0/0/0")); v.Str != "coaps://"+bsAddr.String() {
		t.Fatalf("BS account lost: %q", v.Str)
	}
	for p, want := range map[string]any{"/1/0/0": int64(1), "/1/0/1": int64(86400), "/1/0/6": false, "/1/0/7": "U", "/0/1/10": int64(1)} {
		v, ok := c.Get(mp(p))
		got := any(v.Int)
		switch want.(type) {
		case bool:
			got = v.Bool
		case string:
			got = v.Str
		}
		if !ok || got != want {
			t.Fatalf("%s = %v, want %v", p, got, want)
		}
	}
	uri, id, key, ok := c.ServerAccount()
	if !ok || uri != "coaps://"+e.dtls || string(id) != ep || string(key) != pass {
		t.Fatalf("server account %q %q %q", uri, id, key)
	}
	_ = c.Close()
	if err := c.DialDTLS(e.dtls, testclient.PSKConfig(ep, []byte(pass))...); err != nil {
		t.Fatal(err)
	}
	if r, err := c.Register(e.ctx); err != nil || r.Code != codes.Created {
		t.Fatalf("register: %v %v", r, err)
	}
	if reg := must(t)(e.l.get("/clients/" + ep)).(map[string]any); reg["secure"] != true {
		t.Fatalf("secure %v", reg["secure"])
	}

	// Teardown: 200 then 204; a second delete_bs_device raises (404).
	if err := lb.deleteBSDevice(ep); err != nil {
		t.Fatal(err)
	}
	if err := lb.deleteBSDevice(ep); err == nil {
		t.Fatal("deleting a missing config did not raise")
	}
}
