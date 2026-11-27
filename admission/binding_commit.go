package admission

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"modernc.org/sqlite"
)

// ReceiptExpiredError reports a receipt that is no longer valid.
type ReceiptExpiredError struct{ ExpiresAt time.Time }

func (e *ReceiptExpiredError) Error() string { return "admission: receipt expired" }

type receiptClockKey struct{}

// WithReceiptClock injects a trusted clock for receipt validation. Production
// callers should use the default system clock, not untrusted request input.
func WithReceiptClock(ctx context.Context, clock func() time.Time) context.Context {
	return context.WithValue(ctx, receiptClockKey{}, clock)
}

func receiptNow(ctx context.Context) time.Time {
	if clock, ok := ctx.Value(receiptClockKey{}).(func() time.Time); ok {
		return clock()
	}
	return time.Now()
}

func checkReceiptExpiry(ctx context.Context, r Receipt) error {
	if !r.ExpiresAt.After(receiptNow(ctx)) {
		return &ReceiptExpiredError{ExpiresAt: r.ExpiresAt}
	}
	return nil
}

// CommitAdmitted serializes retries. Memory callbacks receive nil, cannot use
// SQL transactions and must not expose side effects on error or panic.
func (s *MemoryBindingStore) CommitAdmitted(ctx context.Context, b Binding, r Receipt, effect func(*sql.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkReceiptExpiry(ctx, r); err != nil {
		return err
	}
	if r.DecisionDigest != b.DecisionDigest {
		return ErrBindingConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkReceiptExpiry(ctx, r); err != nil {
		return err
	}
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
	if err := checkReceiptExpiry(ctx, r); err != nil {
		return err
	}
	if err := effect(nil); err != nil {
		return err
	}
	s.bindings[b.ExchangeID] = b
	s.receipts[b.ExchangeID] = r
	s.admitted[admittedIdentity{b.TenantID, b.IdentityID, b.Fingerprint}] = struct{}{}
	return nil
}

// CommitAdmitted commits the binding, receipt and effect in one SQLite
// transaction. The callback must use only the provided transaction for effects.
// A repeated identical commit is a no-op; a different binding or receipt fails.
func (s *SQLiteBindingStore) CommitAdmitted(ctx context.Context, b Binding, r Receipt, effect func(*sql.Tx) error) error {
	if err := checkReceiptExpiry(ctx, r); err != nil {
		return err
	}
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
			result, err = tx.ExecContext(ctx, `INSERT INTO admission_exchange_bindings (exchange_id,tenant_id,identity_id,fingerprint,request_digest,decision_digest,applied,expires_at,first_seen) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?) ON CONFLICT(exchange_id) DO NOTHING`, b.ExchangeID, b.TenantID, b.IdentityID, b.Fingerprint, b.RequestDigest, b.DecisionDigest, r.ExpiresAt.UTC().Format(time.RFC3339Nano), receiptNow(ctx).UnixNano())
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
	if err := checkReceiptExpiry(ctx, r); err != nil {
		return err
	}
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
	_, err = tx.ExecContext(ctx, `INSERT INTO admission_receipts (exchange_id,decision_id,decision_digest,issued_at,expires_at,committed_at) VALUES (?, ?, ?, ?, ?, ?)`, b.ExchangeID, r.DecisionID, r.DecisionDigest, r.IssuedAt.UTC().Format(time.RFC3339Nano), r.ExpiresAt.UTC().Format(time.RFC3339Nano), time.Now().UnixNano())
	if err != nil {
		return err
	}
	if err = checkReceiptExpiry(ctx, r); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO admission_identities (tenant_id,identity_id,fingerprint) VALUES (?,?,?)`, b.TenantID, b.IdentityID, b.Fingerprint); err != nil {
		return err
	}
	if err = effect(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Prune removes expired receipt and binding payloads only after the retention
// boundary. A minimal per-exchange tombstone is kept forever, so storage
// grows by one small row per exchange. Legacy rows without expiry are retained.
func (s *SQLiteBindingStore) Prune(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT exchange_id, expires_at FROM admission_exchange_bindings WHERE expires_at IS NOT NULL`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		expiry, parseErr := time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			rows.Close()
			return parseErr
		}
		if now.After(expiry.Add(s.retention)) {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `DELETE FROM admission_receipts WHERE exchange_id=?`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE admission_exchange_bindings SET tenant_id='',identity_id='',fingerprint='',request_digest='',expires_at=NULL WHERE exchange_id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
