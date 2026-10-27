package memory

import (
	"container/heap"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
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
	expiry   eventExpiries
	dead     int
}

// Expiry order is independent of sequence order: the clock may move backwards.
type eventExpiry struct {
	at    time.Time
	index int
}
type eventExpiries []eventExpiry

func (q eventExpiries) Len() int           { return len(q) }
func (q eventExpiries) Less(i, j int) bool { return q[i].at.Before(q[j].at) }
func (q eventExpiries) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *eventExpiries) Push(v any)        { *q = append(*q, v.(eventExpiry)) }
func (q *eventExpiries) Pop() any {
	old := *q
	v := old[len(old)-1]
	old[len(old)-1] = eventExpiry{}
	*q = old[:len(old)-1]
	return v
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
	cutoff := now.Add(-retention)
	for len(log.expiry) > 0 && log.expiry[0].at.Before(cutoff) {
		expired := heap.Pop(&log.expiry).(eventExpiry)
		e := log.items[expired.index]
		if e.Sequence > log.low {
			log.low = e.Sequence
		}
		// Release payloads immediately, but keep sequence keys for cursor search.
		log.items[expired.index] = store.Event{Sequence: e.Sequence}
		log.dead++
	}
	// Reclaim holes only when at least half the slice is dead. The scan and
	// heap rebuild are amortized over expired events, never over every poll.
	if log.dead > 0 && log.dead >= len(log.items)-log.dead {
		kept := log.items[:0]
		log.expiry = log.expiry[:0]
		for _, e := range log.items {
			if e.SpecVersion != "" {
				log.expiry = append(log.expiry, eventExpiry{at: e.Time, index: len(kept)})
				kept = append(kept, e)
			}
		}
		clear(log.items[len(kept):])
		log.items = kept
		log.dead = 0
		heap.Init(&log.expiry)
	}
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
	start, end := 0, len(log.items)
	if f.After != "" {
		start = sort.Search(len(log.items), func(i int) bool { return log.items[i].Sequence > f.After })
	}
	if f.Before != "" {
		end = sort.Search(len(log.items), func(i int) bool { return log.items[i].Sequence >= f.Before })
	}
	for offset := 0; offset < end-start; offset++ {
		i := start + offset
		if f.Newest {
			i = end - 1 - offset
		}
		e := log.items[i]
		if e.SpecVersion == "" || f.Device != "" && e.Subject != f.Device || !matchesEvent(e.Type, f.Type) {
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
	s.trim(log, now)
	log.items = append(log.items, cloneEvent(e))
	heap.Push(&log.expiry, eventExpiry{at: now, index: len(log.items) - 1})
	return cloneEvent(e), nil
}
