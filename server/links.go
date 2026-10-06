package server

import (
	"sync"

	"github.com/fiumaralabs/lwm2m"
	"github.com/fiumaralabs/lwm2m/link"
)

// parseObjectLinks interprets a Register/Update payload (REG-07..10). It
// returns the object list, the alternate path ("" for "/", GEN-08) and the
// content formats advertised on the root link.
func parseObjectLinks(payload []byte) ([]link.Object, string, []lwm2m.ContentFormat, error) {
	if payload == nil {
		return nil, "", nil, nil
	}
	ls, err := link.Parse(string(payload))
	if err != nil {
		return nil, "", nil, err
	}
	r, err := link.ParseRegistration(ls)
	if err != nil {
		return nil, "", nil, err
	}
	root := r.Root
	if root == "/" {
		root = ""
	}
	return r.Objects, root, r.ContentFormats, nil
}

// profileCache maps Profile IDs to object lists: pre-configured ones from
// Config.Profiles (PROF-04) and dynamic ones learned from registrations
// that carried both the pid and the full list (PROF-11).
type profileCache struct {
	mu sync.RWMutex
	m  map[string][]link.Object
}

func newProfileCache(pre map[string][]link.Object) *profileCache {
	c := &profileCache{m: map[string][]link.Object{}}
	for k, v := range pre {
		c.m[k] = v
	}
	return c
}

// resolveProfiles returns the union of the lists for pids; ok=false if any
// pid is unknown (PROF-06).
func (s *Server) resolveProfiles(pids []string) ([]link.Object, bool) {
	s.profiles.mu.RLock()
	defer s.profiles.mu.RUnlock()
	var out []link.Object
	for _, id := range pids {
		l, ok := s.profiles.m[id]
		if !ok {
			return nil, false
		}
		out = mergeObjects(out, l)
	}
	return out, true
}

// learnProfiles records a dynamic Profile ID sent together with its list.
// Only a single dynamic pid can be learned unambiguously.
func (s *Server) learnProfiles(pids []string, objs []link.Object) {
	if len(pids) != 1 {
		return
	}
	s.profiles.mu.Lock()
	defer s.profiles.mu.Unlock()
	if _, ok := s.profiles.m[pids[0]]; !ok {
		s.profiles.m[pids[0]] = objs
	}
}
