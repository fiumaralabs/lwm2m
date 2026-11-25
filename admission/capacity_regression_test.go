package admission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type capacityTransport struct {
	calls     int
	recovered bool
}

func (tr *capacityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.calls++
	if !tr.recovered {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	body, err := json.Marshal(map[string]any{"tenant_id": req.TenantID, "identity_id": req.IdentityID, "exchange_id": req.ExchangeID, "request_fingerprint": req.RequestFingerprint, "decision": "allow", "decision_id": "decision", "issued_at": now.Add(-time.Second), "expires_at": now.Add(time.Minute)})
	if err != nil {
		return nil, err
	}
	id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
	header := make(http.Header)
	header.Set("webhook-id", id)
	header.Set("webhook-timestamp", ts)
	header.Set("webhook-signature", testSign("whsec_Yg==", id, ts, body))
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

func TestFailedExchangeBindingExpires(t *testing.T) {
	tr := &capacityTransport{}
	c := NewClient(Config{Enabled: true, Endpoint: "https://example.test/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, ReplayWindow: time.Hour, Transport: tr})
	req := Request{TenantID: "tenant", IdentityID: "device", ExchangeID: "one", Operation: "Register", RequestFingerprint: "fingerprint", ObservedAt: time.Now()}
	c.Decide(context.Background(), req)
	req.RequestFingerprint = "different"
	c.Decide(context.Background(), req)
	if tr.calls != 1 {
		t.Fatalf("conflicting binding reached transport: %d", tr.calls)
	}
	// Advance expiry deterministically, without waiting for an hour.
	c.mu.Lock()
	c.used[req.ExchangeID].expires = time.Now().Add(-time.Second)
	c.mu.Unlock()
	c.Decide(context.Background(), req)
	if tr.calls != 2 {
		t.Fatalf("expired binding not reclaimed: %d", tr.calls)
	}
}

func TestFailedExchangesDoNotPermanentlyExhaustCapacity(t *testing.T) {
	tr := &capacityTransport{}
	c := NewClient(Config{Enabled: true, Endpoint: "https://example.test/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, ReplayWindow: time.Nanosecond, Transport: tr})
	req := Request{TenantID: "tenant", IdentityID: "device", Operation: "Register", RequestFingerprint: "fingerprint", ObservedAt: time.Now()}
	for i := 0; i < 100001; i++ {
		req.ExchangeID = fmt.Sprintf("exchange-%d", i)
		_, err := c.Decide(context.Background(), req)
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if tr.calls != 100001 {
		t.Fatalf("sent %d requests, want 100001", tr.calls)
	}
	tr.recovered = true
	// Use a realistic signature replay window after the accelerated outage.
	c.config.ReplayWindow = 5 * time.Minute
	req.ExchangeID = "recovered"
	got, err := c.Decide(context.Background(), req)
	if err != nil || !got.Admitted {
		t.Fatalf("recovery: %+v, %v", got, err)
	}
	if _, err = c.Decide(context.Background(), req); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("replay: %v", err)
	}
	if tr.calls != 100002 {
		t.Fatalf("unexpected calls: %d", tr.calls)
	}
}
