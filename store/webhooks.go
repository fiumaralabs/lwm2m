package store

import (
	"errors"
	"strconv"
	"time"
)

var ErrWebhookNotFound = errors.New("webhook not found")
var ErrWebhookExists = errors.New("webhook exists")
var ErrWebhookCursor = errors.New("invalid webhook cursor")
var ErrWebhookPrecondition = errors.New("webhook precondition failed")

// Webhook holds metadata and private signing material. Secrets never marshal.
type Webhook struct {
	Version         uint64          `json:"-"`
	ID              string          `json:"id"`
	URL             string          `json:"url"`
	Types           []string        `json:"types"`
	Selector        string          `json:"selector"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	Secret          string          `json:"-"`
	PreviousSecrets []WebhookSecret `json:"-"`
}

// WebhookSecret retains a retired signing key only until its overlap expires.
type WebhookSecret struct {
	Secret    string
	ExpiresAt time.Time
}

// Webhooks implementations are tenant-isolated, concurrency-safe and return owned copies.
type Webhooks interface {
	// Replay returns an unexpired successful POST snapshot; an empty target ID
	// selects create, otherwise it selects rotation of that webhook.
	Replay(tenant, targetID, key string) (Webhook, bool)
	Create(string, Webhook, string) (Webhook, error)
	Get(string, string) (Webhook, error)
	List(string, string, int) ([]Webhook, string, error)
	Patch(string, string, WebhookPatch, WebhookMatch) (Webhook, error)
	Rotate(string, string, string, string) (Webhook, error)
	Delete(string, string, WebhookMatch) error
}

// WebhookMatch is an optional strong version condition, evaluated atomically
// after tenant-scoped existence lookup. Any matches any existing resource.
type WebhookMatch struct {
	Required bool
	Any      bool
	Tags     []string
}

func (v Webhook) ETag() string { return `"` + v.ID + "-" + strconv.FormatUint(v.Version, 10) + `"` }

func (m WebhookMatch) Matches(v Webhook) bool {
	if !m.Required || m.Any {
		return true
	}
	for _, tag := range m.Tags {
		if tag == v.ETag() {
			return true
		}
	}
	return false
}

// WebhookPatch applies only supplied fields; an empty Selector clears it.
type WebhookPatch struct {
	URL      *string
	Types    *[]string
	Selector *string
	Status   *string
}
