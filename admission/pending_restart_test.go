package admission

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestPendingExchangeSurvivesRestart(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	req := Request{TenantID: "tenant", IdentityID: "device", ExchangeID: "restart", Operation: "Register", RequestFingerprint: "fp", ObservedAt: now}
	var bodies [][]byte
	var store *SQLiteBindingStore
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		var count int
		if err := store.db.QueryRow(`SELECT count(*) FROM admission_pending_exchanges WHERE exchange_id=?`, req.ExchangeID).Scan(&count); err != nil || count != 1 {
			t.Errorf("pending before dispatch: count=%d err=%v", count, err)
		}
		raw, _ := json.Marshal(map[string]any{"tenant_id": req.TenantID, "identity_id": req.IdentityID, "exchange_id": req.ExchangeID, "request_fingerprint": req.RequestFingerprint, "decision": "allow", "decision_id": "decision", "issued_at": now.Add(-time.Second), "expires_at": now.Add(time.Minute)})
		id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
		w.Header().Set("webhook-id", id)
		w.Header().Set("webhook-timestamp", ts)
		w.Header().Set("webhook-signature", testSign("whsec_Yg==", id, ts, raw))
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "db")
	var err error
	store, err = OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Enabled: true, Endpoint: server.URL + "/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, Transport: server.Client().Transport, BindingStore: store}
	ctx := context.Background()
	first, err := NewClient(cfg).Decide(ctx, req)
	if err != nil || !first.Admitted {
		t.Fatalf("first: %+v %v", first, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenSQLiteBindingStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg.BindingStore = store
	if status, _, err := store.LookupExchange(ctx, req.ExchangeID); err != nil || status != ExchangePending {
		t.Fatalf("pending lookup: %s %v", status, err)
	}
	changed := req
	changed.Operation = "Bootstrap"
	if _, err := NewClient(cfg).Decide(ctx, changed); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("changed retry: %v", err)
	}
	retry := req
	retry.ObservedAt = now.Add(10 * time.Second)
	second, err := NewClient(cfg).Decide(ctx, retry)
	if err != nil || !second.Admitted {
		t.Fatalf("retry: %+v %v", second, err)
	}
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) {
		t.Fatalf("different requests: %q", bodies)
	}
	if err = store.CommitAdmitted(ctx, second.Binding, second.Receipt, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err = store.CommitAdmitted(ctx, second.Binding, second.Receipt, func(*sql.Tx) error {
		t.Error("duplicate commit repeated the effect")
		return nil
	}); err != nil {
		t.Fatalf("duplicate commit: %v", err)
	}
	status, receipt, err := store.LookupExchange(ctx, req.ExchangeID)
	if err != nil || status != ExchangeAccepted || receipt.DecisionID != second.Receipt.DecisionID || receipt.DecisionDigest != second.Receipt.DecisionDigest {
		t.Fatalf("accepted lookup: %s %+v %v", status, receipt, err)
	}
	if _, _, err := store.LookupExchange(ctx, "missing"); !errors.Is(err, ErrExchangeUnknown) {
		t.Fatalf("unknown lookup: %v", err)
	}
	future := WithReceiptClock(ctx, func() time.Time { return now.Add(time.Hour) })
	if _, _, err := store.LookupExchange(future, req.ExchangeID); !errors.Is(err, ErrExchangeUnknown) {
		t.Fatalf("expired lookup: %v", err)
	}
	var count int
	if err = store.db.QueryRow(`SELECT count(*) FROM admission_receipts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipt count %d: %v", count, err)
	}
}
