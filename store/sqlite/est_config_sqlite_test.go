package sqlite

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

func TestESTConfigUpgradeFrom21(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`INSERT INTO devices (tenant, endpoint, payload) VALUES ('t', 'kept', '{}'); DROP TABLE est_configs; PRAGMA user_version=21`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count, version int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE tenant='t' AND endpoint='kept'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("existing data: count=%d err=%v", count, err)
	}
	if err := db.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 23 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if db.ESTConfigs().Put("t", apiv1.ESTConfig{Selector: "ok"}) {
		t.Fatal("unexpected overwrite")
	}
}

func TestESTConfigConcurrentPut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, s := range []*ESTConfigs{a.ESTConfigs(), b.ESTConfigs()} {
		wg.Add(1)
		go func(s *ESTConfigs) { defer wg.Done(); results <- s.Put("t", apiv1.ESTConfig{Enabled: true}) }(s)
	}
	wg.Wait()
	close(results)
	overwritten := 0
	for result := range results {
		if result {
			overwritten++
		}
	}
	if overwritten != 1 {
		t.Fatalf("overwrites = %d, want 1", overwritten)
	}
}
