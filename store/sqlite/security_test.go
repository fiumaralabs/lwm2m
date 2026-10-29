package sqlite_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/server"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestSecurityStoreContract(t *testing.T) {
	factories := map[string]func(*testing.T) server.SecurityStore{
		"memory": func(t *testing.T) server.SecurityStore { return server.NewMemorySecurityStore() },
		"sqlite": func(t *testing.T) server.SecurityStore {
			db, err := sqlite.Open(filepath.Join(t.TempDir(), "security.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			return db.Security()
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			s := factory(t)
			a := server.SecurityInfo{Endpoint: "a", PSKIdentity: "old", PSKKey: []byte{1, 2, 3}}
			b := server.SecurityInfo{Endpoint: "b", X509: true}
			for _, v := range []server.SecurityInfo{b, a} {
				if err := s.Put(v); err != nil {
					t.Fatal(err)
				}
			}
			got, ok := s.ByEndpoint("a")
			if !ok || got.Endpoint != a.Endpoint || got.PSKIdentity != a.PSKIdentity || !bytes.Equal(got.PSKKey, a.PSKKey) {
				t.Fatal("endpoint lookup mismatch")
			}
			got, ok = s.ByPSKIdentity("old")
			if !ok || got.Endpoint != "a" {
				t.Fatal("identity lookup mismatch")
			}
			all := s.All()
			if len(all) != 2 || all[0].Endpoint != "a" || all[1].Endpoint != "b" || !all[1].X509 {
				t.Fatal("ordered listing mismatch")
			}
			if err := s.Put(server.SecurityInfo{Endpoint: "b", PSKIdentity: "old"}); !errors.Is(err, server.ErrDuplicatePSKIdentity) {
				t.Fatal("duplicate identity not rejected")
			}
			if err := s.Put(server.SecurityInfo{Endpoint: "fresh", PSKIdentity: "old"}); !errors.Is(err, server.ErrDuplicatePSKIdentity) {
				t.Fatal("new endpoint duplicate identity not rejected")
			}
			if err := s.Put(server.SecurityInfo{Endpoint: "a", PublicKey: []byte{4}}); !errors.Is(err, server.ErrRPKUnsupported) {
				t.Fatal("RPK not rejected")
			}
			got, ok = s.ByEndpoint("b")
			if !ok || !got.X509 {
				t.Fatal("failed update changed existing record")
			}
			a.PSKIdentity = "new"
			if err := s.Put(a); err != nil {
				t.Fatal(err)
			}
			if _, ok := s.ByPSKIdentity("old"); ok {
				t.Fatal("old identity still indexed")
			}
			if err := s.Put(server.SecurityInfo{Endpoint: "c", PSKIdentity: "old"}); err != nil {
				t.Fatal("old identity not freed")
			}
			got, ok = s.Remove("a")
			if !ok || got.PSKIdentity != "new" || !bytes.Equal(got.PSKKey, a.PSKKey) {
				t.Fatal("remove mismatch")
			}
			if _, ok := s.ByPSKIdentity("new"); ok {
				t.Fatal("removed identity still indexed")
			}
			if _, ok := s.Remove("a"); ok {
				t.Fatal("removed endpoint still present")
			}
			if _, ok := s.ByEndpoint("missing"); ok {
				t.Fatal("missing endpoint found")
			}
			if _, ok := s.ByPSKIdentity(""); ok {
				t.Fatal("empty identity found")
			}
		})
	}
}

func TestSecurityStoreReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	value := server.SecurityInfo{Endpoint: "persisted", PSKIdentity: "identity", PSKKey: []byte{3, 2, 1}}
	if err := db.Security().Put(value); err != nil {
		t.Fatal(err)
	}
	if err := db.Security().Put(server.SecurityInfo{Endpoint: "certificate", X509: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, ok := db.Security().ByPSKIdentity(value.PSKIdentity)
	if !ok || got.Endpoint != value.Endpoint || !bytes.Equal(got.PSKKey, value.PSKKey) {
		t.Fatal("credential did not survive reopen")
	}
	got, ok = db.Security().ByEndpoint("certificate")
	if !ok || !got.X509 {
		t.Fatal("certificate did not survive reopen")
	}
}
