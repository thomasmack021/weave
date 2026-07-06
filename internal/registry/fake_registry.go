package registry

import (
	"context"
	"fmt"
	"sort"
)

// FakeRegistry is an in-memory ModuleRegistry backed by a fixed set of specs.
// It exists for unit tests of the domain layer and for offline development; it
// performs no I/O.
type FakeRegistry struct {
	modules map[string]ModuleSpec
}

// NewFakeRegistry builds a FakeRegistry from the given specs, keyed by name.
// Passing no specs yields an empty registry whose Resolve always reports
// ErrModuleNotFound. When two specs share a name, the last one wins.
func NewFakeRegistry(specs ...ModuleSpec) *FakeRegistry {
	modules := make(map[string]ModuleSpec, len(specs))
	for _, s := range specs {
		modules[s.Name] = s
	}
	return &FakeRegistry{modules: modules}
}

// Resolve returns a copy of the ModuleSpec registered under name, or an error
// wrapping ErrModuleNotFound if none is registered. A copy is returned so
// callers cannot mutate the registry's internal state through the pointer.
func (r *FakeRegistry) Resolve(_ context.Context, name string) (*ModuleSpec, error) {
	spec, ok := r.modules[name]
	if !ok {
		return nil, fmt.Errorf("registry: %q: %w", name, ErrModuleNotFound)
	}
	return &spec, nil
}

// List returns every registered ModuleSpec sorted by name, so callers get a
// deterministic order regardless of map iteration.
func (r *FakeRegistry) List(_ context.Context) ([]ModuleSpec, error) {
	specs := make([]ModuleSpec, 0, len(r.modules))
	for _, s := range r.modules {
		specs = append(specs, s)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs, nil
}
