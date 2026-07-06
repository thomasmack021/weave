package hcl

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/thomasmack/weave/internal/registry"
	"github.com/zclconf/go-cty/cty"
)

// cloudRunSpec is the dummy spec used by the InsertModule tests: a cloud-run
// module with two required string inputs whose tfvars keys differ from their
// input names (exercising the moduleArg/tfvarsKey decoupling).
func cloudRunSpec() *registry.ModuleSpec {
	return &registry.ModuleSpec{
		Name:    "cloud-run",
		Source:  "git::https://github.com/acme/iac-modules.git//modules/cloud-run",
		Version: "v2.4.0",
		Inputs: []registry.InputSpec{
			{Name: "name", Type: "string", Required: true, TfvarsKey: "service_name", ModuleArg: "name"},
			{Name: "image", Type: "string", Required: true, TfvarsKey: "container_image", ModuleArg: "image"},
		},
	}
}

func TestInsertModule_HappyPath(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("testdata", "insert_base.tf"))
	if err != nil {
		t.Fatalf("reading base fixture: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "insert_expected.tf"))
	if err != nil {
		t.Fatalf("reading expected fixture: %v", err)
	}

	doc, err := LoadDocument(base)
	if err != nil {
		t.Fatalf("LoadDocument returned unexpected error: %v", err)
	}

	inputs := map[string]cty.Value{
		"name":  cty.StringVal("api"),
		"image": cty.StringVal("gcr.io/x/api:1"),
	}

	changed, err := InsertModule(doc, "api", cloudRunSpec(), inputs)
	if err != nil {
		t.Fatalf("InsertModule returned unexpected error: %v", err)
	}
	if !changed {
		t.Errorf("InsertModule changed = false, want true")
	}

	got := doc.Bytes()
	if !bytes.Equal(got, want) {
		t.Errorf("output is not byte-identical to insert_expected.tf\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(want), want, len(got), got)
	}
}

// TestInsertModule_Coexistence proves that appending a module does not scramble
// complex pre-existing content: a locals block with inline comments and a
// nested map, multiple blank lines, a block comment, and an unrelated resource
// must all survive byte-for-byte, with the new module cleanly appended.
func TestInsertModule_Coexistence(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("testdata", "coexistence_base.tf"))
	if err != nil {
		t.Fatalf("reading base fixture: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "coexistence_expected.tf"))
	if err != nil {
		t.Fatalf("reading expected fixture: %v", err)
	}

	doc, err := LoadDocument(base)
	if err != nil {
		t.Fatalf("LoadDocument returned unexpected error: %v", err)
	}

	inputs := map[string]cty.Value{
		"name":  cty.StringVal("api"),
		"image": cty.StringVal("gcr.io/x/api:1"),
	}

	changed, err := InsertModule(doc, "api", cloudRunSpec(), inputs)
	if err != nil {
		t.Fatalf("InsertModule returned unexpected error: %v", err)
	}
	if !changed {
		t.Errorf("InsertModule changed = false, want true")
	}

	got := doc.Bytes()
	if !bytes.Equal(got, want) {
		t.Errorf("output is not byte-identical to coexistence_expected.tf\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(want), want, len(got), got)
	}
}

// TestInsertModule_Idempotent guarantees the GitOps cornerstone: re-running the
// same insert must be a no-op. Loading a document that already contains
// module "api" and inserting "api" again must report changed=false and leave
// the bytes untouched — no duplicate block, no stray blank line.
func TestInsertModule_Idempotent(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "insert_expected.tf"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	doc, err := LoadDocument(src)
	if err != nil {
		t.Fatalf("LoadDocument returned unexpected error: %v", err)
	}

	inputs := map[string]cty.Value{
		"name":  cty.StringVal("api"),
		"image": cty.StringVal("gcr.io/x/api:1"),
	}

	changed, err := InsertModule(doc, "api", cloudRunSpec(), inputs)
	if err != nil {
		t.Fatalf("InsertModule returned unexpected error: %v", err)
	}
	if changed {
		t.Errorf("InsertModule changed = true, want false (module \"api\" already present)")
	}

	got := doc.Bytes()
	if !bytes.Equal(got, src) {
		t.Errorf("document was mutated despite existing module\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(src), src, len(got), got)
	}
}
