// Package admission implements the optional policy decision exchange.
package admission

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config is disabled when Endpoint is empty. Secrets are standard webhook keys.
type Config struct {
	Endpoint, KeyID                       string
	RequestSecretRefs, ResponseSecretRefs []string
	RequestSecrets, ResponseSecrets       []string
	Timeout                               time.Duration
	MaxRetries                            int
	Backoff, ReplayWindow                 time.Duration
	Transport                             http.RoundTripper
}
type Request struct {
	TenantID           string    `json:"tenant_id"`
	IdentityID         string    `json:"identity_id"`
	ExchangeID         string    `json:"exchange_id"`
	Operation          string    `json:"operation"`
	RequestFingerprint string    `json:"request_fingerprint"`
	ObservedAt         time.Time `json:"observed_at"`
	Context            any       `json:"context,omitempty"`
}
type Decision struct {
	Admitted                  bool
	MaxAge                    time.Duration
	DecisionID, ReservationID string
}
type Client struct {
	config Config
	mu     sync.Mutex
	used   map[string]*exchangeState
}

type exchangeState struct {
	digest         [32]byte
	busy, admitted bool
}

var ErrUnavailable = errors.New("admission unavailable")

func NewClient(c Config) *Client { return &Client{config: c, used: make(map[string]*exchangeState)} }
func key(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "whsec_") {
		return nil, ErrUnavailable
	}
	b, e := base64.StdEncoding.Strict().DecodeString(strings.TrimPrefix(s, "whsec_"))
	if e != nil || len(b) == 0 {
		return nil, ErrUnavailable
	}
	return b, nil
}
func mac(k []byte, id, ts string, body []byte) string {
	h := hmac.New(sha256.New, k)
	fmt.Fprintf(h, "%s.%s.", id, ts)
	h.Write(body)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
func (c *Client) Decide(ctx context.Context, request Request) (Decision, error) {
	failure := Decision{MaxAge: time.Second * 5}
	if c.config.Endpoint == "" {
		return Decision{}, nil
	}
	cfg := c.config
	cfg.RequestSecrets = append([]string(nil), cfg.RequestSecrets...)
	cfg.ResponseSecrets = append([]string(nil), cfg.ResponseSecrets...)
	for _, ref := range cfg.RequestSecretRefs {
		value, ok := os.LookupEnv(ref)
		if !ok {
			return failure, ErrUnavailable
		}
		cfg.RequestSecrets = append(cfg.RequestSecrets, value)
	}
	for _, ref := range cfg.ResponseSecretRefs {
		value, ok := os.LookupEnv(ref)
		if !ok {
			return failure, ErrUnavailable
		}
		cfg.ResponseSecrets = append(cfg.ResponseSecrets, value)
	}
	u, e := url.Parse(cfg.Endpoint)
	if e != nil || u.User != nil || u.Path != "/v1/admission-decisions" || u.RawQuery != "" || u.Fragment != "" || u.Scheme != "https" || u.Host == "" {
		return failure, ErrUnavailable
	}
	if request.TenantID == "" || request.IdentityID == "" || request.ExchangeID == "" || request.Operation == "" || request.RequestFingerprint == "" || request.ObservedAt.IsZero() || len(cfg.RequestSecrets) == 0 || len(cfg.ResponseSecrets) == 0 {
		return failure, ErrUnavailable
	}
	reqKeys, respKeys := [][]byte{}, [][]byte{}
	for _, s := range cfg.RequestSecrets {
		k, e := key(s)
		if e != nil {
			return failure, ErrUnavailable
		}
		reqKeys = append(reqKeys, k)
	}
	for _, s := range cfg.ResponseSecrets {
		k, e := key(s)
		if e != nil {
			return failure, ErrUnavailable
		}
		for _, requestKey := range reqKeys {
			if hmac.Equal(requestKey, k) {
				return failure, ErrUnavailable
			}
		}
		respKeys = append(respKeys, k)
	}
	body, e := json.Marshal(request)
	if e != nil {
		return failure, ErrUnavailable
	}
	if len(body) > 65536 {
		return failure, ErrUnavailable
	}
	timeout := cfg.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	window := cfg.ReplayWindow
	if window <= 0 || window > 5*time.Minute {
		window = 5 * time.Minute
	}
	retries := cfg.MaxRetries
	if retries < 0 {
		retries = 0
	}
	if retries > 5 {
		retries = 5
	}
	backoff := cfg.Backoff
	if backoff < 0 {
		backoff = 0
	}
	if backoff > time.Second {
		backoff = time.Second
	}
	httpClient := http.Client{Transport: cfg.Transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	digest := sha256.Sum256(body)
	c.mu.Lock()
	if c.used == nil {
		c.used = make(map[string]*exchangeState)
	}
	state := c.used[request.ExchangeID]
	if state == nil {
		if len(c.used) >= 100000 {
			c.mu.Unlock()
			return failure, ErrUnavailable
		}
		state = &exchangeState{digest: digest}
		c.used[request.ExchangeID] = state
	}
	if state.digest != digest || state.busy || state.admitted {
		c.mu.Unlock()
		return failure, ErrUnavailable
	}
	state.busy = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); state.busy = false; c.mu.Unlock() }()

	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 && backoff > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return failure, ErrUnavailable
			case <-timer.C:
			}
		}
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		httpReq, e := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader(body))
		if e != nil {
			return failure, ErrUnavailable
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("webhook-id", request.ExchangeID)
		httpReq.Header.Set("webhook-timestamp", ts)
		sigs := make([]string, 0, len(reqKeys))
		for _, k := range reqKeys {
			sigs = append(sigs, "v1,"+mac(k, request.ExchangeID, ts, body))
		}
		httpReq.Header.Set("webhook-signature", strings.Join(sigs, " "))
		resp, e := httpClient.Do(httpReq)
		if e != nil {
			continue
		}
		raw, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			continue
		}
		if e != nil || len(raw) > 65536 || resp.StatusCode != http.StatusOK || resp.Header.Get("webhook-id") != request.ExchangeID || resp.Header.Get("webhook-timestamp") != ts {
			return failure, ErrUnavailable
		}
		stamp, e := strconv.ParseInt(ts, 10, 64)
		now := time.Now()
		if e != nil || now.Sub(time.Unix(stamp, 0)) > window || time.Unix(stamp, 0).Sub(now) > window {
			return failure, ErrUnavailable
		}
		valid := false
		for _, sig := range strings.Fields(resp.Header.Get("webhook-signature")) {
			if !strings.HasPrefix(sig, "v1,") {
				continue
			}
			for _, k := range respKeys {
				if hmac.Equal([]byte(strings.TrimPrefix(sig, "v1,")), []byte(mac(k, request.ExchangeID, ts, raw))) {
					valid = true
				}
			}
		}
		if !valid {
			return failure, ErrUnavailable
		}
		var result struct {
			TenantID      string    `json:"tenant_id"`
			IdentityID    string    `json:"identity_id"`
			ExchangeID    string    `json:"exchange_id"`
			Fingerprint   string    `json:"request_fingerprint"`
			Decision      string    `json:"decision"`
			DecisionID    string    `json:"decision_id"`
			ReservationID string    `json:"reservation_id"`
			IssuedAt      time.Time `json:"issued_at"`
			ExpiresAt     time.Time `json:"expires_at"`
		}
		if json.Unmarshal(raw, &result) != nil || result.TenantID != request.TenantID || result.IdentityID != request.IdentityID || result.ExchangeID != request.ExchangeID || result.Fingerprint != request.RequestFingerprint || result.DecisionID == "" || result.IssuedAt.IsZero() || result.IssuedAt.After(now) || !result.ExpiresAt.After(now) || !result.ExpiresAt.After(result.IssuedAt) || result.Decision != "allow" {
			return failure, ErrUnavailable
		}
		c.mu.Lock()
		state.admitted = true
		c.mu.Unlock()
		return Decision{Admitted: true, DecisionID: result.DecisionID, ReservationID: result.ReservationID}, nil
	}
	return failure, ErrUnavailable
}
