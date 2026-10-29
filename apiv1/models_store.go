package apiv1

import (
	"sync"

	"github.com/fiumaralabs/lwm2m/model"
)

// ModelStore stores uploaded model definitions by tenant. List returns a snapshot.
type ModelStore interface {
	List(tenant string) []*model.Object
	Put(tenant string, object *model.Object)
	Delete(tenant string, id uint16, version model.Version) bool
}

type memoryModelStore struct {
	mu      sync.RWMutex
	objects map[string]map[uint16]map[model.Version]*model.Object
}

func (s *memoryModelStore) List(tenant string) []*model.Object {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Object
	for _, versions := range s.objects[tenant] {
		for _, object := range versions {
			out = append(out, object)
		}
	}
	return out
}
func (s *memoryModelStore) Put(tenant string, object *model.Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects == nil {
		s.objects = make(map[string]map[uint16]map[model.Version]*model.Object)
	}
	if s.objects[tenant] == nil {
		s.objects[tenant] = make(map[uint16]map[model.Version]*model.Object)
	}
	if s.objects[tenant][object.ID] == nil {
		s.objects[tenant][object.ID] = make(map[model.Version]*model.Object)
	}
	s.objects[tenant][object.ID][object.Version] = object
}
func (s *memoryModelStore) Delete(tenant string, id uint16, version model.Version) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := s.objects[tenant][id]
	if _, ok := versions[version]; !ok {
		return false
	}
	delete(versions, version)
	return true
}
