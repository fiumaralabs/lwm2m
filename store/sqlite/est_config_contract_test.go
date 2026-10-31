package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestESTConfigContract(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var s apiv1.ESTConfigStore
			if backend == "memory" {
				s = apiv1.NewMemoryESTConfigStore()
			} else {
				db, err := sqlite.Open(filepath.Join(t.TempDir(), "est.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				s = db.ESTConfigs()
			}
			first := apiv1.ESTConfig{Enabled: true, Certificate: "cert", PrivateKey: "key", ValidityDays: 123, Selector: "selector"}
			second := apiv1.ESTConfig{Enabled: false, Certificate: "other", PrivateKey: "other key", ValidityDays: 5, Selector: "new"}
			if _, ok := s.Get("missing"); ok {
				t.Fatal("missing tenant exists")
			}
			if s.Put("one", first) {
				t.Fatal("first put reported overwrite")
			}
			if _, ok := s.Get("two"); ok {
				t.Fatal("tenant leak")
			}
			if s.Put("two", first) {
				t.Fatal("other tenant reported overwrite")
			}
			if !s.Put("one", second) {
				t.Fatal("overwrite not reported")
			}
			if got, ok := s.Get("one"); !ok || got != second {
				t.Fatalf("overwritten config: %+v %v", got, ok)
			}
			if got, ok := s.Get("two"); !ok || got != first {
				t.Fatalf("other tenant: %+v %v", got, ok)
			}
		})
	}
}

func TestESTConfigReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "est.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := apiv1.ESTConfig{Enabled: true, Certificate: "certificate", PrivateKey: "private key", ValidityDays: 42, Selector: "/3/0"}
	if db.ESTConfigs().Put("tenant", want) {
		t.Fatal("unexpected overwrite")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got, ok := db.ESTConfigs().Get("tenant"); !ok || got != want {
		t.Fatalf("after reopen: %+v %v", got, ok)
	}
	if _, ok := db.ESTConfigs().Get("other"); ok {
		t.Fatal("tenant leak after reopen")
	}
}
