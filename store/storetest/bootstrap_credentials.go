package storetest

import (
	"bytes"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

// BootstrapCredentials exercises the shared credential store contract.
func BootstrapCredentials(t *testing.T, s apiv1.BootstrapCredentialStore) {
	t.Helper()
	key := []byte{1, 2, 3}
	before := time.Now().UTC()
	if existed, duplicate := s.Put("a", "device", apiv1.BootstrapCredential{Identity: "identity", Key: key}); existed || duplicate {
		t.Fatal("first put")
	}
	key[0] = 9
	got, ok := s.Get("a", "device")
	if !ok || got.Identity != "identity" || !bytes.Equal(got.Key, []byte{1, 2, 3}) || got.UpdatedAt.Before(before) || got.UpdatedAt.After(time.Now().UTC()) {
		t.Fatal("incorrect stored credential")
	}
	got.Key[0] = 8
	got, _ = s.Get("a", "device")
	if got.Key[0] != 1 {
		t.Fatal("returned key aliases store")
	}
	if _, ok := s.Get("b", "device"); ok {
		t.Fatal("tenant leak")
	}
	if existed, duplicate := s.Put("b", "device", apiv1.BootstrapCredential{Identity: "identity"}); existed || !duplicate {
		t.Fatal("cross-tenant duplicate accepted")
	}
	if existed, duplicate := s.Put("a", "other", apiv1.BootstrapCredential{Identity: "identity"}); existed || !duplicate {
		t.Fatal("duplicate accepted")
	}
	if existed, duplicate := s.Put("a", "device", apiv1.BootstrapCredential{Identity: "identity", Key: []byte{4}}); !existed || duplicate {
		t.Fatal("overwrite failed")
	}
	if existed, duplicate := s.Put("b", "device", apiv1.BootstrapCredential{Identity: "other"}); existed || duplicate {
		t.Fatal("other tenant insert")
	}
	if existed, duplicate := s.Put("a", "device", apiv1.BootstrapCredential{Identity: "other"}); existed || !duplicate {
		t.Fatal("conflicting update accepted")
	}
	got, ok = s.Get("a", "device")
	if !ok || got.Identity != "identity" || !bytes.Equal(got.Key, []byte{4}) {
		t.Fatal("duplicate modified existing credential")
	}
	if s.Delete("b", "missing") || !s.Delete("a", "device") || s.Delete("a", "device") {
		t.Fatal("delete semantics")
	}
	if _, ok := s.Get("b", "device"); !ok {
		t.Fatal("delete crossed tenants")
	}
	if existed, duplicate := s.Put("a", "device", apiv1.BootstrapCredential{Identity: "identity"}); existed || duplicate {
		t.Fatal("deleted identity not reusable")
	}
}
