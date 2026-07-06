package registry

import (
	"context"
	"errors"
)

// ErrModuleNotFound is returned by a ModuleRegistry when no module matches the
// requested name. Callers should match it with errors.Is.
var ErrModuleNotFound = errors.New("module not found")

// ModuleRegistry resolves a module name to its specification. Implementations
// may source modules from a remote manifest, a local cache, or an in-memory
// fixture; the domain layer depends only on this interface. The context is
// threaded for forward compatibility with remote (HTTP Git API) sources.
type ModuleRegistry interface {
	// Resolve returns the ModuleSpec registered under name. It returns an error
	// wrapping ErrModuleNotFound when no such module exists.
	Resolve(ctx context.Context, name string) (*ModuleSpec, error)

	// List returns every registered ModuleSpec, in a stable order, for
	// enumeration commands such as `weave list`.
	List(ctx context.Context) ([]ModuleSpec, error)
}
