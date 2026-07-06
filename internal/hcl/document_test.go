package hcl

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestDocument_FormatPreservation is the foundational guarantee of the hcl
// package: loading a document and immediately serializing it must reproduce the
// source byte-for-byte. If this fails, every Day 2 mutation risks corrupting a
// developer's hand-formatting, comments, or unrelated configuration.
func TestDocument_FormatPreservation(t *testing.T) {
	path := filepath.Join("testdata", "format_preserve.tf")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", path, err)
	}

	doc, err := LoadDocument(src)
	if err != nil {
		t.Fatalf("LoadDocument returned unexpected error: %v", err)
	}

	got := doc.Bytes()
	if !bytes.Equal(got, src) {
		t.Errorf("round-trip is not byte-identical\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			len(src), src, len(got), got)
	}
}
