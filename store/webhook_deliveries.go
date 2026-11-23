package store

import "time"

// WebhookDelivery is one completed delivery attempt. Status is nil without a response.
type WebhookDelivery struct {
	ID          string    `json:"id"`
	EventID     string    `json:"eventId"`
	EventType   string    `json:"eventType"`
	AttemptedAt time.Time `json:"attemptedAt"`
	Status      *int      `json:"status"`
	LatencyMS   float64   `json:"latencyMs"`
	Error       string    `json:"error"`
}

// WebhookDeliveries holds tenant-scoped, bounded, newest-first attempt history.
type WebhookDeliveries interface {
	Append(tenant, webhook string, delivery WebhookDelivery)
	List(tenant, webhook, cursor string, limit int) ([]WebhookDelivery, string, error)
}
