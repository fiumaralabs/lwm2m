package apiv1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
	"github.com/fiumaralabs/lwm2m/store/memory"
)

// EventOptions configures the tenant-local SSE event stream.
type EventOptions struct {
	// Events defaults to a memory event log allocated once per New call.
	Events store.Events `json:"-"`
	// EventKeepAlive defaults to fifteen seconds when nonpositive.
	EventKeepAlive time.Duration `json:"-"`
}

func (o EventOptions) eventHandler() http.Handler {
	if o.Events == nil {
		o.Events = &memory.Events{}
	}
	if o.EventKeepAlive <= 0 {
		o.EventKeepAlive = 15 * time.Second
	}
	return RequireScope("devices:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			WriteProblem(w, 405, "method_not_allowed", "Expected GET")
			return
		}
		q, queryErr := url.ParseQuery(r.URL.RawQuery)
		f := store.EventFilter{Type: q.Get("type"), Device: q.Get("device"), After: r.Header.Get("Last-Event-ID"), Limit: 100}
		if f.After == "" {
			f.After = q.Get("since")
		}
		invalid := queryErr != nil || f.After != "" && !store.ValidSequence(f.After)
		for _, key := range []string{"type", "device", "since"} {
			if len(q[key]) > 1 {
				invalid = true
			}
		}
		if q.Has("type") {
			for _, pattern := range strings.Split(f.Type, ",") {
				base := strings.TrimSuffix(pattern, "*")
				if base == "" {
					invalid = true
				}
				for _, c := range base {
					if c <= ' ' || c >= 127 || c == '*' {
						invalid = true
					}
				}
			}
		}
		if invalid {
			WriteProblem(w, 400, "invalid_query", "Invalid event query")
			return
		}
		tenant := TenantFrom(r.Context())
		items, reset, err := o.Events.List(tenant, f)
		if err != nil {
			WriteProblem(w, 500, "internal_error", "Cannot list events")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		rc := http.NewResponseController(w)
		write := func(frame string) bool {
			_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			// Bound the write and flush, not the idle time between frames:
			// HTTP/2 resets the stream when an armed deadline expires.
			defer rc.SetWriteDeadline(time.Time{})
			_, err := fmt.Fprint(w, frame)
			return err == nil && rc.Flush() == nil
		}
		if !write("") {
			return
		}
		poll := time.NewTicker(50 * time.Millisecond)
		defer poll.Stop()
		keep := time.NewTicker(o.EventKeepAlive)
		defer keep.Stop()
		for {
			if r.Context().Err() != nil {
				return
			}
			if reset {
				if !write("event: reset\ndata: {}\n\n") {
					return
				}
				f.After = ""
				items, reset, err = o.Events.List(tenant, f)
				if err != nil {
					return
				}
			}
			for _, e := range items {
				raw, err := json.Marshal(e)
				if err != nil || !write("id: "+e.Sequence+"\nevent: "+e.Type+"\ndata: "+string(raw)+"\n\n") {
					return
				}
				f.After = e.Sequence
			}
			if len(items) < f.Limit {
				select {
				case <-r.Context().Done():
					return
				case <-keep.C:
					if !write(": keepalive\n\n") {
						return
					}
				case <-poll.C:
				}
			} else if r.Context().Err() != nil {
				return
			}
			items, reset, err = o.Events.List(tenant, f)
			if err != nil {
				return
			}
		}
	}))
}
