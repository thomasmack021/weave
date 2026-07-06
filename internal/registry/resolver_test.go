package registry

import (
	"context"
	"errors"
	"testing"
)

// Compile-time assertion that the in-memory fake satisfies the interface the
// domain layer will depend on.
var _ ModuleRegistry = (*FakeRegistry)(nil)

// newFakeFromManifest builds a FakeRegistry from the shared validManifest
// fixture, exercising the fake against real, parsed ModuleSpecs.
func newFakeFromManifest(t *testing.T) *FakeRegistry {
	t.Helper()
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("setup: ParseManifest returned unexpected error: %v", err)
	}
	return NewFakeRegistry(m.Modules...)
}

func TestFakeRegistry_ResolveKnownModule(t *testing.T) {
	r := newFakeFromManifest(t)

	spec, err := r.Resolve(context.Background(), "cloud-run")
	if err != nil {
		t.Fatalf("Resolve(%q) returned unexpected error: %v", "cloud-run", err)
	}
	if spec == nil {
		t.Fatalf("Resolve(%q) returned nil spec, want non-nil", "cloud-run")
	}
	if got, want := spec.Name, "cloud-run"; got != want {
		t.Errorf("spec.Name = %q, want %q", got, want)
	}
	if got, want := spec.DisplayName, "GCP Cloud Run Service"; got != want {
		t.Errorf("spec.DisplayName = %q, want %q", got, want)
	}
	if got, want := spec.Source, "git::https://github.com/acme/iac-modules.git//modules/cloud-run"; got != want {
		t.Errorf("spec.Source = %q, want %q", got, want)
	}
}

func TestFakeRegistry_ResolveSecondModule(t *testing.T) {
	r := newFakeFromManifest(t)

	spec, err := r.Resolve(context.Background(), "bigquery-dataset")
	if err != nil {
		t.Fatalf("Resolve(%q) returned unexpected error: %v", "bigquery-dataset", err)
	}
	if spec == nil {
		t.Fatalf("Resolve(%q) returned nil spec, want non-nil", "bigquery-dataset")
	}
	if got, want := spec.Name, "bigquery-dataset"; got != want {
		t.Errorf("spec.Name = %q, want %q", got, want)
	}
}

func TestFakeRegistry_ResolveUnknownModule(t *testing.T) {
	r := newFakeFromManifest(t)

	spec, err := r.Resolve(context.Background(), "unknown")
	if err == nil {
		t.Fatalf("Resolve(%q) returned nil error, want ErrModuleNotFound", "unknown")
	}
	if !errors.Is(err, ErrModuleNotFound) {
		t.Errorf("error = %v, want it to wrap ErrModuleNotFound", err)
	}
	if spec != nil {
		t.Errorf("Resolve(%q) returned spec %+v, want nil on error", "unknown", spec)
	}
}

func TestFakeRegistry_EmptyRegistry(t *testing.T) {
	r := NewFakeRegistry()

	if _, err := r.Resolve(context.Background(), "cloud-run"); !errors.Is(err, ErrModuleNotFound) {
		t.Errorf("Resolve on empty registry: error = %v, want ErrModuleNotFound", err)
	}
}
