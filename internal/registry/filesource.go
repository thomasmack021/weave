package registry

import (
	"context"
	"fmt"
	"os"
)

// FileSource is a ModuleRegistry backed by a spec.yaml manifest on disk. It
// re-reads the file on every call, so a platform engineer's edit to the spec
// is reflected in the catalog without a redeploy or restart. It is the honest
// stepping stone to a remote (Git/HTTP) registry source: same interface,
// different origin.
type FileSource struct {
	path string
}

// Compile-time assertion that *FileSource implements ModuleRegistry.
var _ ModuleRegistry = (*FileSource)(nil)

// NewFileSource returns a FileSource reading the manifest at path. The file is
// not opened here; each Resolve/List call reads it afresh.
func NewFileSource(path string) *FileSource {
	return &FileSource{path: path}
}

// load reads and parses the manifest from disk. Called by every Resolve/List
// so the catalog always reflects the file's current content; a spec file is
// small and the read is cheap.
func (s *FileSource) load() (*Manifest, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("registry: reading spec file %s: %w", s.path, err)
	}
	return ParseManifest(data)
}

// Resolve returns a copy of the ModuleSpec named name from the manifest on
// disk, or an error wrapping ErrModuleNotFound if the manifest has no such
// module.
func (s *FileSource) Resolve(_ context.Context, name string) (*ModuleSpec, error) {
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	for i := range m.Modules {
		if m.Modules[i].Name == name {
			spec := m.Modules[i]
			return &spec, nil
		}
	}
	return nil, fmt.Errorf("registry: %q: %w", name, ErrModuleNotFound)
}

// List returns every ModuleSpec from the manifest on disk, preserving the
// manifest's declaration order (the platform team's chosen catalog order).
func (s *FileSource) List(_ context.Context) ([]ModuleSpec, error) {
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	return m.Modules, nil
}
