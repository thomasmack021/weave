package pipeline

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const (
	testProjectID   = "dev-123"
	testStatePrefix = "weave/dev"
	testEnv         = "dev"
)

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

// TestEnsureStep_CreatesNew: with no existing pipeline, a fresh one is generated
// containing the terraform step for the environment.
func TestEnsureStep_CreatesNew(t *testing.T) {
	want := mustRead(t, "pipeline_expected_new.yaml")

	got, changed, err := EnsureStep(nil, testProjectID, testStatePrefix, testEnv)
	if err != nil {
		t.Fatalf("EnsureStep returned unexpected error: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true (fresh pipeline created)")
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output is not byte-identical to pipeline_expected_new.yaml\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(want), want, len(got), got)
	}
}

// TestEnsureStep_AppendsExisting: an existing pipeline without the terraform step
// gains it, while its pre-existing steps are retained.
func TestEnsureStep_AppendsExisting(t *testing.T) {
	base := mustRead(t, "pipeline_base.yaml")
	want := mustRead(t, "pipeline_expected_append.yaml")

	got, changed, err := EnsureStep(base, testProjectID, testStatePrefix, testEnv)
	if err != nil {
		t.Fatalf("EnsureStep returned unexpected error: %v", err)
	}
	if !changed {
		t.Errorf("changed = false, want true (step appended)")
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output is not byte-identical to pipeline_expected_append.yaml\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(want), want, len(got), got)
	}
}

// TestEnsureStep_Idempotent: re-running against a pipeline that already contains
// the terraform step makes no change and returns the input untouched.
func TestEnsureStep_Idempotent(t *testing.T) {
	existing := mustRead(t, "pipeline_expected_append.yaml")

	got, changed, err := EnsureStep(existing, testProjectID, testStatePrefix, testEnv)
	if err != nil {
		t.Fatalf("EnsureStep returned unexpected error: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (step already present)")
	}
	if !bytes.Equal(got, existing) {
		t.Errorf("output is not byte-identical to the input\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(existing), existing, len(got), got)
	}
}
