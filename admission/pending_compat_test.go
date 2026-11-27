package admission

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUnsupportedBindingStoreFailsClosed(t *testing.T) {
	store := struct{ BindingStore }{NewMemoryBindingStore()}
	tr := &capacityTransport{recovered: true}
	c := NewClient(Config{Enabled: true, Endpoint: "https://example.test/v1/admission-decisions", RequestSecrets: []string{"whsec_YQ=="}, ResponseSecrets: []string{"whsec_Yg=="}, BindingStore: store, Transport: tr})
	req := Request{TenantID: "t", IdentityID: "d", ExchangeID: "e", Operation: "Register", RequestFingerprint: "fp", ObservedAt: time.Now()}
	if _, err := c.Decide(context.Background(), req); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsupported store must fail closed: %v", err)
	}
}
