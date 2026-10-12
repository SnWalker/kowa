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

// FactsForTest snapshots all persisted fields affected by one run without sharing mutable maps.
func (s *MemoryStore) FactsForTest(runID string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	facts := struct {
		Run   Run
		Nodes map[string]NodeRun
		Tasks map[string]Task
	}{Run: s.runs[runID], Nodes: make(map[string]NodeRun), Tasks: make(map[string]Task)}
	for id, node := range s.nodes {
		if node.RunID == runID {
			facts.Nodes[id] = cloneNodeRun(node)
		}
	}
	for id, task := range s.tasks {
		if _, ok := facts.Nodes[task.NodeRunID]; ok {
			facts.Tasks[id] = task
		}
	}
	return facts
}
