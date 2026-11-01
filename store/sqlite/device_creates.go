package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

//go:embed 026_device_create_outcomes.sql
var deviceCreateMigration string

var _ store.DeviceCreateStore = (*Devices)(nil)

// withDeviceCreate serializes the read and writes even across separate DB handles.
func (s *Devices) withDeviceCreate(ctx context.Context, fn func(*sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if err = fn(conn); err != nil {
		return err
	}
	if s.deviceCreateHook != nil {
		if err = s.deviceCreateHook("before-commit"); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

func readDeviceCreate(ctx context.Context, conn *sql.Conn, tenant, id string) (store.DeviceCreateOutcome, bool, error) {
	var o store.DeviceCreateOutcome
	var payload []byte
	err := conn.QueryRowContext(ctx, `SELECT payload FROM device_create_outcomes WHERE tenant=? AND operation_id=?`, tenant, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return o, false, nil
	}
	if err != nil {
		return o, false, err
	}
	err = json.Unmarshal(payload, &o)
	return o, true, err
}

func writeDeviceCreate(ctx context.Context, conn *sql.Conn, o store.DeviceCreateOutcome) error {
	payload, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO device_create_outcomes(tenant,operation_id,payload) VALUES(?,?,?)`, o.Tenant, o.OperationID, payload)
	return err
}

func newDeviceCreateOutcome(tenant, id, fingerprint, status, reason string) store.DeviceCreateOutcome {
	now := time.Now().UTC()
	return store.DeviceCreateOutcome{Tenant: tenant, OperationID: id, Fingerprint: fingerprint, Status: status, Reason: reason, CreatedAt: now, CompletedAt: now}
}

func checkDeviceCreateReplay(o store.DeviceCreateOutcome, fingerprint string) error {
	if o.Reason != "fenced" && o.Fingerprint != fingerprint {
		return store.ErrOperationIDReused
	}
	return nil
}

func (s *Devices) CreateDeviceWithOutcome(ctx context.Context, tenant, id, fingerprint string, d store.Device) (store.DeviceCreateOutcome, bool, error) {
	var out store.DeviceCreateOutcome
	var created bool
	err := s.withDeviceCreate(ctx, func(conn *sql.Conn) error {
		existing, ok, err := readDeviceCreate(ctx, conn, tenant, id)
		if err != nil {
			return err
		}
		if ok {
			out = existing
			return checkDeviceCreateReplay(out, fingerprint)
		}
		var exists bool
		if err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE endpoint=?)`, d.Endpoint).Scan(&exists); err != nil {
			return err
		}
		if exists {
			out = newDeviceCreateOutcome(tenant, id, fingerprint, "not-created", "endpoint-conflict")
		} else {
			now := time.Now().UTC()
			d.CreatedAt, d.UpdatedAt, d.LastSeenAt = now, now, nil
			payload, e := json.Marshal(d)
			if e != nil {
				return e
			}
			if _, e = conn.ExecContext(ctx, `INSERT INTO devices(tenant,endpoint,payload) VALUES(?,?,?)`, tenant, d.Endpoint, payload); e != nil {
				return e
			}
			if s.deviceCreateHook != nil {
				if e = s.deviceCreateHook("after-device"); e != nil {
					return e
				}
			}
			out = newDeviceCreateOutcome(tenant, id, fingerprint, "committed", "")
			out.Endpoint = d.Endpoint
			created = true
		}
		return writeDeviceCreate(ctx, conn, out)
	})
	if err != nil {
		return store.DeviceCreateOutcome{}, false, err
	}
	return out, created, nil
}

func (s *Devices) RecordDeviceCreateNotCreated(ctx context.Context, tenant, id, fingerprint, reason string) (store.DeviceCreateOutcome, error) {
	var out store.DeviceCreateOutcome
	err := s.withDeviceCreate(ctx, func(conn *sql.Conn) error {
		existing, ok, err := readDeviceCreate(ctx, conn, tenant, id)
		if err != nil {
			return err
		}
		if ok {
			out = existing
			return checkDeviceCreateReplay(out, fingerprint)
		}
		out = newDeviceCreateOutcome(tenant, id, fingerprint, "not-created", reason)
		return writeDeviceCreate(ctx, conn, out)
	})
	if err != nil {
		return store.DeviceCreateOutcome{}, err
	}
	return out, nil
}

func (s *Devices) GetOrFenceDeviceCreate(ctx context.Context, tenant, id string) (store.DeviceCreateOutcome, error) {
	var out store.DeviceCreateOutcome
	err := s.withDeviceCreate(ctx, func(conn *sql.Conn) error {
		existing, ok, err := readDeviceCreate(ctx, conn, tenant, id)
		if err != nil {
			return err
		}
		if ok {
			out = existing
			return nil
		}
		out = newDeviceCreateOutcome(tenant, id, "", "not-created", "fenced")
		return writeDeviceCreate(ctx, conn, out)
	})
	if err != nil {
		return store.DeviceCreateOutcome{}, err
	}
	return out, nil
}

func (s *Devices) DeleteDeviceCreate(ctx context.Context, tenant, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM device_create_outcomes WHERE tenant=? AND operation_id=?`, tenant, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
