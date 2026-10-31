package store

import (
	"context"
	"errors"
	"time"
)

// DeviceCreateOutcome is the durable result of an idempotent device create.
// Fingerprint identifies the request; the request body is never retained here.
type DeviceCreateOutcome struct {
	Tenant, OperationID, Fingerprint, Endpoint string
	Status, Reason                             string
	CreatedAt, CompletedAt                     time.Time
}

var ErrOperationIDReused = errors.New("store: operation id reused with a different request")

// DeviceCreateStore serializes device creation, outcomes, fencing and deletion.
// Outcomes are tenant scoped and retained until explicitly deleted. A create
// atomically stores the device and committed outcome, or a not-created outcome
// when the endpoint exists in any tenant. Existing outcomes replay unchanged;
// a different fingerprint returns ErrOperationIDReused unless fenced. A fence
// is inserted atomically on a missing lookup and prevents a subsequent create.
// The boolean reports whether this call created a device. Delete returns
// ErrNotFound for an absent outcome. Callers validate input before creating.
type DeviceCreateStore interface {
	CreateDeviceWithOutcome(ctx context.Context, tenant, operationID, fingerprint string, d Device) (DeviceCreateOutcome, bool, error)
	RecordDeviceCreateNotCreated(ctx context.Context, tenant, operationID, fingerprint, reason string) (DeviceCreateOutcome, error)
	GetOrFenceDeviceCreate(ctx context.Context, tenant, operationID string) (DeviceCreateOutcome, error)
	DeleteDeviceCreate(ctx context.Context, tenant, operationID string) error
}
