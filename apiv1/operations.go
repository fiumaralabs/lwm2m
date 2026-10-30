package apiv1

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m/operations"
	"github.com/fiumaralabs/lwm2m/selector"
	"github.com/fiumaralabs/lwm2m/store"
)

type operationReply struct {
	header http.Header
	body   bytes.Buffer
	status int
	at     time.Time
	done   chan struct{}
}

func (w *operationReply) Header() http.Header         { return w.header }
func (w *operationReply) WriteHeader(s int)           { w.status = s }
func (w *operationReply) Write(b []byte) (int, error) { return w.body.Write(b) }
func (o Options) operationHandler() http.Handler {
	var mu sync.Mutex
	replies := map[[3]string]*operationReply{}
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if r.Method != "POST" || key == "" {
			o.serveOperations(w, r)
			return
		}
		k := [3]string{TenantFrom(r.Context()), r.URL.EscapedPath(), key}
		mu.Lock()
		for k, v := range replies {
			if time.Since(v.at) >= 24*time.Hour {
				select {
				case <-v.done:
					delete(replies, k)
				default:
				}
			}
		}
		reply, exists := replies[k]
		if !exists {
			reply = &operationReply{header: make(http.Header), at: time.Now(), done: make(chan struct{})}
			replies[k] = reply
		}
		mu.Unlock()
		if !exists {
			o.serveOperations(reply, r)
			close(reply.done)
		}
		select {
		case <-r.Context().Done():
			return
		case <-reply.done:
		}
		for k, v := range reply.header {
			w.Header()[k] = append([]string(nil), v...)
		}
		w.WriteHeader(reply.status)
		_, _ = w.Write(reply.body.Bytes())
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := "devices:read"
		if r.Method != "GET" {
			scope = "operations:write"
		}
		RequireScope(scope)(serve).ServeHTTP(w, r)
	})
}
func (o Options) serveOperations(w http.ResponseWriter, r *http.Request) {
	tenant := TenantFrom(r.Context())
	if r.URL.Path == "/v1/operations" {
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET")
			return
		}
		o.listOperations(w, r, r.URL.Query().Get("device"))
		return
	}
	wait := -1
	if r.URL.Query().Has("wait") {
		var err error
		wait, err = strconv.Atoi(r.URL.Query().Get("wait"))
		if err != nil || wait < 0 || wait > 60 {
			WriteProblem(w, 400, "invalid_wait", "wait must be between 0 and 60")
			return
		}
	}
	if strings.HasPrefix(r.URL.EscapedPath(), "/v1/devices/") {
		raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.EscapedPath(), "/v1/devices/"), "/operations")
		ep, err := url.PathUnescape(raw)
		if err != nil || ep == "" || strings.Contains(raw, "/") {
			WriteProblem(w, 404, "not_found", "Unknown path")
			return
		}
		if _, err = o.Devices.Get(tenant, ep); err != nil {
			if !errors.Is(err, store.ErrDeviceNotFound) && r.Method == "GET" && r.URL.Query().Get("selector") != "" {
				operationError(w, err)
			} else {
				WriteProblem(w, 404, "device_not_found", "Device not found")
			}
			return
		}
		if r.Method == "GET" {
			o.listOperations(w, r, ep)
			return
		}
		if r.Method != "POST" {
			w.Header().Set("Allow", "GET, POST")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET or POST")
			return
		}
		if o.Executor == nil || tenant != o.ServerTenant {
			WriteProblem(w, 503, "unavailable", "No executor for tenant")
			return
		}
		var input struct {
			Type        string            `json:"type"`
			TTL         *int              `json:"ttlSeconds"`
			WhenOffline string            `json:"whenOffline"`
			Prefix      string            `json:"prefix"`
			Format      string            `json:"format"`
			Accept      string            `json:"accept"`
			Paths       []string          `json:"paths,omitempty"`
			Path        string            `json:"path,omitempty"`
			Values      []json.RawMessage `json:"values,omitempty"`
			Mode        string            `json:"mode,omitempty"`
			Arguments   string            `json:"arguments,omitempty"`
		}
		if !DecodeJSON(w, r, &input) {
			return
		}
		if input.TTL != nil && (*input.TTL < 0 || *input.TTL > 2147483647) || input.WhenOffline != "" && input.WhenOffline != "queue" && input.WhenOffline != "reject" {
			WriteProblem(w, 400, "invalid_operation", "Invalid TTL or offline policy")
			return
		}
		request, _ := json.Marshal(struct {
			Paths     []string          `json:"paths,omitempty"`
			Path      string            `json:"path,omitempty"`
			Values    []json.RawMessage `json:"values,omitempty"`
			Mode      string            `json:"mode,omitempty"`
			Arguments string            `json:"arguments,omitempty"`
		}{input.Paths, input.Path, input.Values, input.Mode, input.Arguments})
		op := operations.New(tenant, ep, input.Type, request, input.TTL, time.Now().UTC())
		keyID := strings.SplitN(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer lwm_"), "_", 2)[0]
		op.CreatedBy = operations.Actor{Kind: "apiKey", ID: "key_" + keyID}
		if input.WhenOffline != "" {
			op.WhenOffline = input.WhenOffline
		}
		op.Prefix, op.Format, op.Accept = input.Prefix, input.Format, input.Accept
		// Serialize the quota check and submission for requests on this handler.
		o.limits.mu.Lock()
		if t, ok := o.limits.tenants.Get(tenant); ok && t.Limits.OperationsPerMinute > 0 {
			reached, listErr := o.operationLimitReached(r.Context(), tenant, t.Limits.OperationsPerMinute, time.Now())
			if listErr != nil {
				o.limits.mu.Unlock()
				WriteProblem(w, 500, "internal_error", "Cannot list operations")
				return
			}
			if reached {
				o.limits.mu.Unlock()
				// A full rolling minute lets existing operations age out.
				w.Header().Set("Retry-After", "60")
				WriteProblem(w, 429, "limit_exceeded", "Operation limit exceeded")
				return
			}
		}
		op, err = o.Executor.Submit(r.Context(), tenant, op)
		o.limits.mu.Unlock()
		if err != nil {
			operationError(w, err)
			return
		}
		w.Header().Set("Location", "/v1/operations/"+op.ID)
		status := 202
		if wait >= 0 {
			op = o.waitOperation(r, op, wait)
			if op.Status != operations.Queued && op.Status != operations.Sent {
				status = 200
			}
		}
		deviceJSON(w, status, op)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/operations/")
	if id == "" || strings.Contains(id, "/") {
		WriteProblem(w, 404, "not_found", "Unknown path")
		return
	}
	op, err := o.Operations.Get(r.Context(), tenant, id)
	if err != nil {
		operationError(w, err)
		return
	}
	switch r.Method {
	case "GET":
		if wait >= 0 && o.Executor != nil && tenant == o.ServerTenant {
			op = o.waitOperation(r, op, wait)
		}
		deviceJSON(w, 200, op)
	case "DELETE":
		if o.Executor == nil || tenant != o.ServerTenant {
			WriteProblem(w, 503, "unavailable", "No executor for tenant")
			return
		}
		if _, err = o.Executor.Cancel(r.Context(), tenant, id); err != nil {
			operationError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		w.Header().Set("Allow", "GET, DELETE")
		WriteProblem(w, 405, "method_not_allowed", "Expected GET or DELETE")
	}
}
func (o Options) waitOperation(r *http.Request, op operations.Operation, seconds int) operations.Operation {
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
	defer cancel()
	if got, err := o.Executor.Wait(ctx, TenantFrom(r.Context()), op.ID); err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return got
	}
	return op
}
func (o Options) listOperations(w http.ResponseWriter, r *http.Request, device string) {
	limit := 50
	if r.URL.Query().Has("limit") {
		var err error
		limit, err = strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit < 1 || limit > 500 {
			WriteProblem(w, 400, "invalid_limit", "limit must be between 1 and 500")
			return
		}
	}
	f := operations.ListFilter{Device: device, Type: r.URL.Query().Get("type"), Job: r.URL.Query().Get("job")}
	if r.URL.Query().Has("status") {
		for _, status := range strings.Split(r.URL.Query().Get("status"), ",") {
			switch operations.Status(status) {
			case operations.Queued, operations.Sent, operations.Succeeded, operations.Failed, operations.Timeout, operations.Expired, operations.Dropped, operations.Cancelled:
				f.Status = append(f.Status, operations.Status(status))
			default:
				WriteProblem(w, 400, "invalid_filter", "Unknown operation status")
				return
			}
		}
	}
	match, err := selector.Parse(r.URL.Query().Get("selector"))
	if err != nil {
		WriteProblem(w, 400, "invalid_selector", "Invalid label selector")
		return
	}
	items, next, err := o.listSelectedOperations(r, TenantFrom(r.Context()), f, match, limit)
	if err != nil {
		if errors.Is(err, store.ErrCursor) {
			WriteProblem(w, 400, "invalid_cursor", "Invalid pagination cursor")
		} else {
			operationError(w, err)
		}
		return
	}
	deviceJSON(w, 200, struct {
		Items []operations.Operation `json:"items"`
		Next  string                 `json:"nextCursor,omitempty"`
	}{items, next})
}

// operationSelectorCursor resumes within a batch without interpreting store cursors.
type operationSelectorCursor struct {
	Selector, Tenant, Device, Status, Type, Job, Cursor string
	Offset                                              int
}

func (o Options) listSelectedOperations(r *http.Request, tenant string, f operations.ListFilter, match selector.Selector, limit int) ([]operations.Operation, string, error) {
	query, cursor := r.URL.Query().Get("selector"), r.URL.Query().Get("cursor")
	if query == "" {
		return o.Operations.List(r.Context(), tenant, f, cursor, limit)
	}
	c := operationSelectorCursor{Selector: query, Tenant: tenant, Device: f.Device, Status: r.URL.Query().Get("status"), Type: f.Type, Job: f.Job}
	if cursor != "" {
		var decoded operationSelectorCursor
		b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, "opsel."))
		if !strings.HasPrefix(cursor, "opsel.") || err != nil || json.Unmarshal(b, &decoded) != nil || decoded.Selector != c.Selector || decoded.Tenant != c.Tenant || decoded.Device != c.Device || decoded.Status != c.Status || decoded.Type != c.Type || decoded.Job != c.Job || decoded.Offset < 0 || decoded.Offset >= 500 {
			return nil, "", store.ErrCursor
		}
		c = decoded
	}
	items := make([]operations.Operation, 0, limit)
	// Maximum-sized batches keep sparse and no-match scans batch-scaled.
	for {
		if err := r.Context().Err(); err != nil {
			return nil, "", err
		}
		batch, next, err := o.Operations.List(r.Context(), tenant, f, c.Cursor, 500)
		if err != nil {
			return nil, "", err
		}
		for i := c.Offset; i < len(batch); i++ {
			op := batch[i]
			d, err := o.Devices.Get(tenant, op.Device)
			if errors.Is(err, store.ErrDeviceNotFound) {
				continue
			}
			if err != nil {
				return nil, "", err
			}
			if !match.Matches(d.Labels) {
				continue
			}
			if len(items) == limit {
				c.Offset = i
				b, _ := json.Marshal(c)
				return items, "opsel." + base64.RawURLEncoding.EncodeToString(b), nil
			}
			items = append(items, op)
		}
		if next == "" {
			return items, "", nil
		}
		c.Cursor, c.Offset = next, 0
	}
}

func operationError(w http.ResponseWriter, err error) {
	var prefix *operations.ErrPrefixUnsupported
	switch {
	case errors.As(err, &prefix):
		WriteProblem(w, 501, "prefix_unsupported", err.Error())
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrTenant):
		WriteProblem(w, 404, "not_found", "Operation not found")
	case errors.Is(err, operations.ErrNotRegistered):
		WriteProblem(w, 409, "not_registered", "Device not registered")
	case errors.Is(err, operations.ErrConflict):
		WriteProblem(w, 409, "already_sent", "Operation is no longer queued")
	case errors.Is(err, operations.ErrInvalid):
		WriteProblem(w, 400, "invalid_operation", "Invalid operation")
	default:
		WriteProblem(w, 500, "internal_error", "Cannot process operation")
	}
}
