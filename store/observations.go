package store

import (
	"encoding/json"
	"errors"
	"time"
)

var ErrObservationProfile = errors.New("store: profile observation cannot be ended individually")

// Observation is tenant-owned intent; delivery updates are outside this contract.
type Observation struct {
	Tenant             string                     `json:"-"`
	ID                 string                     `json:"id"`
	Device             string                     `json:"device"`
	Paths              []string                   `json:"paths"`
	Attributes         map[string]json.RawMessage `json:"attributes"`
	Persistent         bool                       `json:"persistent"`
	Status             string                     `json:"status"`
	Source             string                     `json:"source"`
	Prefix             string                     `json:"prefix,omitempty"`
	Format             string                     `json:"format,omitempty"`
	Accept             string                     `json:"accept,omitempty"`
	EstablishedAt      *time.Time                 `json:"establishedAt"`
	LastNotificationAt *time.Time                 `json:"lastNotificationAt"`
	NotificationCount  uint64                     `json:"notificationCount"`
	Error              json.RawMessage            `json:"error"`
}

// Observations implementations are concurrency-safe and own all input/output.
// List returns a non-nil slice sorted by ID, including ended observations.
type Observations interface {
	Create(tenant string, v Observation) error
	Get(tenant, id string) (Observation, error)
	List(tenant, device string) ([]Observation, error)
	// UpdateAttributes replaces attributes only, preserving metadata atomically.
	UpdateAttributes(tenant, id string, attributes map[string]json.RawMessage) (Observation, error)
	// End atomically marks API observations ended; profile observations are protected.
	End(tenant, id string) error
}
