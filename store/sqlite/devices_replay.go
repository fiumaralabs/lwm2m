package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"
)

//go:embed 027_device_replays.sql
var deviceReplayMigration string

type deviceReplayKey struct{ tenant, key string }

// Coordinate all SQLite handles in this process, including aliases for the same
// database. Keys for unrelated databases may briefly serialize as well.
var deviceReplays = struct {
	sync.Mutex
	pending map[deviceReplayKey]chan struct{}
}{pending: make(map[deviceReplayKey]chan struct{})}

// ReplayDeviceCreate serializes same-key requests across SQLite handles in-process.
func (s *Devices) ReplayDeviceCreate(ctx context.Context, tenant, key string, run func() (int, http.Header, []byte)) (int, http.Header, []byte, error) {
	k := deviceReplayKey{tenant, key}
	for {
		deviceReplays.Lock()
		if pending := deviceReplays.pending[k]; pending != nil {
			deviceReplays.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return 0, nil, nil, ctx.Err()
			}
		}
		pending := make(chan struct{})
		deviceReplays.pending[k] = pending
		deviceReplays.Unlock()
		defer func() { deviceReplays.Lock(); delete(deviceReplays.pending, k); close(pending); deviceReplays.Unlock() }()
		break
	}
	var status int
	var headerData, body []byte
	err := s.db.QueryRowContext(ctx, `SELECT status, header, body FROM device_replays WHERE tenant=? AND key=? AND expires>?`, tenant, key, time.Now().UnixNano()).Scan(&status, &headerData, &body)
	if err == nil {
		var header http.Header
		if err = json.Unmarshal(headerData, &header); err != nil {
			return 0, nil, nil, err
		}
		return status, header, body, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil, err
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM device_replays WHERE expires<=?`, time.Now().UnixNano()); err != nil {
		return 0, nil, nil, err
	}
	// The callback creates the device and event without a held SQLite transaction:
	// a crash or persistence failure before this response is saved can lose replay.
	status, header, body := run()
	headerData, err = json.Marshal(header)
	if err != nil {
		return 0, nil, nil, err
	}
	if body == nil {
		body = []byte{}
	}
	// Once the callback has run, a disconnected client must not cancel replay persistence.
	_, err = s.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO device_replays(tenant,key,status,header,body,expires) VALUES(?,?,?,?,?,?) ON CONFLICT(tenant,key) DO UPDATE SET status=excluded.status, header=excluded.header, body=excluded.body, expires=excluded.expires`, tenant, key, status, headerData, body, time.Now().Add(24*time.Hour).UnixNano())
	if err != nil {
		return 0, nil, nil, err
	}
	return status, header.Clone(), append([]byte(nil), body...), nil
}
