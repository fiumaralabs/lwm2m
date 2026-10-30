package coap

import (
	"reflect"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
)

func TestRevokingSecurityStoreMutations(t *testing.T) {
	var revoked []string
	s := NewRevokingSecurityStore(server.NewMemorySecurityStore(), func(id string) { revoked = append(revoked, id) })
	put := func(ep, id string) error {
		return s.Put(server.SecurityInfo{Endpoint: ep, PSKIdentity: id, PSKKey: []byte("0123456789abcdef")})
	}
	if err := put("one", "old"); err != nil {
		t.Fatal(err)
	}
	if err := put("two", "taken"); err != nil {
		t.Fatal(err)
	}
	if err := put("one", "taken"); err != server.ErrDuplicatePSKIdentity {
		t.Fatalf("duplicate: %v", err)
	}
	if len(revoked) != 0 {
		t.Fatalf("failed mutation revoked %v", revoked)
	}
	if err := put("one", "new"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Remove("one"); err != nil || !ok {
		t.Fatal("missing endpoint")
	}
	if _, ok, err := s.Remove("one"); err != nil || ok {
		t.Fatal("removed twice")
	}
	if !reflect.DeepEqual(revoked, []string{"old", "new"}) {
		t.Fatalf("revoked: %v", revoked)
	}
}
