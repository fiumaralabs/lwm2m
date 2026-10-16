package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/server"
)

// obsHub runs the page's live values: one LwM2M Observe per (registration,
// path), shared by every visitor watching it and streamed to them as
// server-sent events. Limits: max streams open in total, perIP per visitor,
// perDevice distinct paths per client, each stream closed after dur, and
// notifications forwarded at most every minGap. The Observe is cancelled on
// the client when the last visitor leaves. /0 and /21 are refused like reads.
type obsHub struct {
	w         *webAPI
	max       int
	perIP     int
	perDevice int
	dur       time.Duration
	minGap    time.Duration

	startMu sync.Mutex // serialises Observe starts, so one path is observed once

	mu      sync.Mutex
	streams map[string]*obsStream // regID + " " + path
	byObs   map[string]*obsStream // Observation.ID
	ips     map[string]int
	subs    int
}

type obsStream struct {
	key, regID string
	ob         *server.Observation
	subs       map[*obsSub]bool
	last       []byte // last "value" event, sent to visitors who join
	lastSent   time.Time
	ended      bool
}

type obsSub struct {
	ch  chan []byte
	end chan string
}

func newObsHub(w *webAPI, max, perIP int, dur time.Duration) *obsHub {
	return &obsHub{w: w, max: max, perIP: perIP, perDevice: 4, dur: dur, minGap: 250 * time.Millisecond,
		streams: map[string]*obsStream{}, byObs: map[string]*obsStream{}, ips: map[string]int{}}
}

var (
	errTooManyIP     = errors.New("you already have the maximum number of live values open; close one first")
	errTooMany       = errors.New("the server has too many live values open right now; try again in a few minutes")
	errTooManyDevice = errors.New("this device already has the maximum number of live values open")
)

func (h *obsHub) acquire(ip string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ips[ip] >= h.perIP {
		return errTooManyIP
	}
	if h.subs >= h.max {
		return errTooMany
	}
	h.subs++
	h.ips[ip]++
	return nil
}

func (h *obsHub) releaseIP(ip string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs--
	if h.ips[ip]--; h.ips[ip] <= 0 {
		delete(h.ips, ip)
	}
}

func sseEvent(name string, v any) []byte {
	b, _ := json.Marshal(v)
	return []byte("event: " + name + "\ndata: " + string(b) + "\n\n")
}

// attach joins sub to the stream of reg/p, starting the Observe if needed.
func (h *obsHub) attach(reg *server.Registration, p lwm2m.Path, sub *obsSub) (*obsStream, []byte, int, error) {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	key := reg.ID + " " + p.String()
	h.mu.Lock()
	if st, ok := h.streams[key]; ok && !st.ended {
		st.subs[sub] = true
		last := st.last
		h.mu.Unlock()
		return st, last, 0, nil
	}
	n := 0
	for _, st := range h.streams {
		if st.regID == reg.ID {
			n++
		}
	}
	h.mu.Unlock()
	if n >= h.perDevice {
		return nil, nil, 429, errTooManyDevice
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	ob, resp, err := h.w.srv.Observe(ctx, reg.Endpoint, p, server.ObserveOptions{})
	if err != nil {
		return nil, nil, 502, err
	}
	if ob == nil {
		return nil, nil, 502, fmt.Errorf("the client refused the Observe: %s", server.CodeString(resp.Code))
	}
	st := &obsStream{key: key, regID: reg.ID, ob: ob, subs: map[*obsSub]bool{sub: true}, lastSent: time.Now(),
		last: sseEvent("value", responseJSON(resp, time.Since(start)))}
	h.mu.Lock()
	h.streams[key] = st
	h.byObs[ob.ID] = st
	h.mu.Unlock()
	return st, st.last, 0, nil
}

// detach removes sub; the last one out cancels the Observe on the client.
func (h *obsHub) detach(st *obsStream, sub *obsSub) {
	h.mu.Lock()
	delete(st.subs, sub)
	cancel := len(st.subs) == 0 && !st.ended
	if cancel {
		h.endLocked(st, "")
	}
	h.mu.Unlock()
	if cancel {
		go func() {
			ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			_, _ = h.w.srv.CancelObservation(ctx, st.ob, true)
		}()
	}
}

// endLocked closes a stream; h.mu is held.
func (h *obsHub) endLocked(st *obsStream, reason string) {
	st.ended = true
	if h.streams[st.key] == st {
		delete(h.streams, st.key)
	}
	delete(h.byObs, st.ob.ID)
	if reason == "" {
		return
	}
	for s := range st.subs {
		select {
		case s.end <- reason:
		default:
		}
	}
}

func (h *obsHub) endRegistration(regID, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, st := range h.streams {
		if st.regID == regID {
			h.endLocked(st, reason)
		}
	}
}

// open reports the number of streams (distinct Observes) and visitors.
func (h *obsHub) open() (streams, subs int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.streams), h.subs
}

func (h *obsHub) onEvent(e server.Event) {
	switch e := e.(type) {
	case server.Notification:
		h.mu.Lock()
		defer h.mu.Unlock()
		st, ok := h.byObs[e.Observation.ID]
		if !ok {
			return
		}
		if !e.Response.Success() { // the server already dropped the observation
			h.endLocked(st, "the client ended the observation ("+server.CodeString(e.Response.Code)+")")
			return
		}
		msg := sseEvent("value", responseJSON(e.Response, 0))
		st.last = msg
		if time.Since(st.lastSent) < h.minGap {
			return // a fast client: visitors get the next one, or the latest on join
		}
		st.lastSent = time.Now()
		for s := range st.subs {
			select {
			case s.ch <- msg:
			default: // slow visitor: drop
			}
		}
	case server.Deregistered:
		h.endRegistration(e.Registration.ID, "the client "+e.Reason.String())
	case server.Registered:
		if e.Replaced != nil {
			h.endRegistration(e.Replaced.ID, "the client registered again; start a new live view")
		}
	}
}

func (h *obsHub) serve(rw http.ResponseWriter, r *http.Request) {
	p, ok := checkPath(rw, r)
	if !ok {
		return
	}
	if p.Len() < 2 {
		writeJSON(rw, 400, map[string]string{"error": "live values need an instance or resource path, e.g. /3303/0/5700"})
		return
	}
	reg, ok := h.w.srv.Store().ByEndpoint(r.PathValue("ep"))
	if !ok {
		writeJSON(rw, 404, map[string]string{"error": "not registered"})
		return
	}
	if !reg.HasObject(p.Object()) {
		writeJSON(rw, 404, map[string]string{"error": "the client did not register object " + strconv.Itoa(int(p.Object()))})
		return
	}
	if _, ok := rw.(http.Flusher); !ok {
		writeJSON(rw, 500, map[string]string{"error": "streaming unsupported"})
		return
	}
	ip := h.w.clientIP(r)
	if err := h.acquire(ip); err != nil {
		writeJSON(rw, 429, map[string]string{"error": err.Error()})
		return
	}
	defer h.releaseIP(ip)
	if !h.w.rateLimit(rw, r) {
		return
	}
	sub := &obsSub{ch: make(chan []byte, 8), end: make(chan string, 1)}
	st, first, code, err := h.attach(reg, p, sub)
	if err != nil {
		if code == 0 {
			code = 502
		}
		writeJSON(rw, code, map[string]string{"error": err.Error()})
		return
	}
	defer h.detach(st, sub)

	rc := http.NewResponseController(rw)
	_ = rc.SetWriteDeadline(time.Time{}) // the server's WriteTimeout would cut the stream
	hd := rw.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store, no-transform")
	hd.Set("X-Accel-Buffering", "no")
	rw.WriteHeader(200)
	deadline := time.Now().Add(h.dur)
	write := func(b []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := rw.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write([]byte("retry: 15000\n\n")) || !write(sseEvent("open", map[string]any{"path": p.String(),
		"until": deadline.UTC(), "observe_for_s": int64(h.dur / time.Second)})) || !write(first) {
		return
	}
	limit := time.NewTimer(h.dur)
	defer limit.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-limit.C:
			write(sseEvent("end", map[string]string{"reason": fmt.Sprintf("live values stop after %d minutes on this demo", int(h.dur.Minutes()))}))
			return
		case reason := <-sub.end:
			write(sseEvent("end", map[string]string{"reason": reason}))
			return
		case <-ping.C:
			if !write([]byte(": ping\n\n")) {
				return
			}
		case msg := <-sub.ch:
			if !write(msg) {
				return
			}
		}
	}
}
