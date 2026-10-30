// Package operations defines asynchronous device operations independently of delivery and storage.
package operations

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/fiumaralabs/lwm2m/internal/id"
)

// ListFilter combines fields with AND and statuses with OR.
type ListFilter struct {
	// CreatedAfter is exclusive; CreatedBefore is inclusive.
	CreatedAfter  *time.Time `json:",omitempty"`
	CreatedBefore *time.Time `json:",omitempty"`
	Device        string
	Status        []Status
	Type          string
	Job           string
}

type Status string

const (
	Queued    Status = "queued"
	Sent      Status = "sent"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Timeout   Status = "timeout"
	Expired   Status = "expired"
	Dropped   Status = "dropped"
	Cancelled Status = "cancelled"
)

var ErrTransition = errors.New("operations: invalid transition")

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Actor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Operation keeps the request and result as JSON so codecs and HTTP types do not
// become dependencies of a durable store. Result includes the CoAP code, if answered.
type Operation struct {
	Tenant      string          `json:"-"`
	ID          string          `json:"id"`
	Device      string          `json:"device"`
	Type        string          `json:"type"`
	Request     json.RawMessage `json:"request"`
	Status      Status          `json:"status"`
	TTLSeconds  int             `json:"ttlSeconds"`
	WhenOffline string          `json:"whenOffline"`
	CreatedAt   time.Time       `json:"createdAt"`
	SentAt      *time.Time      `json:"sentAt,omitempty"`
	CompletedAt *time.Time      `json:"completedAt,omitempty"`
	ExpiresAt   time.Time       `json:"expiresAt"`
	CreatedBy   Actor           `json:"createdBy"`
	Job         *string         `json:"job"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       *Error          `json:"error,omitempty"`
	Prefix      string          `json:"prefix,omitempty"`
	Format      string          `json:"format,omitempty"`
	Accept      string          `json:"accept,omitempty"`
}

// New uses the default TTL when ttl is nil. A zero TTL means now or never.
// The caller validates request data, TTL bounds and offline policy before Create.
func New(tenant, device, kind string, request json.RawMessage, ttl *int, now time.Time) Operation {
	seconds := 86400
	if ttl != nil {
		seconds = *ttl
	}
	return Operation{Tenant: tenant, ID: id.New("op"), Device: device, Type: kind,
		Request: append(json.RawMessage(nil), request...), Status: Queued, TTLSeconds: seconds,
		WhenOffline: "queue", CreatedAt: now, ExpiresAt: now.Add(time.Duration(seconds) * time.Second)}
}

// Change contains only mutable lifecycle fields. Identity and request never change.
type Change struct {
	Status Status
	At     time.Time
	Result json.RawMessage
	Error  *Error
}

// Apply checks lifecycle edges. Expiry scheduling and device delivery belong to the executor.
func (o *Operation) Apply(c Change) error {
	valid := o.Status == Queued && (c.Status == Sent || c.Status == Failed || c.Status == Expired || c.Status == Dropped || c.Status == Cancelled) ||
		o.Status == Sent && (c.Status == Succeeded || c.Status == Failed || c.Status == Timeout || c.Status == Dropped)
	if !valid {
		return ErrTransition
	}
	o.Status, o.Result, o.Error = c.Status, append(json.RawMessage(nil), c.Result...), nil
	if c.Error != nil {
		e := *c.Error
		o.Error = &e
	}
	if c.Status == Sent {
		o.SentAt = &c.At
	} else {
		o.CompletedAt = &c.At
	}
	return nil
}
