package store

import (
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
// The alias lets the executor use this exact contract without an import cycle.
type Operations = operations.Operations
