package apiv1

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
)

// TimeSeriesPoint is a recorded numeric or boolean resource value.
type TimeSeriesPoint struct {
	T time.Time `json:"t"`
	V any       `json:"v"`
}

// TimeSeriesStore provides tenant-scoped resource history. Query returns points
// in timestamp order; points with equal timestamps retain append order across
// queries so raw pagination can resume without repeating existing points.
type TimeSeriesStore interface {
	Append(tenant, endpoint, path string, t time.Time, value any) error
	Query(tenant, endpoint, path string, from, to time.Time) ([]TimeSeriesPoint, error)
}

type seriesKey struct{ tenant, endpoint, path string }

// MemoryTimeSeriesStore keeps time series in process memory.
type MemoryTimeSeriesStore struct {
	mu     sync.RWMutex
	points map[seriesKey][]TimeSeriesPoint
}

func NewMemoryTimeSeriesStore() *MemoryTimeSeriesStore { return &MemoryTimeSeriesStore{} }

func (s *MemoryTimeSeriesStore) Append(tenant, endpoint, path string, t time.Time, value any) error {
	p, err := lwm2m.ParsePath(path)
	if err != nil || (!p.IsResource() && !p.IsResourceInstance()) {
		return fmt.Errorf("invalid resource path")
	}
	path = p.String()
	switch v := value.(type) {
	case bool:
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("invalid value")
		}
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return fmt.Errorf("invalid value")
		}
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
	default:
		return fmt.Errorf("value must be numeric or boolean")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.points == nil {
		s.points = make(map[seriesKey][]TimeSeriesPoint)
	}
	k := seriesKey{tenant, endpoint, path}
	s.points[k] = append(s.points[k], TimeSeriesPoint{t, value})
	return nil
}

func (s *MemoryTimeSeriesStore) Query(tenant, endpoint, path string, from, to time.Time) ([]TimeSeriesPoint, error) {
	p, err := lwm2m.ParsePath(path)
	if err != nil || (!p.IsResource() && !p.IsResourceInstance()) {
		return nil, fmt.Errorf("invalid resource path")
	}
	path = p.String()
	s.mu.RLock()
	defer s.mu.RUnlock()
	points := []TimeSeriesPoint{}
	for _, p := range s.points[seriesKey{tenant, endpoint, path}] {
		if !p.T.Before(from) && !p.T.After(to) {
			points = append(points, p)
		}
	}
	sort.SliceStable(points, func(i, j int) bool { return points[i].T.Before(points[j].T) })
	return points, nil
}
