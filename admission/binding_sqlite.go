package admission

import (
	"context"
	"database/sql"

	_ "modernc.org/sqlite"
)

// SQLiteBindingStore persists binding tombstones in a SQLite database.
type SQLiteBindingStore struct{ db *sql.DB }

func OpenSQLiteBindingStore(path string) (*SQLiteBindingStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err == nil {
		_, err = db.Exec(`CREATE TABLE IF NOT EXISTS admission_exchange_bindings (
   exchange_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, identity_id TEXT NOT NULL,
   fingerprint TEXT NOT NULL, request_digest TEXT NOT NULL, decision_digest TEXT NOT NULL,
   applied INTEGER NOT NULL CHECK(applied = 1))`)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteBindingStore{db: db}, nil
}
func (s *SQLiteBindingStore) Close() error { return s.db.Close() }
func (s *SQLiteBindingStore) BindAndMarkApplied(ctx context.Context, b Binding) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO admission_exchange_bindings VALUES (?, ?, ?, ?, ?, ?, 1) ON CONFLICT(exchange_id) DO NOTHING`, b.ExchangeID, b.TenantID, b.IdentityID, b.Fingerprint, b.RequestDigest, b.DecisionDigest)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		var old Binding
		old.ExchangeID = b.ExchangeID
		err = tx.QueryRowContext(ctx, `SELECT tenant_id, identity_id, fingerprint, request_digest, decision_digest FROM admission_exchange_bindings WHERE exchange_id = ?`, b.ExchangeID).Scan(&old.TenantID, &old.IdentityID, &old.Fingerprint, &old.RequestDigest, &old.DecisionDigest)
		if err != nil {
			return false, err
		}
		if old != b {
			return false, ErrBindingConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n == 0, nil
}
