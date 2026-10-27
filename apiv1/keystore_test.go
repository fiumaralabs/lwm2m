package apiv1

import (
	"errors"
	"testing"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

func TestBootstrapKeysStayOutsideManagedStore(t *testing.T) {
	managed := &memory.APIKeys{}
	keys := deviceKeys().WithStore(managed)
	if _, err := keys.state.Get("test"); err != nil {
		t.Fatal(err)
	}
	if _, err := managed.Get("test"); !errors.Is(err, store.ErrAPIKeyNotFound) {
		t.Fatalf("bootstrap persisted: %v", err)
	}
	key := store.APIKey{KeyMetadata: store.KeyMetadata{ID: "managed"}, Tenant: "default"}
	if err := keys.state.Create(key); err != nil {
		t.Fatal(err)
	}
	restarted := Keys{}.WithStore(managed)
	if _, err := restarted.state.Get("test"); !errors.Is(err, store.ErrAPIKeyNotFound) {
		t.Fatalf("removed bootstrap still authenticates: %v", err)
	}
	if _, err := restarted.state.Get("managed"); err != nil {
		t.Fatal(err)
	}
}
