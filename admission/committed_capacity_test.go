package admission

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCommittedReplayDoesNotExhaustClientCapacity(t *testing.T) {
	store := NewMemoryBindingStore()
	for i := 0; i < 100000; i++ {
		id := fmt.Sprintf("committed-%d", i)
		if _, err := store.BindAndMarkApplied(context.Background(), Binding{ExchangeID: id}); err != nil {
			t.Fatal(err)
		}
	}
	tr := &capacityTransport{recovered: true}
	c := NewClient(Config{Enabled: true, Endpoint: "https://example.test/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, BindingStore: store, Transport: tr})
	req := Request{TenantID: "tenant", IdentityID: "device", Operation: "Register", RequestFingerprint: "fingerprint", ObservedAt: time.Now()}
	for i := 0; i < 100000; i++ {
		req.ExchangeID = fmt.Sprintf("committed-%d", i)
		if _, err := c.Decide(context.Background(), req); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	req.ExchangeID = "new"
	if got, err := c.Decide(context.Background(), req); err != nil || !got.Admitted {
		t.Fatalf("new exchange after replays: %+v, %v", got, err)
	}
}

func TestCommittedLookupErrorsDoNotRetainNewState(t *testing.T) {
	store := NewMemoryBindingStore()
	c := NewClient(Config{Enabled: true, Endpoint: "https://example.test/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, BindingStore: store})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := Request{TenantID: "tenant", IdentityID: "device", ExchangeID: "failed", Operation: "Register", RequestFingerprint: "fingerprint", ObservedAt: time.Now()}
	if _, err := c.Decide(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("lookup: %v", err)
	}
	if len(c.used) != 0 || c.expiry.Len() != 0 {
		t.Fatalf("lookup error retained state: used=%d expiry=%d", len(c.used), c.expiry.Len())
	}
}
