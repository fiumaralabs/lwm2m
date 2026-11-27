package admission

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type admittedIdentity struct{ tenant, identity, fingerprint string }

// ErrNotAdmitted indicates that the exact authenticated identity has not been admitted.
var ErrNotAdmitted = errors.New("admission: identity not admitted")

// IsAdmitted reports whether this exact identity was committed as admitted.
func (s *MemoryBindingStore) IsAdmitted(ctx context.Context, tenant, identity, fingerprint string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, ok := s.admitted[admittedIdentity{tenant, identity, fingerprint}]
	return ok, nil
}

// IsAdmitted reports whether this exact identity was committed as admitted.
func (s *SQLiteBindingStore) IsAdmitted(ctx context.Context, tenant, identity, fingerprint string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM admission_identities WHERE tenant_id=? AND identity_id=? AND fingerprint=?`, tenant, identity, fingerprint).Scan(&n)
	return n != 0, err
}

// CommitExisting records a subsequent exchange and its effect. Memory callbacks
// receive nil and must not expose side effects on failure or panic.
func (s *MemoryBindingStore) CommitExisting(ctx context.Context, exchangeID, tenant, identity, fingerprint, requestDigest string, effect func(*sql.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if requestDigest == "" {
		return errors.New("admission: empty request digest")
	}
	if effect == nil {
		return errors.New("admission: nil effect")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	key := admittedIdentity{tenant, identity, fingerprint}
	if _, ok := s.admitted[key]; !ok {
		return ErrNotAdmitted
	}
	if old, ok := s.existing[exchangeID]; ok {
		if old != key || s.bindings[exchangeID].RequestDigest != requestDigest {
			return ErrBindingConflict
		}
		return nil
	}
	if _, ok := s.bindings[exchangeID]; ok {
		return ErrBindingConflict
	}
	if err := effect(nil); err != nil {
		return err
	}
	s.existing[exchangeID] = key
	s.bindings[exchangeID] = Binding{ExchangeID: exchangeID, TenantID: tenant, IdentityID: identity, Fingerprint: fingerprint, RequestDigest: requestDigest}
	s.receipts[exchangeID] = Receipt{Kind: "existing_admission"}
	return nil
}

// CommitExisting atomically records the receipt and effect. The callback must
// use only the provided transaction. The requestDigest must identify the authenticated
// canonical request; identical exchange retries are no-ops.
func (s *SQLiteBindingStore) CommitExisting(ctx context.Context, exchangeID, tenant, identity, fingerprint, requestDigest string, effect func(*sql.Tx) error) error {
	if requestDigest == "" {
		return errors.New("admission: empty request digest")
	}
	if effect == nil {
		return errors.New("admission: nil effect")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Acquire the write lock before checking identity and exchange state.
	result, err := tx.ExecContext(ctx, `INSERT INTO admission_exchange_bindings (exchange_id,tenant_id,identity_id,fingerprint,request_digest,decision_digest,applied) SELECT ?,?,?,?,?,'',1 WHERE EXISTS (SELECT 1 FROM admission_identities WHERE tenant_id=? AND identity_id=? AND fingerprint=?) ON CONFLICT(exchange_id) DO NOTHING`, exchangeID, tenant, identity, fingerprint, requestDigest, tenant, identity, fingerprint)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var admitted int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM admission_identities WHERE tenant_id=? AND identity_id=? AND fingerprint=?`, tenant, identity, fingerprint).Scan(&admitted); err != nil {
			return err
		}
		if admitted == 0 {
			return ErrNotAdmitted
		}
		var t, i, f, digest, kind string
		err = tx.QueryRowContext(ctx, `SELECT b.tenant_id,b.identity_id,b.fingerprint,b.request_digest,r.kind FROM admission_exchange_bindings b JOIN admission_receipts r ON r.exchange_id=b.exchange_id WHERE b.exchange_id=?`, exchangeID).Scan(&t, &i, &f, &digest, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBindingConflict
		}
		if err != nil {
			return err
		}
		if t != tenant || i != identity || f != fingerprint || digest != requestDigest || kind != "existing_admission" {
			return ErrBindingConflict
		}
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO admission_receipts (exchange_id,decision_id,decision_digest,issued_at,expires_at,committed_at,kind,reservation_id) VALUES (?,'','','','',?,'existing_admission','')`, exchangeID, time.Now().UnixNano())
	if err != nil {
		return err
	}
	if err = effect(tx); err != nil {
		return err
	}
	return tx.Commit()
}
