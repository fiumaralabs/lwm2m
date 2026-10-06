package leshanapi

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
)

// hub fans server events out to /event streams (EventServlet, §3.8).
type hub struct {
	mu   sync.Mutex
	subs map[*sub]struct{}
}

type sub struct {
	ep string // "" = every endpoint
	ch chan []byte
}

func (h *hub) publish(name, ep string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	// Jetty's EventSource.Emitter framing, CRLF line ends.
	frame := []byte("event: " + name + "\r\ndata: " + string(b) + "\r\n\r\n")
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.ep != "" && s.ep != ep {
			continue
		}
		select {
		case s.ch <- frame:
		default: // ponytail: a stalled reader loses events rather than blocking CoAP; buffer is 256
		}
	}
}

// OnEvent turns native events into Leshan SSE events. Install it as
// server.Config.OnEvent; it never blocks.
func (a *API) OnEvent(e server.Event) {
	switch e := e.(type) {
	case server.Registered:
		a.hub.publish("REGISTRATION", e.Registration.Endpoint, newRegistrationJSON(e.Registration, nil))
	case server.Updated:
		r := e.Registration
		u := map[string]any{"registrationId": r.ID, "address": leshanAddr(r.Addr), "secure": r.Identity.Secure(),
			"additionalAttributes": map[string]string{}}
		if r.Lifetime != e.Previous.Lifetime {
			u["lifetime"] = int64(r.Lifetime.Seconds())
		}
		a.hub.publish("UPDATED", r.Endpoint, map[string]any{"registration": newRegistrationJSON(r, nil), "update": u})
	case server.Deregistered:
		a.hub.publish("DEREGISTRATION", e.Registration.Endpoint, newRegistrationJSON(e.Registration, nil))
	case server.Awake:
		a.hub.publish("AWAKE", e.Registration.Endpoint, map[string]string{"ep": e.Registration.Endpoint})
	case server.Notification:
		r, ob, resp := e.Registration, e.Observation, e.Response
		if !resp.Success() || resp.DecodeErr != nil {
			return // Leshan would put "val":null, which the harness cannot parse
		}
		sch := a.schema(r)
		if ob.Composite {
			paths := make([]string, len(ob.Paths))
			for i, p := range ob.Paths {
				paths[i] = p.String()
			}
			a.hub.publish("NOTIFICATION", r.Endpoint, map[string]any{"ep": r.Endpoint, "kind": "composite",
				"val": compositeContent(ob.Paths, resp.Nodes, sch), "paths": paths})
			return
		}
		val, ok := encodeNode(ob.Paths[0], resp.Nodes, sch)
		if !ok {
			return
		}
		a.hub.publish("NOTIFICATION", r.Endpoint, map[string]any{"ep": r.Endpoint, "kind": "single",
			"res": ob.Paths[0].String(), "val": val})
	case server.SendReceived:
		// getMostRecentNodes: one entry per record path, latest timestamp wins.
		latest := map[lwm2m.Path]lwm2m.Node{}
		for _, n := range e.Nodes {
			if old, ok := latest[n.Path]; !ok || !old.HasTime || (n.HasTime && n.Time >= old.Time) {
				latest[n.Path] = n
			}
		}
		val := map[string]any{}
		sch := a.schema(e.Registration)
		for p, n := range latest {
			if v, ok := encodeNode(p, []lwm2m.Node{n}, sch); ok {
				val[p.String()] = v
			}
		}
		a.hub.publish("SEND", e.Registration.Endpoint, map[string]any{"ep": e.Registration.Endpoint, "val": val})
	}
}

// serveEvents streams events. The harness requests "/event?<ep>", so "ep"
// is absent and the stream carries every endpoint, as in Leshan.
func (a *API) serveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	s := &sub{ep: r.URL.Query().Get("ep"), ch: make(chan []byte, 256)}
	a.hub.mu.Lock()
	a.hub.subs[s] = struct{}{}
	a.hub.mu.Unlock()
	defer func() {
		a.hub.mu.Lock()
		delete(a.hub.subs, s)
		a.hub.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream;charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	// The heartbeat (a bare CRLF, as Jetty sends) keeps the harness's
	// line iterator moving so its own timeout check runs (§3.8).
	t := time.NewTicker(a.opts.Heartbeat)
	defer t.Stop()
	for {
		var b []byte
		select {
		case <-r.Context().Done():
			return
		case b = <-s.ch:
		case <-t.C:
			b = []byte("\r\n")
		}
		if _, err := w.Write(b); err != nil {
			return
		}
		fl.Flush()
	}
}
