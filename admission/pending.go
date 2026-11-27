package admission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ExchangeStatus is the locally known outcome of an exchange.
type ExchangeStatus string

const (
	ExchangePending  ExchangeStatus = "pending"
	ExchangeAccepted ExchangeStatus = "accepted"
)

// ErrExchangeUnknown means there is no current, authoritative outcome.
var ErrExchangeUnknown = errors.New("admission: exchange unknown")

// PendingBindingStore persists requests before dispatch. SavePending atomically
// inserts or returns the original matching request, rejecting conflicting data.
// The returned body must be an owned copy and must match its stored digest.
type PendingBindingStore interface {
	SavePending(context.Context, string, []byte, string) ([]byte, error)
	LookupExchange(context.Context, string) (ExchangeStatus, Receipt, error)
}

type pendingRequest struct {
	body   []byte
	digest string
}

// samePending permits a regenerated observation time but no other request change.
func samePending(old, body []byte, oldDigest string) bool {
	sum := sha256.Sum256(old)
	if fmt.Sprintf("%x", sum) != oldDigest {
		return false
	}
	var previous, next map[string]json.RawMessage
	if json.Unmarshal(old, &previous) != nil || json.Unmarshal(body, &next) != nil || len(previous) != len(next) {
		return false
	}
	for key, value := range previous {
		if key != "observed_at" && !bytes.Equal(value, next[key]) {
			return false
		}
	}
	return true
}

func (s *MemoryBindingStore) SavePending(ctx context.Context, id string, body []byte, digest string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = make(map[string]pendingRequest)
	}
	if p, ok := s.pending[id]; ok {
		if !samePending(p.body, body, p.digest) {
			return nil, ErrBindingConflict
		}
		return append([]byte(nil), p.body...), nil
	}
	if _, ok := s.bindings[id]; ok {
		return nil, ErrBindingConflict
	}
	s.pending[id] = pendingRequest{append([]byte(nil), body...), digest}
	return body, nil
}

func (s *SQLiteBindingStore) SavePending(ctx context.Context, id string, body []byte, digest string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admission_exchange_bindings WHERE exchange_id=?)`, id).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrBindingConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO admission_pending_exchanges(exchange_id,request_body,request_digest) VALUES (?,?,?) ON CONFLICT(exchange_id) DO NOTHING`, id, body, digest); err != nil {
		return nil, err
	}
	var old []byte
	var oldDigest string
	if err = tx.QueryRowContext(ctx, `SELECT request_body,request_digest FROM admission_pending_exchanges WHERE exchange_id=?`, id).Scan(&old, &oldDigest); err != nil {
		return nil, err
	}
	if !samePending(old, body, oldDigest) {
		return nil, ErrBindingConflict
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return old, nil
}

// LookupExchange returns a pending exchange or a current accepted receipt.
// Absence and expired receipts are unknown, never terminal rejection.
func (s *MemoryBindingStore) LookupExchange(ctx context.Context, id string) (ExchangeStatus, Receipt, error) {
	if err := ctx.Err(); err != nil {
		return "", Receipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.receipts[id]; ok {
		if checkReceiptExpiry(ctx, r) != nil {
			return "", Receipt{}, ErrExchangeUnknown
		}
		return ExchangeAccepted, r, nil
	}
	if _, ok := s.bindings[id]; ok {
		return "", Receipt{}, ErrExchangeUnknown
	}
	if _, ok := s.pending[id]; ok {
		return ExchangePending, Receipt{}, nil
	}
	return "", Receipt{}, ErrExchangeUnknown
}

func (s *SQLiteBindingStore) LookupExchange(ctx context.Context, id string) (ExchangeStatus, Receipt, error) {
	var r Receipt
	var issued, expires string
	err := s.db.QueryRowContext(ctx, `SELECT kind,reservation_id,decision_id,decision_digest,issued_at,expires_at FROM admission_receipts WHERE exchange_id=?`, id).Scan(&r.Kind, &r.ReservationID, &r.DecisionID, &r.DecisionDigest, &issued, &expires)
	if err == nil {
		r.IssuedAt, err = time.Parse(time.RFC3339Nano, issued)
		if err != nil {
			return "", Receipt{}, err
		}
		r.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
		if err != nil {
			return "", Receipt{}, err
		}
		if checkReceiptExpiry(ctx, r) != nil {
			return "", Receipt{}, ErrExchangeUnknown
		}
		return ExchangeAccepted, r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", Receipt{}, err
	}
	var pending bool
	err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM admission_pending_exchanges WHERE exchange_id=? AND NOT EXISTS(SELECT 1 FROM admission_exchange_bindings WHERE exchange_id=?))`, id, id).Scan(&pending)
	if err != nil {
		return "", Receipt{}, err
	}
	if pending {
		return ExchangePending, Receipt{}, nil
	}
	return "", Receipt{}, ErrExchangeUnknown
}
