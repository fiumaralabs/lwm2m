package admission

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"modernc.org/sqlite"
)

// CommitAdmitted serializes retries. Memory callbacks receive nil, cannot use
// SQL transactions and must not expose side effects on error or panic.
func (s *MemoryBindingStore) CommitAdmitted(ctx context.Context, b Binding, r Receipt, effect func(*sql.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.DecisionDigest != b.DecisionDigest {
		return ErrBindingConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if old, ok := s.bindings[b.ExchangeID]; ok {
		previous, hasReceipt := s.receipts[b.ExchangeID]
		if old != b || !hasReceipt || previous.DecisionID != r.DecisionID || previous.DecisionDigest != r.DecisionDigest || !previous.IssuedAt.Equal(r.IssuedAt) || !previous.ExpiresAt.Equal(r.ExpiresAt) {
			return ErrBindingConflict
		}
		return nil
	}
	if effect == nil {
		return errors.New("admission: nil effect")
	}
	if err := effect(nil); err != nil {
		return err
	}
	s.bindings[b.ExchangeID] = b
	s.receipts[b.ExchangeID] = r
	return nil
}

// CommitAdmitted commits the binding, receipt and effect in one SQLite
// transaction. The callback must use only the provided transaction for effects.
// A repeated identical commit is a no-op; a different binding or receipt fails.
func (s *SQLiteBindingStore) CommitAdmitted(ctx context.Context, b Binding, r Receipt, effect func(*sql.Tx) error) error {
	if r.DecisionDigest != b.DecisionDigest {
		return ErrBindingConflict
	}
	if effect == nil {
		return errors.New("admission: nil effect")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var tx *sql.Tx
	var result sql.Result
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for {
		tx, err = s.db.BeginTx(ctx, nil)
		if err == nil {
			result, err = tx.ExecContext(ctx, `INSERT INTO admission_exchange_bindings VALUES (?, ?, ?, ?, ?, ?, 1) ON CONFLICT(exchange_id) DO NOTHING`, b.ExchangeID, b.TenantID, b.IdentityID, b.Fingerprint, b.RequestDigest, b.DecisionDigest)
			if err == nil {
				break
			}
			_ = tx.Rollback()
		}
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || (sqliteErr.Code()&0xff != 5 && sqliteErr.Code()&0xff != 6) || time.Now().After(deadline) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer tx.Rollback()
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var old Binding
		var decisionID, digest, issued, expires string
		old.ExchangeID = b.ExchangeID
		err = tx.QueryRowContext(ctx, `SELECT b.tenant_id,b.identity_id,b.fingerprint,b.request_digest,b.decision_digest,r.decision_id,r.decision_digest,r.issued_at,r.expires_at FROM admission_exchange_bindings b JOIN admission_receipts r ON r.exchange_id=b.exchange_id WHERE b.exchange_id=?`, b.ExchangeID).Scan(&old.TenantID, &old.IdentityID, &old.Fingerprint, &old.RequestDigest, &old.DecisionDigest, &decisionID, &digest, &issued, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBindingConflict
		}
		if err != nil {
			return err
		}
		if old != b || decisionID != r.DecisionID || digest != r.DecisionDigest || issued != r.IssuedAt.UTC().Format(time.RFC3339Nano) || expires != r.ExpiresAt.UTC().Format(time.RFC3339Nano) {
			return ErrBindingConflict
		}
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admission_receipts VALUES (?, ?, ?, ?, ?, ?)`, b.ExchangeID, r.DecisionID, r.DecisionDigest, r.IssuedAt.UTC().Format(time.RFC3339Nano), r.ExpiresAt.UTC().Format(time.RFC3339Nano), time.Now().UnixNano())
	if err != nil {
		return err
	}
	if err = effect(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Prune retains all barriers: CommitAdmitted does not yet enforce exchange
// expiry, so no receipt is safe to delete, even after its retention period.
// The caller's context is still honored. Retention remains a lower bound, not
// a promise that records will be removed after that time.
func (s *SQLiteBindingStore) Prune(ctx context.Context, now time.Time) error {
	return ctx.Err()
}
