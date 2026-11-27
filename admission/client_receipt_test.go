package admission

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestClientVerifiedReceiptRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	req := Request{TenantID: "tenant", IdentityID: "device", ExchangeID: "exchange", Operation: "Register", RequestFingerprint: "fingerprint", ObservedAt: now}
	var response, requestBody []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ = io.ReadAll(r.Body)
		response, _ = json.Marshal(map[string]any{"tenant_id": req.TenantID, "identity_id": req.IdentityID, "exchange_id": req.ExchangeID, "request_fingerprint": req.RequestFingerprint, "decision": "allow", "decision_id": "decision", "reservation_id": "reservation", "issued_at": now.Add(-time.Second), "expires_at": now.Add(time.Minute)})
		id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
		w.Header().Set("webhook-id", id)
		w.Header().Set("webhook-timestamp", ts)
		w.Header().Set("webhook-signature", testSign("whsec_Yg==", id, ts, response))
		_, _ = w.Write(response)
	}))
	defer server.Close()
	client := NewClient(Config{Enabled: true, Endpoint: server.URL + "/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, Transport: server.Client().Transport})
	got, err := client.Decide(context.Background(), req)
	if err != nil || !got.Admitted {
		t.Fatalf("decide: %+v %v", got, err)
	}
	decisionBytes, _ := json.Marshal([]string{"allow", "decision", "reservation"})
	decisionHash, requestHash := sha256.Sum256(decisionBytes), sha256.Sum256(requestBody)
	wantBinding := Binding{ExchangeID: req.ExchangeID, TenantID: req.TenantID, IdentityID: req.IdentityID, Fingerprint: req.RequestFingerprint, RequestDigest: fmt.Sprintf("%x", requestHash), DecisionDigest: fmt.Sprintf("%x", decisionHash)}
	if got.Binding != wantBinding {
		t.Fatalf("binding: got %+v want %+v", got.Binding, wantBinding)
	}
	if got.Receipt.DecisionID != "decision" || got.Receipt.DecisionDigest != wantBinding.DecisionDigest || !got.Receipt.IssuedAt.Equal(now.Add(-time.Second)) || !got.Receipt.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("receipt: %+v", got.Receipt)
	}
	store, err := OpenSQLiteBindingStore(filepath.Join(t.TempDir(), "binding.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	applied := 0
	effect := func(*sql.Tx) error { applied++; return nil }
	if err := store.CommitAdmitted(context.Background(), got.Binding, got.Receipt, effect); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitAdmitted(context.Background(), got.Binding, got.Receipt, effect); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("applied %d times", applied)
	}
}

func TestClientRejectsInvalidReceiptTimes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name            string
		issued, expires any
	}{
		{"missing issued", nil, now.Add(time.Minute)}, {"missing expires", now.Add(-time.Second), nil},
		{"issued in future", now.Add(time.Minute), now.Add(2 * time.Minute)}, {"expired", now.Add(-time.Minute), now.Add(-time.Second)},
		{"equal", now.Add(-time.Second), now.Add(-time.Second)}, {"expires before issued", now.Add(-time.Second), now.Add(-time.Minute)},
		{"malformed", "not-a-time", now.Add(time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{TenantID: "tenant", IdentityID: "device", ExchangeID: "exchange", Operation: "Register", RequestFingerprint: "fp", ObservedAt: now}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				v := map[string]any{"tenant_id": req.TenantID, "identity_id": req.IdentityID, "exchange_id": req.ExchangeID, "request_fingerprint": req.RequestFingerprint, "decision": "allow", "decision_id": "decision"}
				if tc.issued != nil {
					v["issued_at"] = tc.issued
				}
				if tc.expires != nil {
					v["expires_at"] = tc.expires
				}
				raw, _ := json.Marshal(v)
				id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
				w.Header().Set("webhook-id", id)
				w.Header().Set("webhook-timestamp", ts)
				w.Header().Set("webhook-signature", testSign("whsec_Yg==", id, ts, raw))
				_, _ = w.Write(raw)
			}))
			defer server.Close()
			c := NewClient(Config{Enabled: true, Endpoint: server.URL + "/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, Transport: server.Client().Transport})
			got, err := c.Decide(context.Background(), req)
			if got.Admitted || !errors.Is(err, ErrInvalidDecisionTime) || !errors.Is(err, ErrUnavailable) {
				t.Fatalf("got %+v, error %v", got, err)
			}
		})
	}
}
