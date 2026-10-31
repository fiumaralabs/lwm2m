package apiv1

import "testing"

func TestTrustAnchorStoreContract(t *testing.T) {
	s := NewMemoryTrustAnchorStore(1)
	a := TrustAnchor{ID: "a", Name: "CA", Certificates: []TrustAnchorCertificate{{Fingerprint: "abc"}}}
	if err := s.Create("one", a); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Get("one", "a"); !ok || got.Name != "CA" {
		t.Fatalf("get: %#v %v", got, ok)
	}
	if _, ok := s.Get("two", "a"); ok {
		t.Fatal("tenant leak")
	}
	if got := s.List("one"); len(got) != 1 {
		t.Fatalf("list: %#v", got)
	}
	if err := s.Create("one", TrustAnchor{ID: "b", Certificates: []TrustAnchorCertificate{{Fingerprint: "xyz"}}}); err != ErrTrustAnchorLimit {
		t.Fatalf("limit: %v", err)
	}
	if s.Delete("two", "a") || !s.Delete("one", "a") {
		t.Fatal("delete")
	}
	if _, ok := s.Get("one", "a"); ok {
		t.Fatal("not deleted")
	}
	if err := s.Create("two", a); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("two", "a"); !ok {
		t.Fatal("missing other tenant")
	}
}
