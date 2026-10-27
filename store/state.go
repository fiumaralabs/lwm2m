package store

import (
	"encoding/json"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
)

// StateValue is a last-known resource value. Prefix is part of its identity.
type StateValue struct {
	Path       string          `json:"path"`
	Prefix     string          `json:"prefix,omitempty"`
	Type       string          `json:"type"`
	Value      json.RawMessage `json:"value"`
	Timestamp  *time.Time      `json:"timestamp,omitempty"`
	ReceivedAt time.Time       `json:"receivedAt"`
	Source     string          `json:"source"`
}

// State records successful operation values and serves subtree-filtered snapshots.
// An empty prefix selects the root device, not all gateway end devices.
type State interface {
	Record(operations.Operation) error
	List(tenant, device, prefix string, paths []string) ([]StateValue, error)
}
