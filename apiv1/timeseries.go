package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/store"
)

type seriesCursor struct {
	Tenant, Endpoint, Filter string
	From, To                 time.Time
	PathIndex                int
	Timestamp                time.Time
	EqualCount               int
}
type responseSeries struct {
	Path   string            `json:"path"`
	Points []TimeSeriesPoint `json:"points"`
}

func (o Options) timeSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET")
		return
	}
	if o.TimeSeries == nil {
		WriteProblem(w, 501, "timeseries_disabled", "Time series is disabled")
		return
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/timeseries")
	ep, err := url.PathUnescape(raw)
	if err != nil || ep == "" || strings.Contains(raw, "/") {
		WriteProblem(w, 404, "not_found", "Unknown device path")
		return
	}
	tenant := TenantFrom(r.Context())
	if _, err := o.Devices.Get(tenant, ep); err != nil {
		if errors.Is(err, store.ErrDeviceNotFound) {
			WriteProblem(w, 404, "not_found", "Device not found")
		} else {
			WriteProblem(w, 500, "internal_error", "Cannot get device")
		}
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	bad := func() { WriteProblem(w, 400, "invalid_query", "Invalid time series query") }
	if err != nil {
		bad()
		return
	}
	for k, values := range q {
		if k != "path" && k != "from" && k != "to" && k != "interval" && k != "aggregate" && k != "limit" && k != "cursor" || k != "path" && len(values) != 1 {
			bad()
			return
		}
	}
	paths := q["path"]
	if len(paths) == 0 {
		bad()
		return
	}
	seen := map[string]bool{}
	for i, path := range paths {
		p, err := lwm2m.ParsePath(path)
		if err != nil || !strings.HasPrefix(path, "/") || (!p.IsResource() && !p.IsResourceInstance()) || seen[p.String()] {
			bad()
			return
		}
		seen[p.String()] = true
		paths[i] = p.String()
	}
	now := time.Now().UTC()
	from, to := now.Add(-24*time.Hour), now
	if q.Has("from") {
		from, err = time.Parse(time.RFC3339, q.Get("from"))
		if err != nil {
			bad()
			return
		}
	}
	if q.Has("to") {
		to, err = time.Parse(time.RFC3339, q.Get("to"))
		if err != nil {
			bad()
			return
		}
	}
	interval := 0
	agg := q.Get("aggregate")
	if q.Has("interval") {
		interval, err = strconv.Atoi(q.Get("interval"))
		if err != nil || interval < 1 || int64(interval) > int64((1<<63-1)/int64(time.Second)) {
			bad()
			return
		}
	}
	if (interval == 0) != (agg == "") {
		bad()
		return
	}
	if agg != "" && agg != "avg" && agg != "min" && agg != "max" && agg != "sum" && agg != "count" && agg != "first" && agg != "last" {
		bad()
		return
	}
	limit := 50
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			bad()
			return
		}
	}
	if agg != "" && (q.Has("limit") || q.Has("cursor")) {
		bad()
		return
	}
	filter := url.Values{}
	for k, v := range q {
		if k != "cursor" && k != "limit" {
			filter[k] = v
		}
	}
	cur := seriesCursor{Tenant: tenant, Endpoint: ep, Filter: filter.Encode(), From: from, To: to}
	if q.Has("cursor") {
		var c seriesCursor
		b, e := base64.RawURLEncoding.DecodeString(q.Get("cursor"))
		if e != nil || json.Unmarshal(b, &c) != nil || c.Tenant != tenant || c.Endpoint != ep || c.Filter != cur.Filter || c.PathIndex < 0 || c.PathIndex >= len(paths) || c.EqualCount < 1 || c.From.IsZero() || c.To.IsZero() || c.Timestamp.Before(c.From) || c.Timestamp.After(c.To) {
			bad()
			return
		}
		cur = c
		from, to = c.From, c.To
	}
	if from.After(to) {
		bad()
		return
	}
	result := make([]responseSeries, 0, len(paths))
	selectedCount := 0
	hasMore := false
	last := cur
	for pathIndex, path := range paths {
		points, err := o.TimeSeries.Query(tenant, ep, path, from, to)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot query time series")
			return
		}
		if agg != "" {
			points = aggregatePoints(points, time.Duration(interval)*time.Second, agg)
			for _, p := range points {
				if v, ok := p.V.(float64); ok && (math.IsInf(v, 0) || math.IsNaN(v)) {
					WriteProblem(w, 500, "internal_error", "Time series aggregate is not representable")
					return
				}
			}
		} else {
			selected := []TimeSeriesPoint{}
			// Resume by path and timestamp, counting equal-timestamp points in
			// append order. Backdated inserts before the cursor are not replayed.
			equalCount := 0
			var previous time.Time
			for _, p := range points {
				if p.T.Equal(previous) {
					equalCount++
				} else {
					previous, equalCount = p.T, 1
				}
				if q.Has("cursor") && (pathIndex < cur.PathIndex || pathIndex == cur.PathIndex && (p.T.Before(cur.Timestamp) || p.T.Equal(cur.Timestamp) && equalCount <= cur.EqualCount)) {
					continue
				}
				if selectedCount == limit {
					hasMore = true
					continue
				}
				selected = append(selected, p)
				selectedCount++
				last.PathIndex, last.Timestamp, last.EqualCount = pathIndex, p.T, equalCount
			}
			points = selected
		}
		if points == nil {
			points = []TimeSeriesPoint{}
		}
		result = append(result, responseSeries{path, points})
	}
	next := ""
	if agg == "" && hasMore {
		b, _ := json.Marshal(last)
		next = base64.RawURLEncoding.EncodeToString(b)
	}
	deviceJSON(w, 200, struct {
		Series []responseSeries `json:"series"`
		Next   string           `json:"nextCursor,omitempty"`
	}{result, next})
}

func numeric(v any) float64 {
	switch x := v.(type) {
	case bool:
		if x {
			return 1
		}
		return 0
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	}
	return 0
}

func aggregatePoints(points []TimeSeriesPoint, interval time.Duration, aggregate string) []TimeSeriesPoint {
	out := []TimeSeriesPoint{}
	for i := 0; i < len(points); {
		start := points[i].T.Truncate(interval)
		j := i + 1
		for j < len(points) && points[j].T.Truncate(interval).Equal(start) {
			j++
		}
		sum, avg, min, max := 0.0, 0.0, numeric(points[i].V), numeric(points[i].V)
		for _, p := range points[i:j] {
			n := numeric(p.V)
			sum += n
			avg += n / float64(j-i)
			if n < min {
				min = n
			}
			if n > max {
				max = n
			}
		}
		var v any
		switch aggregate {
		case "avg":
			v = avg
		case "min":
			v = min
		case "max":
			v = max
		case "sum":
			v = sum
		case "count":
			v = j - i
		case "first":
			v = points[i].V
		case "last":
			v = points[j-1].V
		}
		out = append(out, TimeSeriesPoint{start, v})
		i = j
	}
	return out
}
