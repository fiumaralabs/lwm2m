package sqlite

import (
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	_ "modernc.org/sqlite"
)

//go:embed 001_devices.sql
var migration string

//go:embed 002_operations.sql
var operationsMigration string

//go:embed 003_events.sql
var eventsMigration string

//go:embed 004_webhooks_settings.sql
var webhooksSettingsMigration string

//go:embed 005_api_keys.sql
var apiKeysMigration string

//go:embed 006_security.sql
var securityMigration string

//go:embed 007_observations.sql
var observationsMigration string

//go:embed 008_audit.sql
var auditMigration string

//go:embed 009_timeseries.sql
var timeSeriesMigration string

//go:embed 010_models.sql
var modelsMigration string

//go:embed 011_tenants.sql
var tenantsMigration string

//go:embed 012_operations_window.sql
var operationsWindowMigration string

//go:embed 013_firmware.sql
var firmwareMigration string

//go:embed 016_device_profile_replays.sql
var deviceProfileReplaysMigration string

//go:embed 014_device_profiles.sql
var deviceProfilesMigration string

//go:embed 017_jobs.sql
var jobsMigration string

//go:embed 019_job_replays.sql
var jobReplaysMigration string

//go:embed 018_firmware_updates.sql
var firmwareUpdatesMigration string

type Devices struct{ db *sql.DB }
type deviceCursor struct {
	Tenant, Q, Status *string
	After             string
}

func (s *Devices) List(tenant string, f store.DeviceFilter, cursor string, limit int) ([]store.Device, string, error) {
	var c deviceCursor
	if limit < 1 || limit > 500 {
		return nil, "", store.ErrDeviceCursor
	}
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant == nil || c.Q == nil || c.Status == nil || *c.Tenant != tenant || *c.Q != f.Q || *c.Status != f.Status || c.After == "" {
			return nil, "", store.ErrDeviceCursor
		}
	}
	var payload string
	var items []store.Device
	err := s.db.QueryRow("SELECT json_group_array(json(payload)) FROM (SELECT payload FROM devices WHERE tenant=? AND endpoint>? ORDER BY endpoint)", tenant, c.After).Scan(&payload)
	if err != nil {
		return nil, "", err
	}
	if err = json.Unmarshal([]byte(payload), &items); err != nil {
		return nil, "", err
	}
	out := make([]store.Device, 0)
	for _, d := range items {
		if (!strings.Contains(d.Endpoint, f.Q) && !strings.Contains(d.Name, f.Q)) || (f.Status != "" && (f.StatusOf == nil || f.StatusOf(d) != f.Status)) {
			continue
		}
		if len(out) == limit {
			c = deviceCursor{Tenant: &tenant, Q: &f.Q, Status: &f.Status, After: out[len(out)-1].Endpoint}
			b, _ := json.Marshal(c)
			return out, base64.RawURLEncoding.EncodeToString(b), nil
		}
		out = append(out, d)
	}
	return out, "", nil
}

func Open(path string) (*Devices, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	s := &Devices{db: db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Devices) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 0 || version > 19 {
		return fmt.Errorf("unsupported SQLite schema version %d", version)
	}
	if version == 0 {
		if _, err = tx.Exec(migration); err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err = tx.Exec(operationsMigration); err != nil {
			return err
		}
	}
	if version < 3 {
		if _, err = tx.Exec(eventsMigration); err != nil {
			return err
		}
	}
	if version < 4 {
		if _, err = tx.Exec(webhooksSettingsMigration); err != nil {
			return err
		}
	}
	if version < 5 {
		if _, err = tx.Exec(apiKeysMigration); err != nil {
			return err
		}
	}
	if version < 6 {
		if _, err = tx.Exec(securityMigration); err != nil {
			return err
		}
	}
	if version < 7 {
		if _, err = tx.Exec(observationsMigration); err != nil {
			return err
		}
	}
	if version < 8 {
		if _, err = tx.Exec(auditMigration); err != nil {
			return err
		}
	}
	if version < 9 {
		if _, err = tx.Exec(timeSeriesMigration); err != nil {
			return err
		}
	}
	if version < 10 {
		if _, err = tx.Exec(modelsMigration); err != nil {
			return err
		}
	}
	if version < 11 {
		if _, err = tx.Exec(tenantsMigration); err != nil {
			return err
		}
	}
	if version < 12 {
		if _, err = tx.Exec(operationsWindowMigration); err != nil {
			return err
		}
	}
	if version < 13 {
		if _, err = tx.Exec(firmwareMigration); err != nil {
			return err
		}
	}
	if version < 14 {
		if _, err = tx.Exec(deviceProfilesMigration); err != nil {
			return err
		}
	}
	if version < 15 {
		if _, err = tx.Exec(securityUpdatedAtMigration); err != nil {
			return err
		}
	}
	if version < 16 {
		if _, err = tx.Exec(deviceProfileReplaysMigration); err != nil {
			return err
		}
	}
	if version < 17 {
		if _, err = tx.Exec(jobsMigration); err != nil {
			return err
		}
	}
	if version < 18 {
		if _, err = tx.Exec(firmwareUpdatesMigration); err != nil {
			return err
		}
	}
	if version < 19 {
		if _, err = tx.Exec(jobReplaysMigration); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Devices) Close() error { return s.db.Close() }
func (s *Devices) Create(tenant string, d store.Device) (store.Device, error) {
	d.CreatedAt = time.Now().UTC()
	d.UpdatedAt = d.CreatedAt
	d.LastSeenAt = nil
	d.Labels = maps.Collect(maps.All(d.Labels))
	payload, _ := json.Marshal(d) // Device contains only JSON-safe values; timestamps were reset above.
	err := s.db.QueryRow("INSERT INTO devices(tenant,endpoint,payload) VALUES(?,?,?) ON CONFLICT(tenant,endpoint) DO NOTHING RETURNING endpoint", tenant, d.Endpoint, string(payload)).Scan(&d.Endpoint)
	if err == sql.ErrNoRows {
		return store.Device{}, store.ErrDeviceExists
	}
	return d, err
}
func (s *Devices) Get(tenant, endpoint string) (store.Device, error) {
	var payload string
	var d store.Device
	err := s.db.QueryRow("SELECT payload FROM devices WHERE tenant=? AND endpoint=?", tenant, endpoint).Scan(&payload)
	if err == sql.ErrNoRows {
		return d, store.ErrDeviceNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(payload), &d)
	}
	return d, err
}
func (s *Devices) Delete(tenant, endpoint string) error {
	result, err := s.db.Exec("DELETE FROM devices WHERE tenant=? AND endpoint=?", tenant, endpoint)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return store.ErrDeviceNotFound
	}
	return err
}
func (s *Devices) Seen(tenant, endpoint string, at time.Time) {
	stamp := at.UTC().Format("2006-01-02T15:04:05.000000000Z")
	_, err := s.db.Exec("UPDATE devices SET payload=json_set(payload,'$.lastSeenAt',?),last_seen=? WHERE tenant=? AND endpoint=? AND last_seen<?", stamp, stamp, tenant, endpoint, stamp)
	if err != nil {
		log.Printf("sqlite Seen tenant=%q endpoint=%q: %v", tenant, endpoint, err)
	}
}
