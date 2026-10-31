package sqlite_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/fiumaralabs/lwm2m/apiv1"
	"github.com/fiumaralabs/lwm2m/store/sqlite"
)

func TestJobsUpgradeFromMainSchema16(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	schema, err := os.ReadFile("testdata/schema_v16.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO device_profile_replays(tenant,key,body,expires) VALUES('tenant','key',X'007F',123456789)`); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 16 {
		t.Fatalf("fixture version = %d, err = %v", version, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	job := apiv1.Job{ID: "one", Target: apiv1.JobTarget{Devices: []string{"device"}}, Action: apiv1.JobAction{Operation: []byte(`{"type":"read"}`)}, Rollout: apiv1.JobRollout{MaxConcurrent: 1}, TTLSeconds: 30}
	if err = d.Jobs().Create("tenant", job); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Jobs().Get("tenant", "one"); !ok {
		t.Fatal("job missing")
	}
	if err = d.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var body []byte
	var expires int64
	if err = db.QueryRow(`SELECT body, expires FROM device_profile_replays WHERE tenant='tenant' AND key='key'`).Scan(&body, &expires); err != nil {
		t.Fatal(err)
	}
	if string(body) != string([]byte{0, 127}) || expires != 123456789 {
		t.Fatalf("replay changed: %x %d", body, expires)
	}
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 21 {
		t.Fatalf("upgraded version = %d, err = %v", version, err)
	}
}
