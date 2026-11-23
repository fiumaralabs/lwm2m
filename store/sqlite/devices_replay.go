package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

//go:embed 027_device_replays.sql
var deviceReplayMigration string

type deviceReplayKey struct{ tenant, key string }

// ReplayDeviceCreate serializes same-key requests within this store instance.
func (s *Devices) ReplayDeviceCreate(ctx context.Context, tenant, key string, run func() (int, http.Header, []byte)) (int, http.Header, []byte, error) {
	k := deviceReplayKey{tenant, key}
	for {
		s.replayMu.Lock()
		if s.replayPending == nil {
			s.replayPending = make(map[deviceReplayKey]chan struct{})
		}
		if pending := s.replayPending[k]; pending != nil {
			s.replayMu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return 0, nil, nil, ctx.Err()
			}
		}
		pending := make(chan struct{})
		s.replayPending[k] = pending
		s.replayMu.Unlock()
		defer func() { s.replayMu.Lock(); delete(s.replayPending, k); close(pending); s.replayMu.Unlock() }()
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO device_replays(tenant,key,status,header,body,expires) VALUES(?,?,?,?,?,?) ON CONFLICT(tenant,key) DO UPDATE SET status=excluded.status, header=excluded.header, body=excluded.body, expires=excluded.expires`, tenant, key, status, headerData, body, time.Now().Add(24*time.Hour).UnixNano())
	if err != nil {
		return 0, nil, nil, err
	}
	return status, header.Clone(), append([]byte(nil), body...), nil
}
