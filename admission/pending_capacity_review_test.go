package admission

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCapacityRejectionDoesNotPersistPending(t *testing.T) {
	store := NewMemoryBindingStore()
	c := NewClient(Config{Enabled: true, Endpoint: "https://example.test/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, BindingStore: store, Transport: &capacityTransport{}})
	c.used = make(map[string]*exchangeState)
	for i := 0; i < 100000; i++ {
		id := fmt.Sprintf("full-%d", i)
		c.used[id] = &exchangeState{id: id, busy: true}
	}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("refused-%d", i)
		req := Request{TenantID: "tenant", IdentityID: "device", ExchangeID: id, Operation: "Register", RequestFingerprint: "fp", ObservedAt: time.Now()}
		if _, err := c.Decide(context.Background(), req); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Decide: %v", err)
		}
		if _, _, err := store.LookupExchange(context.Background(), id); !errors.Is(err, ErrExchangeUnknown) {
			t.Fatalf("refused exchange persisted: %v", err)
		}
	}
}
