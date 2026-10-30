package apiv1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// BootstrapSession captures a completed bootstrap exchange. The JSON fields
// with unspecified schemas are preserved as raw JSON, including null.
type BootstrapSession struct {
	ID        string                 `json:"id"`
	StartedAt time.Time              `json:"startedAt"`
	EndedAt   time.Time              `json:"endedAt"`
	Kind      string                 `json:"kind"`
	Identity  json.RawMessage        `json:"identity"`
	Format    string                 `json:"format"`
	Steps     []BootstrapSessionStep `json:"steps"`
	Discover  json.RawMessage        `json:"discover"`
	Reads     json.RawMessage        `json:"reads"`
	Status    string                 `json:"status"`
	Error     json.RawMessage        `json:"error"`
}

type BootstrapSessionStep struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Format string `json:"format,omitempty"`
	Code   string `json:"code"`
}

// BootstrapSessionRetention limits retained sessions per tenant and device.
const BootstrapSessionRetention = 100

var ErrBootstrapSessionCursor = errors.New("invalid bootstrap session cursor")

// BootstrapSessionStore implementations are concurrency-safe and return owned copies.
// ListForDevice orders by append time, newest first; cursor is tenant/device bound.
type BootstrapSessionStore interface {
	Append(tenant, endpoint string, session BootstrapSession) error
	ListForDevice(tenant, endpoint, cursor string, limit int) ([]BootstrapSession, string, error)
}

type bootstrapSessionRecord struct {
	sequence uint64
	session  BootstrapSession
}
type memoryBootstrapSessionStore struct {
	mu       sync.RWMutex
	sequence uint64
	records  map[string]map[string][]bootstrapSessionRecord
}

func NewMemoryBootstrapSessionStore() BootstrapSessionStore {
	return &memoryBootstrapSessionStore{records: make(map[string]map[string][]bootstrapSessionRecord)}
}

func cloneBootstrapSession(v BootstrapSession) BootstrapSession {
	v.Identity = append(json.RawMessage(nil), v.Identity...)
	v.Discover = append(json.RawMessage(nil), v.Discover...)
	v.Reads = append(json.RawMessage(nil), v.Reads...)
	v.Error = append(json.RawMessage(nil), v.Error...)
	if v.Steps != nil {
		v.Steps = append([]BootstrapSessionStep{}, v.Steps...)
	}
	return v
}

func (s *memoryBootstrapSessionStore) Append(tenant, endpoint string, session BootstrapSession) error {
	hasError := len(bytes.TrimSpace(session.Error)) > 0 && !bytes.Equal(bytes.TrimSpace(session.Error), []byte("null"))
	if (session.Kind != "request" && session.Kind != "pack") || (session.Status != "succeeded" && session.Status != "failed") || (session.Status == "failed") != hasError {
		return errors.New("invalid bootstrap session kind, status or error")
	}
	for _, v := range []json.RawMessage{session.Identity, session.Discover, session.Reads, session.Error} {
		if len(v) > 0 && !json.Valid(v) {
			return errors.New("invalid bootstrap session JSON")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records[tenant] == nil {
		s.records[tenant] = make(map[string][]bootstrapSessionRecord)
	}
	s.sequence++
	records := append(s.records[tenant][endpoint], bootstrapSessionRecord{s.sequence, cloneBootstrapSession(session)})
	if len(records) > BootstrapSessionRetention {
		records = append([]bootstrapSessionRecord(nil), records[len(records)-BootstrapSessionRetention:]...)
	}
	s.records[tenant][endpoint] = records
	return nil
}

func (s *memoryBootstrapSessionStore) ListForDevice(tenant, endpoint, cursor string, limit int) ([]BootstrapSession, string, error) {
	var c struct {
		Tenant   *string
		Endpoint *string
		Before   uint64
	}
	if limit < 1 || limit > 500 {
		return nil, "", ErrBootstrapSessionCursor
	}
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Tenant == nil || c.Endpoint == nil || *c.Tenant != tenant || *c.Endpoint != endpoint || c.Before == 0 {
			return nil, "", ErrBootstrapSessionCursor
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := s.records[tenant][endpoint]
	out := make([]BootstrapSession, 0)
	var last uint64
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if cursor != "" && r.sequence >= c.Before {
			continue
		}
		if len(out) == limit {
			c.Tenant, c.Endpoint, c.Before = &tenant, &endpoint, last
			b, _ := json.Marshal(c)
			return out, base64.RawURLEncoding.EncodeToString(b), nil
		}
		out = append(out, cloneBootstrapSession(r.session))
		last = r.sequence
	}
	return out, "", nil
}
