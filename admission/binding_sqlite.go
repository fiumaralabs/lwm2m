package admission

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteBindingStore persists binding tombstones in a SQLite database.
type SQLiteBindingStore struct {
	db        *sql.DB
	owned     bool
	retention time.Duration
	mu        sync.Mutex
}

func OpenSQLiteBindingStore(path string) (*SQLiteBindingStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	s, err := NewSQLiteBindingStoreDB(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	s.owned = true
	return s, nil
}

// NewSQLiteBindingStoreDB uses the caller's database without taking ownership.
// Retention defaults to seven days; at most one optional duration is accepted.
func NewSQLiteBindingStoreDB(db *sql.DB, retention ...time.Duration) (*SQLiteBindingStore, error) {
	if db == nil || len(retention) > 1 {
		return nil, errors.New("admission: invalid database or retention")
	}
	d := DefaultBindingRetention
	if len(retention) == 1 {
		d = retention[0]
	}
	if d <= 0 {
		return nil, errors.New("admission: retention must be positive")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS admission_exchange_bindings (
   exchange_id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, identity_id TEXT NOT NULL,
   fingerprint TEXT NOT NULL, request_digest TEXT NOT NULL, decision_digest TEXT NOT NULL,
   applied INTEGER NOT NULL CHECK(applied = 1))`)
	if err != nil {
		return nil, err
	}
	// Legacy rows have no authenticated expiry and remain permanent barriers.
	for _, col := range []struct{ name, typ string }{{"expires_at", "TEXT"}, {"first_seen", "INTEGER"}} {
		rows, err := db.Query(`PRAGMA table_info(admission_exchange_bindings)`)
		if err != nil {
			return nil, err
		}
		found := false
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var defaultValue any
			if err = rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
				rows.Close()
				return nil, err
			}
			if name == col.name {
				found = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if !found {
			if _, err = db.Exec(`ALTER TABLE admission_exchange_bindings ADD COLUMN ` + col.name + ` ` + col.typ); err != nil {
				return nil, err
			}
		}
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS admission_receipts (
   exchange_id TEXT PRIMARY KEY, decision_id TEXT NOT NULL, decision_digest TEXT NOT NULL,
   issued_at TEXT NOT NULL, expires_at TEXT NOT NULL, committed_at INTEGER NOT NULL)`)
	if err != nil {
		return nil, err
	}
	// Add receipt metadata without invalidating databases created by older versions.
	for _, col := range []string{"kind", "reservation_id"} {
		rows, err := db.Query(`PRAGMA table_info(admission_receipts)`)
		if err != nil {
			return nil, err
		}
		found := false
		for rows.Next() {
			var cid, nn, pk int
			var name, typ string
			var def any
			if err = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
				rows.Close()
				return nil, err
			}
			found = found || name == col
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if !found {
			def := "''"
			if col == "kind" {
				def = "'admitted'"
			}
			if _, err = db.Exec(`ALTER TABLE admission_receipts ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ` + def); err != nil {
				return nil, err
			}
		}
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS admission_identities (tenant_id TEXT NOT NULL, identity_id TEXT NOT NULL, fingerprint TEXT NOT NULL, PRIMARY KEY (tenant_id, identity_id, fingerprint))`); err != nil {
		return nil, err
	}
	// Recover identities from unpruned committed receipts, not bare replay bindings.
	if _, err = db.Exec(`INSERT OR IGNORE INTO admission_identities SELECT b.tenant_id,b.identity_id,b.fingerprint FROM admission_exchange_bindings b JOIN admission_receipts r ON r.exchange_id=b.exchange_id WHERE b.tenant_id<>'' AND b.identity_id<>'' AND b.fingerprint<>'' AND r.kind='admitted'`); err != nil {
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS admission_pending_exchanges (exchange_id TEXT PRIMARY KEY, request_body BLOB NOT NULL, request_digest TEXT NOT NULL)`); err != nil {
		return nil, err
	}
	return &SQLiteBindingStore{db: db, retention: d}, nil
}

// Close closes databases opened by OpenSQLiteBindingStore, not borrowed databases.
func (s *SQLiteBindingStore) Close() error {
	if s.owned {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteBindingStore) BindAndMarkApplied(ctx context.Context, b Binding) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO admission_exchange_bindings (exchange_id,tenant_id,identity_id,fingerprint,request_digest,decision_digest,applied) VALUES (?, ?, ?, ?, ?, ?, 1) ON CONFLICT(exchange_id) DO NOTHING`, b.ExchangeID, b.TenantID, b.IdentityID, b.Fingerprint, b.RequestDigest, b.DecisionDigest)
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

func (s *SQLiteBindingStore) exchangeCommitted(ctx context.Context, id string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admission_exchange_bindings WHERE exchange_id=?)`, id).Scan(&exists)
	return exists != 0, err
}
