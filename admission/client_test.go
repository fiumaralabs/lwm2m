package admission

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testSign(secret, id, ts string, body []byte) string {
	key, _ := base64.StdEncoding.DecodeString(secret[len("whsec_"):])
	h := hmac.New(sha256.New, key)
	fmt.Fprintf(h, "%s.%s.", id, ts)
	h.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(h.Sum(nil))
}

type handlerTransport struct {
	handler http.Handler
	delayed atomic.Bool
}

func (tr *handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Force the first request past its deadline before delivering it. Unlike a
	// network transport, this controlled transport synchronously finishes each
	// handler before returning its response, even if the context has expired.
	if tr.delayed.CompareAndSwap(false, true) {
		<-r.Context().Done()
	}
	w := httptest.NewRecorder()
	tr.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func TestClientDecide(t *testing.T) {
	data, err := os.ReadFile("testdata/signing.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	secret := vectors[0].Secret
	t.Setenv("ADMISSION_TEST_REQUEST_KEY", secret)
	responseSecret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("separate-response-test-key"))
	t.Setenv("ADMISSION_TEST_RESPONSE_KEY", responseSecret)
	req := Request{TenantID: "tenant", IdentityID: "device", ExchangeID: "exchange_123", Operation: "Register", RequestFingerprint: "fingerprint", ObservedAt: time.Now().UTC()}
	for _, name := range []string{"disabled", "allow", "deny", "timeout", "retries", "signature", "retry-after-failure", "replay", "binding", "expired", "store-restart"} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			var firstBody, firstID string
			var mu sync.Mutex
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				n := calls.Add(1)
				body, _ := io.ReadAll(r.Body)
				id, ts := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
				if n == 1 {
					firstBody, firstID = string(body), id
				} else if firstBody != string(body) || firstID != id {
					t.Error("retry binding changed")
				}
				mu.Unlock()
				if r.URL.Path != "/v1/admission-decisions" || r.Method != "POST" || r.Header.Get("webhook-signature") != testSign(secret, id, ts, body) {
					t.Error("request signature or target invalid")
				}
				if name == "timeout" {
					<-r.Context().Done()
					return
				}
				if name == "retries" {
					if n == 1 && r.Context().Err() == nil {
						t.Error("first attempt was not delayed past its deadline")
					}
					w.WriteHeader(503)
					return
				}
				now := time.Now().UTC()
				response := map[string]any{"tenant_id": req.TenantID, "identity_id": req.IdentityID, "exchange_id": req.ExchangeID, "request_fingerprint": req.RequestFingerprint, "decision": "allow", "decision_id": "decision", "issued_at": now.Add(-time.Second), "expires_at": now.Add(time.Minute)}
				switch name {
				case "deny":
					response["decision"] = "deny"
				case "binding":
					response["tenant_id"] = "other"
				case "expired":
					response["expires_at"] = now.Add(-time.Second)
				case "replay":
					ts = "1700000000"
				}
				b, _ := json.Marshal(response)
				sig := testSign(responseSecret, id, ts, b)
				if name == "signature" || (name == "retry-after-failure" && n == 1) {
					sig = "v1,AAAA"
				}
				w.Header().Set("webhook-id", id)
				w.Header().Set("webhook-timestamp", ts)
				w.Header().Set("webhook-signature", sig)
				w.Write(b)
			})
			server := httptest.NewTLSServer(handler)
			defer server.Close()
			// Only the timeout case needs a short real network deadline.
			// The retry case uses a synchronous transport below.
			cfg := Config{Enabled: true, Endpoint: server.URL + "/v1/admission-decisions", RequestSecretRefs: []string{"ADMISSION_TEST_REQUEST_KEY"}, ResponseSecretRefs: []string{"ADMISSION_TEST_RESPONSE_KEY"}, Timeout: 5 * time.Second, MaxRetries: 2, Transport: server.Client().Transport}
			if name == "retries" {
				cfg.Timeout = time.Nanosecond
				cfg.Transport = &handlerTransport{handler: handler}
			}
			if name == "timeout" {
				cfg.Timeout = 20 * time.Millisecond
			}
			if name == "disabled" {
				cfg.Enabled = false
				cfg.RequestSecrets = nil
				cfg.ResponseSecrets = nil
				cfg.RequestSecretRefs = nil
				cfg.ResponseSecretRefs = nil
			}
			var bindingPath string
			var bindingDB *SQLiteBindingStore
			if name == "store-restart" {
				bindingPath = filepath.Join(t.TempDir(), "admission.db")
				var err error
				bindingDB, err = OpenSQLiteBindingStore(bindingPath)
				if err != nil {
					t.Fatal(err)
				}
				cfg.BindingStore = bindingDB
			}
			client := NewClient(cfg)
			got, err := client.Decide(context.Background(), req)
			if name == "store-restart" {
				if err != nil || !got.Admitted {
					t.Fatalf("first: %+v %v", got, err)
				}
				if err := bindingDB.Close(); err != nil {
					t.Fatal(err)
				}
				bindingDB, err = OpenSQLiteBindingStore(bindingPath)
				if err != nil {
					t.Fatal(err)
				}
				defer bindingDB.Close()
				cfg.BindingStore = bindingDB
				got, err = NewClient(cfg).Decide(context.Background(), req)
				if err == nil || got.Admitted {
					t.Fatalf("restart allowed replay: %+v %v", got, err)
				}
				return
			}
			if name == "disabled" {
				if calls.Load() != 0 || err != nil || got.Admitted {
					t.Fatalf("disabled: %+v %v calls=%d", got, err, calls.Load())
				}
				return
			}
			if name == "allow" {
				if err != nil || !got.Admitted {
					t.Fatalf("allow: %+v %v", got, err)
				}
				if _, err := client.Decide(context.Background(), req); err == nil {
					t.Fatal("successful exchange was accepted twice")
				}
				return
			}
			if name == "retry-after-failure" {
				if err == nil || got.Admitted {
					t.Fatal("first attempt should fail")
				}
				got, err = client.Decide(context.Background(), req)
				if err != nil || !got.Admitted {
					t.Fatalf("retry: %+v %v", got, err)
				}
				return
			}
			if got.Admitted || got.MaxAge <= 0 || err == nil {
				t.Fatalf("failure: %+v %v", got, err)
			}
			if name == "retries" && calls.Load() != 3 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}

func TestClientSigningFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/signing.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name, Secret, ID, Timestamp, Body string
		Expected                          string `json:"expected_signature"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			k, err := key(v.Secret)
			if err != nil {
				t.Fatal(err)
			}
			if got := "v1," + mac(k, v.ID, v.Timestamp, []byte(v.Body)); got != v.Expected {
				t.Fatal("signature differs from published vector")
			}
		})
	}
}

func TestVerificationVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/signing.json")
	if err != nil {
		t.Fatal(err)
	}
	var vs []struct {
		Name, Secret, ID, Timestamp, Body string
		Provided                          string `json:"provided_signature"`
		RequestID                         string `json:"request_id"`
		RequestTimestamp                  string `json:"request_timestamp"`
		Now                               int64
		Valid                             bool
	}
	if err := json.Unmarshal(data, &vs); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		t.Run(v.Name, func(t *testing.T) {
			k, err := key(v.Secret)
			if err != nil {
				t.Fatal(err)
			}
			got := verifyResponse(v.ID, v.Timestamp, v.Provided, v.RequestID, v.RequestTimestamp, []byte(v.Body), [][]byte{k}, time.Unix(v.Now, 0), 5*time.Minute)
			if got != v.Valid {
				t.Fatalf("got %v, want %v", got, v.Valid)
			}
		})
	}
}
