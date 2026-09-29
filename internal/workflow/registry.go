package workflow

import (
	"fmt"
	"sync"
)

type definitionKey struct {
	name    string
	version int
}

type Registry struct {
	mu    sync.RWMutex
	plans map[definitionKey]CompiledPlan
}

func NewRegistry() *Registry {
	return &Registry{plans: make(map[definitionKey]CompiledPlan)}
}

func (r *Registry) Publish(plan CompiledPlan) error {
	key := definitionKey{name: plan.Name, version: plan.Version}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, exists := r.plans[key]; exists {
		if existing.Digest != plan.Digest {
			return fmt.Errorf("%w: %s@%d", ErrDefinitionConflict, plan.Name, plan.Version)
		}
		return nil
	}
	r.plans[key] = clonePlan(plan)
	return nil
}

func (r *Registry) Get(name string, version int) (CompiledPlan, error) {
	key := definitionKey{name: name, version: version}
	r.mu.RLock()
	defer r.mu.RUnlock()
	plan, exists := r.plans[key]
	if !exists {
		return CompiledPlan{}, fmt.Errorf("%w: %s@%d", ErrDefinitionNotFound, name, version)
	}
	return clonePlan(plan), nil
}

func clonePlan(plan CompiledPlan) CompiledPlan {
	cloned := plan
	cloned.Definition = cloneDefinition(plan.Definition)
	cloned.Order = append([]string{}, plan.Order...)
	cloned.Nodes = make(map[string]Node, len(plan.Nodes))
	for id, node := range plan.Nodes {
		cloned.Nodes[id] = cloneNode(node)
	}
	return cloned
}
