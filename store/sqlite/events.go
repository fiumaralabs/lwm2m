package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/id"
	"github.com/fiumaralabs/lwm2m/store"
)

// Events shares its owning Devices store's connection and lifetime.
// Configure Clock and Retention before use; defaults are UTC now and seven days.
type Events struct {
	db        *sql.DB
	Clock     func() time.Time
	Retention time.Duration
}

var _ store.Events = (*Events)(nil)

func (s *Devices) Events() *Events { return &Events{db: s.db} }

func (s *Events) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}

func (s *Events) trim(tx *sql.Tx, tenant string, now time.Time) error {
	retention := s.Retention
	if retention <= 0 {
		retention = 7 * 24 * time.Hour
	}
	cutoff := now.Add(-retention).UnixNano()
	if _, err := tx.Exec(`UPDATE event_heads SET low=max(low,coalesce((SELECT max(sequence) FROM events WHERE tenant=? AND stamp<?),0)) WHERE tenant=?`, tenant, cutoff, tenant); err != nil {
		return err
	}
	_, err := tx.Exec("DELETE FROM events WHERE tenant=? AND stamp<?", tenant, cutoff)
	return err
}

func (s *Events) Append(tenant, kind, device string, data json.RawMessage) (store.Event, error) {
	e := store.Event{SpecVersion: "1.0", ID: id.New("evt"), Source: "/tenants/" + tenant, Type: kind, Time: s.now(), Subject: device, DataContentType: "application/json", Data: append(json.RawMessage(nil), data...)}
	tx, err := s.db.Begin()
	if err != nil {
		return store.Event{}, err
	}
	defer tx.Rollback()
	var sequence int64
	err = tx.QueryRow(`INSERT INTO event_heads(tenant,sequence) VALUES(?,1) ON CONFLICT(tenant) DO UPDATE SET sequence=sequence+1 RETURNING sequence`, tenant).Scan(&sequence)
	if err != nil {
		return store.Event{}, err
	}
	e.Sequence = fmt.Sprintf("%018d", sequence)
	envelope := e
	envelope.Data = nil // Keep the original payload bytes, including nil, in the BLOB.
	body, err := json.Marshal(envelope)
	if err != nil {
		return store.Event{}, err
	}
	if err := s.trim(tx, tenant, e.Time); err != nil {
		return store.Event{}, err
	}
	if _, err := tx.Exec("INSERT INTO events(tenant,sequence,type,device,stamp,body,data) VALUES(?,?,?,?,?,?,?)", tenant, sequence, kind, device, e.Time.UnixNano(), string(body), []byte(e.Data)); err != nil {
		return store.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return store.Event{}, err
	}
	return e, nil
}

func (s *Events) List(tenant string, f store.EventFilter) ([]store.Event, bool, error) {
	if f.Limit < 1 || f.After != "" && !store.ValidSequence(f.After) || f.Before != "" && !store.ValidSequence(f.Before) {
		return nil, false, store.ErrEventQuery
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if err := s.trim(tx, tenant, s.now()); err != nil {
		return nil, false, err
	}
	var low int64
	if err := tx.QueryRow("SELECT coalesce((SELECT low FROM event_heads WHERE tenant=?),0)", tenant).Scan(&low); err != nil {
		return nil, false, err
	}
	query, args := "SELECT body,data FROM events WHERE tenant=?", []any{tenant}
	for _, bound := range []struct{ value, clause string }{{f.After, " AND sequence>CAST(? AS INTEGER)"}, {f.Before, " AND sequence<CAST(? AS INTEGER)"}, {f.Device, " AND device=?"}} {
		if bound.value != "" {
			query += bound.clause
			args = append(args, bound.value)
		}
	}
	if f.Type != "" {
		var clauses []string
		for _, pattern := range strings.Split(f.Type, ",") {
			if strings.HasSuffix(pattern, "*") {
				prefix := strings.TrimSuffix(pattern, "*")
				clauses = append(clauses, "substr(type,1,length(?))=?")
				args = append(args, prefix, prefix)
			} else {
				clauses = append(clauses, "type=?")
				args = append(args, pattern)
			}
		}
		query += " AND (" + strings.Join(clauses, " OR ") + ")"
	}
	query += " ORDER BY sequence"
	if f.Newest {
		query += " DESC"
	}
	query += " LIMIT ?"
	args = append(args, f.Limit)
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := make([]store.Event, 0)
	for rows.Next() {
		var body string
		var data []byte
		var e store.Event
		if err := rows.Scan(&body, &data); err != nil {
			return nil, false, err
		}
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			return nil, false, err
		}
		e.Data = data
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return out, f.After != "" && f.After < fmt.Sprintf("%018d", low), nil
}
