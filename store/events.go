package store

import (
	"encoding/json"
	"errors"
	"time"
)

// Event is a CloudEvents 1.0 envelope with a tenant-local cursor.
type Event struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	Subject         string          `json:"subject"`
	Sequence        string          `json:"sequence"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
}

type EventFilter struct {
	Type, Device, After, Before string
	Limit                       int
	Newest                      bool
}

// Events stores immutable, tenant-local event history.
type Events interface {
	Append(tenant, kind, device string, data json.RawMessage) (Event, error)
	// List returns ascending sequences, or descending when Newest is set.
	// After and Before are exclusive. The bool requests a replay reset when
	// After precedes the last expired sequence, even if no events remain.
	List(tenant string, f EventFilter) ([]Event, bool, error)
}

var ErrEventQuery = errors.New("invalid event query")

// ValidSequence accepts exactly eighteen ASCII decimal digits.
func ValidSequence(s string) bool {
	if len(s) != 18 {
		return false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
