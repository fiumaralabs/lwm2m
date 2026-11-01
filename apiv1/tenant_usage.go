package apiv1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/store"
)

type tenantLimiter struct {
	mu      sync.Mutex
	locks   map[string]*tenantLock
	tenants TenantStore
}

type tenantLock struct {
	mu   sync.Mutex
	refs int
}

func (l *tenantLimiter) lock(tenant string) func() {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*tenantLock)
	}
	entry := l.locks[tenant]
	if entry == nil {
		entry = &tenantLock{}
		l.locks[tenant] = entry
	}
	entry.refs++
	l.mu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.locks, tenant)
		}
		l.mu.Unlock()
	}
}

func (o Options) deviceLimitReached(tenant string, limit int64) (bool, error) {
	var count int64
	for cursor := ""; ; {
		items, next, err := o.Devices.List(tenant, store.DeviceFilter{}, cursor, 500)
		if err != nil {
			return false, err
		}
		count += int64(len(items))
		if count >= limit {
			return true, nil
		}
		if next == "" {
			return false, nil
		}
		cursor = next
	}
}

var errOperationLimit = errors.New("operation limit exceeded")
var errOperationAdmission = errors.New("operation admission failed")

func (o Options) submitOperation(ctx context.Context, tenant string, op operations.Operation) (operations.Operation, error) {
	unlock := o.limits.lock(tenant)
	defer unlock()
	if t, ok := o.limits.tenants.Get(tenant); ok && t.Limits.OperationsPerMinute > 0 {
		reached, err := o.operationLimitReached(ctx, tenant, t.Limits.OperationsPerMinute, time.Now())
		if err != nil {
			return operations.Operation{}, fmt.Errorf("%w: %v", errOperationAdmission, err)
		}
		if reached {
			return operations.Operation{}, errOperationLimit
		}
	}
	return o.Executor.Submit(ctx, tenant, op)
}

func (o Options) operationLimitReached(ctx context.Context, tenant string, limit int64, now time.Time) (bool, error) {
	var count int64
	after := now.Add(-time.Minute)
	for cursor := ""; ; {
		items, next, err := o.Operations.List(ctx, tenant, operations.ListFilter{CreatedAfter: &after, CreatedBefore: &now}, cursor, 500)
		if err != nil {
			return false, err
		}
		for _, op := range items {
			if !op.CreatedAt.After(after) {
				return count >= limit, nil
			}
			if op.CreatedAt.After(now.Add(-time.Minute)) && !op.CreatedAt.After(now) {
				count++
			}
		}
		if count >= limit {
			return true, nil
		}
		if next == "" {
			return false, nil
		}
		cursor = next
	}
}

// tenantUsage reports retained events and operations grouped by UTC day.
func (o Options) tenantUsage(s TenantStore, w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/tenants/"), "/usage")
	if id == "" || strings.Contains(id, "/") {
		WriteProblem(w, 404, "not_found", "Unknown tenant")
		return
	}
	if _, ok := s.Get(id); !ok {
		WriteProblem(w, 404, "not_found", "Unknown tenant")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET")
		return
	}
	var devices, registered int
	for cursor := ""; ; {
		items, next, err := o.Devices.List(id, store.DeviceFilter{}, cursor, 500)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot list devices")
			return
		}
		devices += len(items)
		for _, d := range items {
			status := o.view(id, d).Status
			if status == "registered" || status == "sleeping" {
				registered++
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	type day struct {
		Operations int `json:"operations"`
		Events     int `json:"events"`
	}
	daily := map[string]day{}
	for cursor := ""; ; {
		items, next, err := o.Operations.List(r.Context(), id, operations.ListFilter{}, cursor, 500)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot list operations")
			return
		}
		for _, op := range items {
			key := op.CreatedAt.UTC().Format(time.DateOnly)
			d := daily[key]
			d.Operations++
			daily[key] = d
		}
		if next == "" {
			break
		}
		cursor = next
	}
	for cursor := ""; ; {
		items, _, err := o.Events.List(id, store.EventFilter{After: cursor, Limit: 500})
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot list events")
			return
		}
		for _, event := range items {
			key := event.Time.UTC().Format(time.DateOnly)
			d := daily[key]
			d.Events++
			daily[key] = d
		}
		if len(items) < 500 {
			break
		}
		cursor = items[len(items)-1].Sequence
	}
	deviceJSON(w, 200, struct {
		Devices    int            `json:"devices"`
		Registered int            `json:"registered"`
		Daily      map[string]day `json:"daily"`
	}{devices, registered, daily})
}
