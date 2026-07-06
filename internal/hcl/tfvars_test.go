package hcl

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestUpsertVariable_InsertNew(t *testing.T) {
	base := mustReadFixture(t, "tfvars_base.tfvars")
	want := mustReadFixture(t, "tfvars_expected_insert.tfvars")

	doc, err := LoadDocument(base)
	if err != nil {
		t.Fatalf("LoadDocument returned unexpected error: %v", err)
	}

	changed := UpsertVariable(doc, "service_name", cty.StringVal("api"))
	if !changed {
		t.Errorf("UpsertVariable changed = false, want true (new variable)")
	}

	got := doc.Bytes()
	if !bytes.Equal(got, want) {
		t.Errorf("output is not byte-identical to tfvars_expected_insert.tfvars\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(want), want, len(got), got)
	}
}

func TestUpsertVariable_SkipIdentical(t *testing.T) {
	base := mustReadFixture(t, "tfvars_base.tfvars")

	doc, err := LoadDocument(base)
	if err != nil {
		t.Fatalf("LoadDocument returned unexpected error: %v", err)
	}

	changed := UpsertVariable(doc, "region", cty.StringVal("us-central1"))
	if changed {
		t.Errorf("UpsertVariable changed = true, want false (identical value)")
	}

	got := doc.Bytes()
	if !bytes.Equal(got, base) {
		t.Errorf("document was mutated despite identical value\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(base), base, len(got), got)
	}
}

func TestUpsertVariable_OverwriteChanged(t *testing.T) {
	base := mustReadFixture(t, "tfvars_base.tfvars")
	want := mustReadFixture(t, "tfvars_expected_overwrite.tfvars")

	doc, err := LoadDocument(base)
	if err != nil {
		t.Fatalf("LoadDocument returned unexpected error: %v", err)
	}

	changed := UpsertVariable(doc, "region", cty.StringVal("europe-west3"))
	if !changed {
		t.Errorf("UpsertVariable changed = false, want true (value changed)")
	}

	got := doc.Bytes()
	if !bytes.Equal(got, want) {
		t.Errorf("output is not byte-identical to tfvars_expected_overwrite.tfvars\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(want), want, len(got), got)
	}
}
