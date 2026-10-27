package memory

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

// Events is a tenant-isolated event log. Configure Clock and Retention before use.
// Its zero value uses the system clock and seven days of retention.
type Events struct {
	mu        sync.Mutex
	Clock     func() time.Time
	Retention time.Duration
	tenants   map[string]*eventLog
}
type eventLog struct {
	sequence uint64
	low      string
	items    []store.Event
}

func (s *Events) now() time.Time {
	if s.Clock != nil {
		return s.Clock().UTC()
	}
	return time.Now().UTC()
}
func cloneEvent(e store.Event) store.Event {
	e.Data = append(json.RawMessage(nil), e.Data...)
	return e
}
func (s *Events) trim(log *eventLog, now time.Time) {
	retention := s.Retention
	if retention <= 0 {
		retention = 7 * 24 * time.Hour
	}
	kept := make([]store.Event, 0, len(log.items))
	for _, e := range log.items {
		if e.Time.Before(now.Add(-retention)) {
			if e.Sequence > log.low {
				log.low = e.Sequence
			}
		} else {
			kept = append(kept, e)
		}
	}
	log.items = kept
}
func matchesEvent(kind, filter string) bool {
	if filter == "" {
		return true
	}
	for _, pattern := range strings.Split(filter, ",") {
		if kind == pattern || strings.HasSuffix(pattern, "*") && strings.HasPrefix(kind, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}
func (s *Events) List(tenant string, f store.EventFilter) ([]store.Event, bool, error) {
	if f.Limit < 1 || f.After != "" && !store.ValidSequence(f.After) || f.Before != "" && !store.ValidSequence(f.Before) {
		return nil, false, store.ErrEventQuery
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]store.Event, 0)
	log := s.tenants[tenant]
	if log == nil {
		return result, false, nil
	}
	s.trim(log, s.now())
	for offset := range log.items {
		i := offset
		if f.Newest {
			i = len(log.items) - 1 - offset
		}
		e := log.items[i]
		if f.After != "" && e.Sequence <= f.After || f.Before != "" && e.Sequence >= f.Before || f.Device != "" && e.Subject != f.Device || !matchesEvent(e.Type, f.Type) {
			continue
		}
		result = append(result, cloneEvent(e))
		if len(result) == f.Limit {
			break
		}
	}
	return result, f.After != "" && f.After < log.low, nil
}
func (s *Events) Append(tenant, kind, device string, data json.RawMessage) (store.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var raw [16]byte
	if _, err := rand.Read(raw[6:]); err != nil {
		return store.Event{}, err
	}
	stamp := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		raw[i] = byte(stamp)
		stamp >>= 8
	}
	n, base := new(big.Int).SetBytes(raw[:]), big.NewInt(32)
	var encoded [26]byte
	for i := 25; i >= 0; i-- {
		remainder := new(big.Int)
		n.QuoRem(n, base, remainder)
		encoded[i] = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"[remainder.Int64()]
	}
	if s.tenants == nil {
		s.tenants = make(map[string]*eventLog)
	}
	log := s.tenants[tenant]
	if log == nil {
		log = &eventLog{}
		s.tenants[tenant] = log
	}
	log.sequence++
	e := store.Event{SpecVersion: "1.0", ID: "evt_" + string(encoded[:]), Source: "/tenants/" + tenant, Type: kind, Time: now, Subject: device, Sequence: fmt.Sprintf("%018d", log.sequence), DataContentType: "application/json", Data: data}
	log.items = append(log.items, cloneEvent(e))
	s.trim(log, now)
	return cloneEvent(e), nil
}
