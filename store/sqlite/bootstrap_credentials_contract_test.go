package sqlite_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

type bootstrapCredentialsProvider interface {
	BootstrapCredentials() apiv1.BootstrapCredentialStore
}

func TestBootstrapCredentialContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) { storetest.BootstrapCredentials(t, apiv1.NewMemoryBootstrapCredentialStore()) })
	t.Run("sqlite", func(t *testing.T) {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "credentials.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		p, ok := any(db).(bootstrapCredentialsProvider)
		if !ok {
			t.Fatal("SQLite bootstrap credential store not implemented")
		}
		storetest.BootstrapCredentials(t, p.BootstrapCredentials())
	})
}

func TestBootstrapCredentialConcurrentConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	a, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	stores := []apiv1.BootstrapCredentialStore{any(a).(bootstrapCredentialsProvider).BootstrapCredentials(), any(b).(bootstrapCredentialsProvider).BootstrapCredentials()}
	for i := 0; i < 20; i++ {
		start := make(chan struct{})
		var wg sync.WaitGroup
		var results [2][2]bool
		for j := range stores {
			wg.Add(1)
			go func(j int) {
				defer wg.Done()
				<-start
				e, d := stores[j].Put(fmt.Sprint(j), "device", apiv1.BootstrapCredential{Identity: fmt.Sprint(i)})
				results[j] = [2]bool{e, d}
			}(j)
		}
		close(start)
		wg.Wait()
		if !((results[0] == [2]bool{false, false} && results[1] == [2]bool{false, true}) || (results[1] == [2]bool{false, false} && results[0] == [2]bool{false, true})) {
			t.Fatalf("concurrent insert: %v", results)
		}
		stores[0].Delete("0", "device")
		stores[1].Delete("1", "device")
	}
}

func TestBootstrapCredentialUpgradeFrom22(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`DROP TABLE bootstrap_credentials; INSERT INTO devices(tenant, endpoint, payload) VALUES ('t', 'kept', '{}'); PRAGMA user_version=22`); err != nil {
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Get("t", "kept"); err != nil {
		t.Fatal("migration lost device", err)
	}
	s := any(db).(bootstrapCredentialsProvider).BootstrapCredentials()
	if existed, duplicate := s.Put("t", "kept", apiv1.BootstrapCredential{Identity: "upgrade"}); existed || duplicate {
		t.Fatal("upgrade insert failed")
	}
}

func TestBootstrapCredentialReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := any(db).(bootstrapCredentialsProvider)
	if !ok {
		db.Close()
		t.Fatal("SQLite bootstrap credential store not implemented")
	}
	p.BootstrapCredentials().Put("tenant", "device", apiv1.BootstrapCredential{Identity: "id", Key: []byte{1, 2}})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := any(db).(bootstrapCredentialsProvider).BootstrapCredentials()
	got, ok := s.Get("tenant", "device")
	if !ok || got.Identity != "id" || len(got.Key) != 2 || got.Key[0] != 1 || got.UpdatedAt.IsZero() {
		t.Fatal("credential lost on reopen")
	}
	if _, ok := s.Get("other", "device"); ok {
		t.Fatal("tenant leak after reopen")
	}
}
