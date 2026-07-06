package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fileSourceManifestV1 is a minimal valid manifest with a single module, as a
// platform engineer would first publish it.
const fileSourceManifestV1 = `
apiVersion: weave.dev/v1
kind: ModuleManifest
metadata:
  registry: "git::https://github.com/acme/iac-modules.git"
  defaultRef: "v2.4.0"
modules:
  - name: cloud-run
    displayName: "GCP Cloud Run Service"
    description: "Stateless container service on Cloud Run."
    category: compute
    source: "git::https://github.com/acme/iac-modules.git//modules/cloud-run"
    version: "v2.4.0"
    stability: stable
    inputs:
      - name: name
        type: string
        required: true
        tfvarsKey: service_name
        moduleArg: name
`

// fileSourceManifestV2 is the same manifest after the platform engineer adds a
// second module — the edit the catalog must pick up without a redeploy.
const fileSourceManifestV2 = fileSourceManifestV1 + `
  - name: bucket
    displayName: "Cloud Storage Bucket"
    description: "Provision a Cloud Storage bucket."
    category: storage
    source: "git::https://github.com/acme/iac-modules.git//modules/bucket"
    version: "v2.4.0"
    stability: stable
    inputs:
      - name: bucket_name
        type: string
        required: true
        tfvarsKey: bucket_name
        moduleArg: name
`

// writeSpecFile writes content as spec.yaml into a fresh temp directory and
// returns the file's path.
func writeSpecFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing spec file: %v", err)
	}
	return path
}

func TestFileSource_List_ReturnsManifestModules(t *testing.T) {
	path := writeSpecFile(t, fileSourceManifestV1)
	src := NewFileSource(path)

	specs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List returned unexpected error: %v", err)
	}
	if got, want := len(specs), 1; got != want {
		t.Fatalf("len(specs) = %d, want %d", got, want)
	}
	if got, want := specs[0].Name, "cloud-run"; got != want {
		t.Errorf("specs[0].Name = %q, want %q", got, want)
	}
	if got, want := specs[0].Source, "git::https://github.com/acme/iac-modules.git//modules/cloud-run"; got != want {
		t.Errorf("specs[0].Source = %q, want %q", got, want)
	}
}

func TestFileSource_Resolve_ReturnsNamedModule(t *testing.T) {
	path := writeSpecFile(t, fileSourceManifestV1)
	src := NewFileSource(path)

	spec, err := src.Resolve(context.Background(), "cloud-run")
	if err != nil {
		t.Fatalf("Resolve returned unexpected error: %v", err)
	}
	if got, want := spec.DisplayName, "GCP Cloud Run Service"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
	if got, want := len(spec.Inputs), 1; got != want {
		t.Fatalf("len(Inputs) = %d, want %d", got, want)
	}
	if got, want := spec.Inputs[0].TfvarsKey, "service_name"; got != want {
		t.Errorf("Inputs[0].TfvarsKey = %q, want %q", got, want)
	}
}

func TestFileSource_Resolve_UnknownModule(t *testing.T) {
	path := writeSpecFile(t, fileSourceManifestV1)
	src := NewFileSource(path)

	_, err := src.Resolve(context.Background(), "does-not-exist")
	if !errors.Is(err, ErrModuleNotFound) {
		t.Errorf("Resolve error = %v, want it to wrap ErrModuleNotFound", err)
	}
}

// The Definition-of-Done bullet behind FileSource: a platform engineer edits
// the spec file and the catalog reflects it on the next call, without a
// redeploy. The same FileSource instance must observe the rewritten file.
func TestFileSource_RereadsFileOnEveryCall(t *testing.T) {
	path := writeSpecFile(t, fileSourceManifestV1)
	src := NewFileSource(path)

	specs, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List (before edit) returned unexpected error: %v", err)
	}
	if got, want := len(specs), 1; got != want {
		t.Fatalf("len(specs) before edit = %d, want %d", got, want)
	}

	// The platform engineer publishes a second module.
	if err := os.WriteFile(path, []byte(fileSourceManifestV2), 0o644); err != nil {
		t.Fatalf("rewriting spec file: %v", err)
	}

	specs, err = src.List(context.Background())
	if err != nil {
		t.Fatalf("List (after edit) returned unexpected error: %v", err)
	}
	if got, want := len(specs), 2; got != want {
		t.Fatalf("len(specs) after edit = %d, want %d", got, want)
	}

	spec, err := src.Resolve(context.Background(), "bucket")
	if err != nil {
		t.Fatalf("Resolve(bucket) after edit returned unexpected error: %v", err)
	}
	if got, want := spec.DisplayName, "Cloud Storage Bucket"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
}
