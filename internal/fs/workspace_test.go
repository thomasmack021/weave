package fs

import (
	"errors"
	"os"
	"testing"

	"github.com/spf13/afero"
)

const resolvedDir = "terraform/env/dev"

// TestWorkspace_Read seeds a file at the resolved path directly via MemMapFs and
// asserts that Read("main.tf") resolves terraform/env/<env>/ transparently.
func TestWorkspace_Read(t *testing.T) {
	mem := afero.NewMemMapFs()
	const want = "module \"x\" {}"
	if err := afero.WriteFile(mem, resolvedDir+"/main.tf", []byte(want), 0o644); err != nil {
		t.Fatalf("seeding filesystem: %v", err)
	}

	ws := NewWorkspace(mem, "dev")

	got, err := ws.Read("main.tf")
	if err != nil {
		t.Fatalf("Read returned unexpected error: %v", err)
	}
	if string(got) != want {
		t.Errorf("Read = %q, want %q", got, want)
	}
}

// TestWorkspace_Read_NotFound asserts a missing file surfaces a catchable
// os.ErrNotExist.
func TestWorkspace_Read_NotFound(t *testing.T) {
	mem := afero.NewMemMapFs()
	ws := NewWorkspace(mem, "dev")

	_, err := ws.Read("missing.tf")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Read error = %v, want it to wrap os.ErrNotExist", err)
	}
}

// TestWorkspace_WriteAtomic asserts WriteAtomic creates the environment
// directory and writes the file with the correct content at the resolved path.
func TestWorkspace_WriteAtomic(t *testing.T) {
	mem := afero.NewMemMapFs()
	ws := NewWorkspace(mem, "dev")

	if err := ws.WriteAtomic("main.tf", []byte("content")); err != nil {
		t.Fatalf("WriteAtomic returned unexpected error: %v", err)
	}

	exists, err := afero.DirExists(mem, resolvedDir)
	if err != nil {
		t.Fatalf("checking directory: %v", err)
	}
	if !exists {
		t.Errorf("expected directory %q to be created", resolvedDir)
	}

	got, err := afero.ReadFile(mem, resolvedDir+"/main.tf")
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != "content" {
		t.Errorf("written content = %q, want %q", got, "content")
	}
}
