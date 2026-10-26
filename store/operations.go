package store

import (
	"context"
	"errors"
	"github.com/fiumaralabs/lwm2m/operations"
)

var (
	ErrNotFound = errors.New("store: not found")
	ErrExists   = errors.New("store: already exists")
	ErrTenant   = errors.New("store: tenant mismatch")
)

// Operations scopes every method by tenant. List is newest first (empty device
// means all devices); Pending is queued-only, oldest first. ID breaks time ties.
// Update atomically checks expected and applies a legal lifecycle change.
// Pending is a snapshot, not a claim; delivery must first Update queued to sent.
type Operations interface {
	Create(context.Context, string, operations.Operation) error
	Get(context.Context, string, string) (operations.Operation, error)
	List(context.Context, string, string) ([]operations.Operation, error)
	Pending(context.Context, string, string) ([]operations.Operation, error)
	Update(context.Context, string, string, operations.Status, operations.Change) error
}
