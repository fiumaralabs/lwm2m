package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
	"github.com/fiumaralabs/lwm2m/store/storetest"
)

func TestOperations(t *testing.T) {
	storetest.Operations(t, func(t *testing.T) store.Operations {
		d, err := sqlite.Open(filepath.Join(t.TempDir(), "ops.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := d.Close(); err != nil {
				t.Error(err)
			}
		})
		return d.Operations()
	})
}

func TestOperationsReopenAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ops.db")
	// Start with a real v1 database, including a device that must survive migration.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE devices (tenant TEXT, endpoint TEXT, payload TEXT, last_seen TEXT DEFAULT '', PRIMARY KEY(tenant,endpoint)); INSERT INTO devices VALUES('t','dev','{"endpoint":"dev"}',''); PRAGMA user_version=1;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get("t", "dev"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	o := operations.New("t", "dev", "read", []byte(`{"paths":["/3/0"]}`), nil, time.Now().UTC())
	o.Prefix, o.Format, o.Accept = "gateway", "tlv", "senml+cbor"
	if err := d.Operations().Create(ctx, "t", o); err != nil {
		t.Fatal(err)
	}
	if err := d.Operations().Update(ctx, "t", o.ID, operations.Queued, operations.Change{Status: operations.Sent, At: o.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	before, err := d.Operations().Unfinished(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := d.Operations().Unfinished(ctx)
	if err != nil || len(after) != 1 || after[0].Tenant != "t" || !reflect.DeepEqual(before, after) {
		t.Fatalf("reopen: %+v %v", after, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Operations().Get(ctx, "t", o.ID); err == nil {
		t.Fatal("operations must share device DB lifetime")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 5 {
		t.Fatalf("version %d: %v", version, err)
	}
	if _, err := db.Exec("PRAGMA user_version=6"); err != nil {
		t.Fatal(err)
	}
	if d, err := sqlite.Open(path); err == nil {
		d.Close()
		t.Fatal("future version accepted")
	}
}
