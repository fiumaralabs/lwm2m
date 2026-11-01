package memory

import (
	"context"
	"net/http"
	"time"
)

type deviceReplayKey struct{ tenant, key string }
type deviceReplay struct {
	done   chan struct{}
	at     time.Time
	status int
	header http.Header
	body   []byte
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
				if !completed {
					delete(s.replays, k)
				}
				close(entry.done)
				s.replayMu.Unlock()
			}()
			entry.status, entry.header, entry.body = run()
			s.replayMu.Lock()
			entry.at = time.Now()
			completed = true
			s.replayMu.Unlock()
		}()
		return entry.status, entry.header.Clone(), append([]byte(nil), entry.body...), nil
	}
}
