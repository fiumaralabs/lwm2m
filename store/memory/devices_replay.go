package memory

import (
	"context"
	"net/http"
	"time"

	"github.com/fiumaralabs/lwm2m/store"
)

type deviceReplayKey struct{ tenant, key string }
type deviceReplay struct {
	done   chan struct{}
	at     time.Time
	status int
	header http.Header
	body   []byte
}

func (*Devices) AtomicDeviceReplaySupported() {}

// saveCreateReplay runs under the device mutation lock before it is released.
func (s *Devices) saveCreateReplay(ctx context.Context, tenant string, d store.Device) {
	r := store.DeviceCreateReplayFrom(ctx)
	if r == nil {
		return
	}
	status, header, body := r.Render(d)
	s.replayMu.Lock()
	defer s.replayMu.Unlock()
	if entry := s.replays[deviceReplayKey{tenant, r.Key}]; entry != nil {
		entry.status, entry.header, entry.body = status, header.Clone(), append([]byte(nil), body...)
		entry.at = time.Now()
	}
}

// ReplayDeviceCreate serializes requests for one tenant and key and retains the first response.
func (s *Devices) ReplayDeviceCreate(ctx context.Context, tenant, key string, run func() (int, http.Header, []byte)) (int, http.Header, []byte, error) {
	k := deviceReplayKey{tenant, key}
	for {
		s.replayMu.Lock()
		if s.replays == nil {
			s.replays = make(map[deviceReplayKey]*deviceReplay)
		}
		now := time.Now()
		for key, entry := range s.replays {
			if !entry.at.IsZero() && now.Sub(entry.at) >= 24*time.Hour {
				delete(s.replays, key)
			}
		}
		if entry := s.replays[k]; entry != nil {
			if !entry.at.IsZero() {
				status, header, body := entry.status, entry.header.Clone(), append([]byte(nil), entry.body...)
				s.replayMu.Unlock()
				return status, header, body, nil
			}
			done := entry.done
			s.replayMu.Unlock()
			select {
			case <-done:
			case <-ctx.Done():
				return 0, nil, nil, ctx.Err()
			}
			continue
		}
		entry := &deviceReplay{done: make(chan struct{})}
		s.replays[k] = entry
		s.replayMu.Unlock()
		completed := false
		func() {
			defer func() {
				s.replayMu.Lock()
				if !completed && entry.at.IsZero() {
					delete(s.replays, k)
				}
				close(entry.done)
				s.replayMu.Unlock()
			}()
			status, header, body := run()
			s.replayMu.Lock()
			if entry.at.IsZero() {
				entry.status, entry.header, entry.body = status, header, body
				entry.at = time.Now()
			}
			completed = true
			s.replayMu.Unlock()
		}()
		return entry.status, entry.header.Clone(), append([]byte(nil), entry.body...), nil
	}
}
