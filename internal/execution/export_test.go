package execution

// RuntimeForTest exposes the stored registration to the external contract test.
func (s *MemoryStore) RuntimeForTest(id string) (RuntimeRegistration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	registration, exists := s.runtimes[id]
	return registration, exists
}

// TaskForTest exposes the stored task to the external contract test.
func (s *MemoryStore) TaskForTest(id string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks[id]
}
