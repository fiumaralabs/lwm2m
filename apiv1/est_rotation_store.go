package apiv1

// ESTIssuerRotator atomically replaces the config and its managed trust anchor.
type ESTIssuerRotator interface {
	PutConfigRotatingIssuerAnchor(tenant string, config ESTConfig, anchor TrustAnchor) (existed bool, err error)
}

type memoryESTIssuerRotator struct {
	configs *memoryESTConfigStore
	anchors *memoryTrustAnchorStore
}

// NewMemoryESTIssuerRotator binds the memory stores used for config and anchor reads.
// Both arguments must be memory stores created by this package.
func NewMemoryESTIssuerRotator(configs ESTConfigStore, anchors TrustAnchorStore) ESTIssuerRotator {
	return &memoryESTIssuerRotator{configs.(*memoryESTConfigStore), anchors.(*memoryTrustAnchorStore)}
}

func (s *memoryESTIssuerRotator) PutConfigRotatingIssuerAnchor(tenant string, c ESTConfig, anchor TrustAnchor) (bool, error) {
	s.configs.mu.Lock()
	defer s.configs.mu.Unlock()
	s.anchors.mu.Lock()
	defer s.anchors.mu.Unlock()
	old := make([]TrustAnchor, 0, 1)
	for id, a := range s.anchors.items[tenant] {
		if a.ESTIssuer {
			old = append(old, a)
			delete(s.anchors.items[tenant], id)
			s.anchors.count--
		}
	}
	anchor.ESTIssuer = true
	if err := s.anchors.create(tenant, anchor); err != nil {
		for _, a := range old {
			s.anchors.items[tenant][a.ID] = a
			s.anchors.count++
		}
		return false, err
	}
	_, existed := s.configs.items[tenant]
	s.configs.items[tenant] = c
	return existed, nil
}
