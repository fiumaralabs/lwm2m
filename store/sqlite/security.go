package sqlite

import (
	"database/sql"
	"errors"

	"github.com/fiumaralabs/lwm2m/server"
	sqlite3 "modernc.org/sqlite"
	sqlite3lib "modernc.org/sqlite/lib"
)

// SecurityStore persists server credentials in the same database as Devices.
// PSK keys are stored unencrypted; protect the database and its backups.
// The SecurityStore interface cannot report lookup or removal errors. These
// methods fail closed on database errors; All returns nil on a read failure.
type SecurityStore struct{ db *sql.DB }

func (s *Devices) Security() *SecurityStore { return &SecurityStore{db: s.db} }

func (s *SecurityStore) Put(si server.SecurityInfo) error {
	if len(si.PublicKey) != 0 {
		return server.ErrRPKUnsupported
	}
	// A single statement makes replacement and the uniqueness check atomic.
	var endpoint string
	err := s.db.QueryRow(`INSERT INTO security(endpoint,psk_identity,psk_key,x509) VALUES(?,?,?,?)
 ON CONFLICT(endpoint) DO UPDATE SET psk_identity=excluded.psk_identity,psk_key=excluded.psk_key,x509=excluded.x509
 ON CONFLICT DO NOTHING RETURNING endpoint`, si.Endpoint, si.PSKIdentity, si.PSKKey, si.X509).Scan(&endpoint)
	if errors.Is(err, sql.ErrNoRows) {
		return server.ErrDuplicatePSKIdentity
	}
	var sqliteErr *sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3lib.SQLITE_CONSTRAINT_UNIQUE {
		return server.ErrDuplicatePSKIdentity
	}
	// Do not return database errors that may contain credential values.
	if err != nil {
		return errors.New("sqlite: security credential write failed")
	}
	return nil
}

type securityScanner interface{ Scan(...any) error }

func scanSecurity(row securityScanner) (server.SecurityInfo, bool) {
	var v server.SecurityInfo
	if err := row.Scan(&v.Endpoint, &v.PSKIdentity, &v.PSKKey, &v.X509); err != nil {
		return server.SecurityInfo{}, false
	}
	return v, true
}
func (s *SecurityStore) ByEndpoint(ep string) (server.SecurityInfo, bool) {
	return scanSecurity(s.db.QueryRow("SELECT endpoint,psk_identity,psk_key,x509 FROM security WHERE endpoint=?", ep))
}
func (s *SecurityStore) ByPSKIdentity(id string) (server.SecurityInfo, bool) {
	if id == "" {
		return server.SecurityInfo{}, false
	}
	return scanSecurity(s.db.QueryRow("SELECT endpoint,psk_identity,psk_key,x509 FROM security WHERE psk_identity=?", id))
}
func (s *SecurityStore) Remove(ep string) (server.SecurityInfo, bool) {
	return scanSecurity(s.db.QueryRow("DELETE FROM security WHERE endpoint=? RETURNING endpoint,psk_identity,psk_key,x509", ep))
}
func (s *SecurityStore) All() []server.SecurityInfo {
	rows, err := s.db.Query("SELECT endpoint,psk_identity,psk_key,x509 FROM security ORDER BY endpoint")
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]server.SecurityInfo, 0)
	for rows.Next() {
		v, ok := scanSecurity(rows)
		if !ok {
			return nil
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		return nil
	}
	return out
}

var _ server.SecurityStore = (*SecurityStore)(nil)
