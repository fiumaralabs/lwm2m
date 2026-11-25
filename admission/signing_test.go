package admission_test

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/webhooks"
)

type signingVector struct {
	Name              string `json:"name"`
	Secret            string `json:"secret"`
	ID                string `json:"id"`
	Timestamp         string `json:"timestamp"`
	Body              string `json:"body"`
	ExpectedSignature string `json:"expected_signature"`
	ProvidedSignature string `json:"provided_signature"`
	RequestID         string `json:"request_id"`
	RequestTimestamp  string `json:"request_timestamp"`
	Now               int64  `json:"now"`
	Valid             bool   `json:"valid"`
}

func TestSigningVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/signing.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []signingVector
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) == 0 {
		t.Fatal("no signing vectors")
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			timestamp, err := strconv.ParseInt(v.Timestamp, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			var signature string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != v.Body {
					t.Errorf("body mismatch: %q, %v", body, err)
				}
				if r.Header.Get("webhook-id") != v.ID || r.Header.Get("webhook-timestamp") != v.Timestamp {
					t.Error("header mismatch")
				}
				signature = r.Header.Get("webhook-signature")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			result := webhooks.Retry(context.Background(), "tenant", store.Webhook{URL: server.URL, Secret: v.Secret}, store.WebhookDelivery{EventID: v.ID, Envelope: []byte(v.Body)}, webhooks.Options{Now: func() time.Time { return time.Unix(timestamp, 0) }})
			if result.Error != "" {
				t.Fatal(result.Error)
			}
			if signature != v.ExpectedSignature {
				t.Errorf("signature = %q, want %q", signature, v.ExpectedSignature)
			}
			// This fixture oracle demonstrates the wire checks, not a production verifier.
			delta := v.Now - timestamp
			valid := subtle.ConstantTimeCompare([]byte(signature), []byte(v.ProvidedSignature)) == 1 && v.ID == v.RequestID && v.Timestamp == v.RequestTimestamp && delta >= -300 && delta <= 300
			if valid != v.Valid {
				t.Errorf("valid = %v, want %v", valid, v.Valid)
			}
		})
	}
}
