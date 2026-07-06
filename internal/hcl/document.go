// Package hcl provides format-preserving load, inspection, and mutation of
// Terraform HCL files via hashicorp/hcl/v2/hclwrite. Loading then serializing
// an unmodified document yields byte-for-byte identical output.
package hcl

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// Document wraps an hclwrite.File, the lossless token-tree representation of an
// HCL source file. All mutations operate on this tree so that comments,
// ordering, and unrelated blocks are preserved.
type Document struct {
	file *hclwrite.File
}

// LoadDocument parses HCL source into a Document. The returned Document, when
// serialized via Bytes without modification, reproduces src exactly. The
// filename is used only for diagnostics; parsing begins at the initial position.
func LoadDocument(src []byte) (*Document, error) {
	file, diags := hclwrite.ParseConfig(src, "main.tf", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("hcl: parsing document: %w", diags)
	}
	return &Document{file: file}, nil
}

// Bytes serializes the document back to HCL source, preserving the original
// formatting of any unmodified content.
func (d *Document) Bytes() []byte {
	return d.file.Bytes()
}
