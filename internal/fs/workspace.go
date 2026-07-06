// Package fs provides an environment-scoped workspace abstraction over an
// afero.Fs. It resolves logical filenames (e.g. "main.tf") to their concrete
// path under terraform/env/<env>/ and performs atomic writes, so the domain
// layer can be tested against an in-memory filesystem.
package fs

import (
	"fmt"
	"path"

	"github.com/spf13/afero"
)

// ReadWriter is the workspace contract the domain layer depends on. *Workspace
// satisfies it, and tests may substitute a fake.
type ReadWriter interface {
	Read(filename string) ([]byte, error)
	WriteAtomic(filename string, data []byte) error
	// Path returns the repo-relative path a logical filename resolves to, so the
	// CLI can map a ChangeSet to paths for git staging.
	Path(filename string) string
}

// Workspace resolves and reads/writes files within a single environment's
// directory (terraform/env/<env>/) on the underlying filesystem.
type Workspace struct {
	fs  afero.Fs
	env string
}

// Compile-time assertion that *Workspace implements ReadWriter.
var _ ReadWriter = (*Workspace)(nil)

// NewWorkspace returns a Workspace scoped to env, backed by appFs.
func NewWorkspace(appFs afero.Fs, env string) *Workspace {
	return &Workspace{fs: appFs, env: env}
}

// resolve maps a logical filename to its concrete path under the environment
// directory, e.g. "main.tf" -> "terraform/env/dev/main.tf".
func (w *Workspace) resolve(filename string) string {
	return path.Join("terraform/env", w.env, filename)
}

// Path returns the repo-relative path that a logical filename resolves to, e.g.
// "main.tf" -> "terraform/env/dev/main.tf". The CLI uses it to translate a
// ChangeSet's logical entries into paths to stage.
func (w *Workspace) Path(filename string) string {
	return w.resolve(filename)
}

// Read reads the named file from terraform/env/<env>/. A missing file surfaces
// an error wrapping os.ErrNotExist.
func (w *Workspace) Read(filename string) ([]byte, error) {
	return afero.ReadFile(w.fs, w.resolve(filename))
}

// WriteAtomic writes data to the named file in terraform/env/<env>/, creating
// the directory as needed. It writes to a temporary file in the same directory
// and renames it into place, so a reader never observes a partially written
// file (critical for never corrupting committed state).
func (w *Workspace) WriteAtomic(filename string, data []byte) error {
	full := w.resolve(filename)
	dir := path.Dir(full)

	if err := w.fs.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("fs: creating directory %s: %w", dir, err)
	}

	tmp, err := afero.TempFile(w.fs, dir, "."+path.Base(filename)+".tmp-")
	if err != nil {
		return fmt.Errorf("fs: creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		w.fs.Remove(tmpName)
		return fmt.Errorf("fs: writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		w.fs.Remove(tmpName)
		return fmt.Errorf("fs: closing temp file: %w", err)
	}

	if err := w.fs.Rename(tmpName, full); err != nil {
		w.fs.Remove(tmpName)
		return fmt.Errorf("fs: renaming temp file to %s: %w", full, err)
	}
	return nil
}
