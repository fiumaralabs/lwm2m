package admission

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBindingStores(t *testing.T) {
	for _, kind := range []string{"memory", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			var s BindingStore
			var db *SQLiteBindingStore
			path := filepath.Join(t.TempDir(), "bindings.db")
			if kind == "memory" {
				s = NewMemoryBindingStore()
			} else {
				var err error
				db, err = OpenSQLiteBindingStore(path)
				if err != nil {
					t.Fatal(err)
				}
				s = db
			}
			b := Binding{ExchangeID: "exchange", TenantID: "tenant", IdentityID: "identity", Fingerprint: "fp", DecisionDigest: "digest"}
			var first atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 32; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					already, err := s.BindAndMarkApplied(context.Background(), b)
					if err != nil {
						t.Error(err)
						return
					}
					if !already {
						first.Add(1)
					}
				}()
			}
			wg.Wait()
			if first.Load() != 1 {
				t.Fatalf("first applications = %d", first.Load())
			}
			changed := b
			changed.DecisionDigest = "other"
			if _, err := s.BindAndMarkApplied(context.Background(), changed); !errors.Is(err, ErrBindingConflict) {
				t.Fatalf("conflict = %v", err)
			}
			if db != nil {
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				var err error
				db, err = OpenSQLiteBindingStore(path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				already, err := db.BindAndMarkApplied(context.Background(), b)
				if err != nil || !already {
					t.Fatalf("restart: already=%v err=%v", already, err)
				}
			}
		})
	}
}
