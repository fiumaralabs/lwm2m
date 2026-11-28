package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/admission"
	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/leshanapi"
	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/testclient"
	"github.com/fiumaralabs/lwm2m/transport/coap"
	"github.com/plgd-dev/go-coap/v3/message/codes"
)

func TestAdmissionReregisterNeverRedecides(t *testing.T) {
	var calls atomic.Int32
	var bindings *admission.SQLiteBindingStore
	policy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request admission.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if status, _, err := bindings.LookupExchange(r.Context(), request.ExchangeID); err != nil || status != admission.ExchangePending {
			t.Errorf("exchange not pending before dispatch: %s %v", status, err)
		}
		if request.TenantID != "default" || request.Operation != "Register" {
			t.Errorf("incorrect policy binding: tenant=%q operation=%q", request.TenantID, request.Operation)
		}
		now := time.Now()
		body, _ := json.Marshal(map[string]any{"tenant_id": request.TenantID, "identity_id": request.IdentityID, "exchange_id": request.ExchangeID, "request_fingerprint": request.RequestFingerprint, "decision": "allow", "decision_id": "decision", "issued_at": now.Add(-time.Second), "expires_at": now.Add(time.Minute)})
		id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
		h := hmac.New(sha256.New, []byte("response-key"))
		fmt.Fprintf(h, "%s.%s.%s", id, ts, body)
		w.Header().Set("webhook-id", id)
		w.Header().Set("webhook-timestamp", ts)
		w.Header().Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(h.Sum(nil)))
		_, _ = w.Write(body)
	}))
	defer policy.Close()
	path := filepath.Join(t.TempDir(), "db")
	var err error
	bindings, err = admission.OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer bindings.Close()
	devices, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer devices.Close()
	client := admission.NewClient(admission.Config{Enabled: true, Endpoint: policy.URL + "/v1/admission-decisions", Timeout: time.Second, RequestSecrets: []string{"whsec_cmVxdWVzdC1rZXk="}, ResponseSecrets: []string{"whsec_cmVzcG9uc2Uta2V5"}, Transport: policy.Client().Transport, BindingStore: bindings})
	hook := &admissionHook{client: client, bindings: bindings}
	registered := make(chan struct{}, 2)
	srv, _, executor, dispatcher := newDaemonWithAdmission(server.Config{OnEvent: func(e server.Event) {
		if _, ok := e.(server.Registered); ok {
			registered <- struct{}{}
		}
	}}, leshanapi.New(leshanapi.Options{Prefix: "/api"}), apiv1.Keys{}, true, apiv1.NewMemoryBootstrapSessionStore(), nil, hook, devices)
	defer srv.Close()
	defer executor.Close()
	defer dispatcher.Close()
	key := []byte("0123456789abcdef")
	if err := srv.Security().Put(server.SecurityInfo{Endpoint: "registered", PSKIdentity: "registered-id", PSKKey: key}); err != nil {
		t.Fatal(err)
	}
	cb := coap.New(srv)
	defer cb.Close()
	addr, err := cb.ListenDTLS("127.0.0.1:0", coap.DTLSConfig{})
	if err != nil {
		t.Fatal(err)
	}
	c := testclient.New(testclient.Config{Endpoint: "registered"})
	defer c.Close()
	if err := c.DialDTLS(addr.String(), testclient.PSKConfig("registered-id", key)...); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, lifetime := range []string{"600", "601"} {
		res, err := c.RegisterRaw(ctx, []string{"ep=registered", "lt=" + lifetime, "lwm2m=1.1", "b=U"}, []byte("</3/0>"), true)
		if err != nil || res.Code != codes.Created {
			t.Fatalf("register lifetime %s: %v %v", lifetime, res, err)
		}
		select {
		case <-registered:
		case <-ctx.Done():
			t.Fatal("registration event did not finish")
		}
		if lifetime == "600" {
			policy.Close()
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("policy calls = %d, want 1", n)
	}
}
