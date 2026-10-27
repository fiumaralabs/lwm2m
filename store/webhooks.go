package store

import (
	"errors"
	"time"
)

var ErrWebhookNotFound = errors.New("webhook not found")
var ErrWebhookExists = errors.New("webhook exists")
var ErrWebhookCursor = errors.New("invalid webhook cursor")

// Webhook holds metadata and private signing material. Secrets never marshal.
type Webhook struct {
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
	Create(string, Webhook) (Webhook, error)
	Get(string, string) (Webhook, error)
	List(string, string, int) ([]Webhook, string, error)
	Patch(string, string, WebhookPatch) (Webhook, error)
	Rotate(string, string, string) (Webhook, error)
	Delete(string, string) error
}

// WebhookPatch applies only supplied fields; an empty Selector clears it.
type WebhookPatch struct {
	URL      *string
	Types    *[]string
	Selector *string
	Status   *string
}
