package store

import (
	"errors"
	"time"
)

// ErrWebhookDeliveryNotFound covers missing or out-of-scope attempts.
var ErrWebhookDeliveryNotFound = errors.New("webhook delivery not found")

// WebhookDelivery is one completed delivery attempt. Status is nil without a response.
type WebhookDelivery struct {
	// Envelope is the exact sent body, or nil if marshaling failed. It is not public JSON.
	Envelope    []byte    `json:"-"`
	ID          string    `json:"id"`
	EventID     string    `json:"eventId"`
	EventType   string    `json:"eventType"`
	Attempt     int       `json:"attempt"`
	AttemptedAt time.Time `json:"attemptedAt"`
	Status      *int      `json:"status"`
	LatencyMS   float64   `json:"latencyMs"`
	Error       string    `json:"error"`
}

// WebhookDeliveries holds tenant-scoped, bounded, newest-first attempt history.
type WebhookDeliveries interface {
	Append(tenant, webhook string, delivery WebhookDelivery)
	Get(tenant, webhook, deliveryID string) (WebhookDelivery, error)
	List(tenant, webhook, cursor string, limit int) ([]WebhookDelivery, string, error)
}
