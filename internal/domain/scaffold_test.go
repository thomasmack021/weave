package domain

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/thomasmack/weave/internal/fs"
	"github.com/thomasmack/weave/internal/registry"
)

// newTestService wires a Service over an in-memory filesystem and an empty fake
// registry (Day 1 needs no module resolution). It returns the Service and the
// underlying MemMapFs so tests can assert what was written.
func newTestService(t *testing.T) (*Service, afero.Fs) {
	t.Helper()
	mem := afero.NewMemMapFs()
	ws := fs.NewWorkspace(mem, "dev")
	svc := NewService(registry.NewFakeRegistry(), ws, "dev")
	return svc, mem
}

func mustReadString(t *testing.T, fsys afero.Fs, path string) string {
	t.Helper()
	b, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func TestService_Scaffold_HappyPath(t *testing.T) {
	svc, mem := newTestService(t)

	changes, err := svc.Scaffold(context.Background(), ScaffoldRequest{
		ProjectID:   "dev-123",
		StatePrefix: "weave/dev",
	})
	if err != nil {
		t.Fatalf("Scaffold returned unexpected error: %v", err)
	}
	if !changes.Changed() {
		t.Errorf("changes.Changed() = false, want true on initial scaffold")
	}

	for _, name := range []string{"main.tf", "dev.tfvars", "pipeline.yaml"} {
		path := "terraform/env/dev/" + name
		exists, err := afero.Exists(mem, path)
		if err != nil {
			t.Fatalf("checking %s: %v", path, err)
		}
		if !exists {
			t.Errorf("expected %s to be written", path)
		}
	}

	tfvars := mustReadString(t, mem, "terraform/env/dev/dev.tfvars")
	if !strings.Contains(tfvars, `project_id = "dev-123"`) {
		t.Errorf("dev.tfvars missing project_id assignment, got:\n%s", tfvars)
	}

	pipeline := mustReadString(t, mem, "terraform/env/dev/pipeline.yaml")
	if !strings.Contains(pipeline, "project_id: dev-123") {
		t.Errorf("pipeline.yaml missing project_id, got:\n%s", pipeline)
	}
	if !strings.Contains(pipeline, "state_prefix: weave/dev") {
		t.Errorf("pipeline.yaml missing state_prefix, got:\n%s", pipeline)
	}

	mainTF := mustReadString(t, mem, "terraform/env/dev/main.tf")
	if len(strings.TrimSpace(mainTF)) == 0 {
		t.Errorf("main.tf is empty, want baseline content")
	}
}

func TestService_Scaffold_Idempotent(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	req := ScaffoldRequest{ProjectID: "dev-123", StatePrefix: "weave/dev"}

	if _, err := svc.Scaffold(ctx, req); err != nil {
		t.Fatalf("first Scaffold returned unexpected error: %v", err)
	}

	changes, err := svc.Scaffold(ctx, req)
	if err != nil {
		t.Fatalf("second Scaffold returned unexpected error: %v", err)
	}
	if changes.Changed() {
		t.Errorf("changes.Changed() = true on re-run, want false")
	}
	if len(changes.Files) == 0 {
		t.Errorf("expected ChangeSet to list files as unchanged, got none")
	}
	for _, f := range changes.Files {
		if f.Action != ActionUnchanged {
			t.Errorf("file %s action = %q, want %q", f.Path, f.Action, ActionUnchanged)
		}
	}
}
