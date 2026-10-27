package store

import (
	"errors"
	"time"
)

var (
	ErrAPIKeyNotFound = errors.New("API key not found")
	ErrAPIKeyExists   = errors.New("API key already exists")
)

// KeyMetadata is the public, non-secret representation of an API key.
type KeyMetadata struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

// APIKey persists only a secret's hash, never the raw secret.
type APIKey struct {
	KeyMetadata
	Hash   [32]byte `json:"hash"`
	Tenant string   `json:"tenant"`
}

// APIKeys owns all input and output snapshots. IDs are globally unique;
// Delete is tenant-scoped. Touch updates only lastUsedAt and never recreates a key.
type APIKeys interface {
	Get(id string) (APIKey, error)
	Create(APIKey) error
	List(tenant string) ([]KeyMetadata, error)
	Delete(tenant, id string) error
	Touch(id string, at time.Time) error
}
