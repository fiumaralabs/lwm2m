package operations

// SessionCount exposes ownership bookkeeping only to external regression tests.
func SessionCount(e *Executor) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.sessions)
}
