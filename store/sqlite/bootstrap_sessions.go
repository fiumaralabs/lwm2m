package sqlite

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"errors"

	"github.com/fiumaralabs/lwm2m/apiv1"
)

type bootstrapSessionPayload struct {
	Session     apiv1.BootstrapSession
	StepsNonNil bool
}

type BootstrapSessions struct{ devices *Devices }

func (s *Devices) BootstrapSessions() *BootstrapSessions { return &BootstrapSessions{devices: s} }

func (s *BootstrapSessions) Append(tenant, endpoint string, session apiv1.BootstrapSession) error {
	e := bytes.TrimSpace(session.Error)
	hasError := len(e) > 0 && !bytes.Equal(e, []byte("null"))
	if (session.Kind != "request" && session.Kind != "pack") || (session.Status != "succeeded" && session.Status != "failed") || (session.Status == "failed") != hasError {
		return errors.New("invalid bootstrap session kind, status or error")
	}
	for _, v := range []json.RawMessage{session.Identity, session.Discover, session.Reads, session.Error} {
		if len(v) > 0 && !json.Valid(v) {
			return errors.New("invalid bootstrap session JSON")
		}
	}
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(bootstrapSessionPayload{Session: session, StepsNonNil: session.Steps != nil}); err != nil {
		return err
	}
	tx, err := s.devices.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO bootstrap_sessions(tenant,endpoint,payload) VALUES(?,?,?)`, tenant, endpoint, b.Bytes()); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM bootstrap_sessions WHERE tenant=? AND endpoint=? AND seq NOT IN (SELECT seq FROM bootstrap_sessions WHERE tenant=? AND endpoint=? ORDER BY seq DESC LIMIT ?)`, tenant, endpoint, tenant, endpoint, apiv1.BootstrapSessionRetention); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *BootstrapSessions) ListForDevice(tenant, endpoint, cursor string, limit int) ([]apiv1.BootstrapSession, string, error) {
	var c struct {
		Tenant, Endpoint *string
		Before           uint64
	}
	if limit < 1 || limit > 500 {
		return nil, "", apiv1.ErrBootstrapSessionCursor
	}
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant == nil || c.Endpoint == nil || *c.Tenant != tenant || *c.Endpoint != endpoint || c.Before == 0 {
			return nil, "", apiv1.ErrBootstrapSessionCursor
		}
	}
	query := `SELECT seq,payload FROM bootstrap_sessions WHERE tenant=? AND endpoint=?`
	args := []any{tenant, endpoint}
	if cursor != "" {
		query += ` AND seq < ?`
		args = append(args, c.Before)
	}
	query += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.devices.db.Query(query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := make([]apiv1.BootstrapSession, 0)
	var last uint64
	for rows.Next() {
		var seq uint64
		var payload []byte
		if err = rows.Scan(&seq, &payload); err != nil {
			return nil, "", err
		}
		if len(out) == limit {
			c.Tenant, c.Endpoint, c.Before = &tenant, &endpoint, last
			b, _ := json.Marshal(c)
			return out, base64.RawURLEncoding.EncodeToString(b), nil
		}
		var v bootstrapSessionPayload
		if err = gob.NewDecoder(bytes.NewReader(payload)).Decode(&v); err != nil {
			return nil, "", err
		}
		if v.StepsNonNil && v.Session.Steps == nil {
			v.Session.Steps = []apiv1.BootstrapSessionStep{}
		}
		out = append(out, v.Session)
		last = seq
	}
	return out, "", rows.Err()
}
