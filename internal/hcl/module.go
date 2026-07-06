package hcl

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/thomasmack/weave/internal/registry"
	"github.com/zclconf/go-cty/cty"
)

// InsertModule appends a module block labelled instanceName to doc. Its source
// attribute is the pinned module source (spec.Source + "?ref=" + spec.Version),
// and each declared input present in inputs is wired to its tfvars variable as
// an unquoted traversal (var.<TfvarsKey>), emitted in spec.Inputs declaration
// order. It returns true when the document was modified.
//
// The block label is the caller-supplied instanceName — decoupled from
// spec.Name — so multiple instances of the same module type can coexist in one
// file (e.g. two cloud-run services).
//
// The inputs map (keyed by input name) determines which arguments are emitted;
// the rendered block references variables, not these literal values.
func InsertModule(doc *Document, instanceName string, spec *registry.ModuleSpec, inputs map[string]cty.Value) (bool, error) {
	body := doc.file.Body()

	// Idempotency guard: if a module block with this label already exists,
	// make no change. Checked before any mutation so re-runs are true no-ops.
	for _, blk := range body.Blocks() {
		if blk.Type() != "module" {
			continue
		}
		labels := blk.Labels()
		if len(labels) > 0 && labels[0] == instanceName {
			return false, nil
		}
	}

	// Blank line for visual separation from preceding content.
	body.AppendNewline()

	block := body.AppendNewBlock("module", []string{instanceName})
	mb := block.Body()

	mb.SetAttributeValue("source", cty.StringVal(spec.Source+"?ref="+spec.Version))
	mb.AppendNewline()

	for _, in := range spec.Inputs {
		if _, ok := inputs[in.Name]; !ok {
			continue // only emit arguments the caller supplied
		}
		mb.SetAttributeTraversal(in.ModuleArg, hcl.Traversal{
			hcl.TraverseRoot{Name: "var"},
			hcl.TraverseAttr{Name: in.TfvarsKey},
		})
	}

	return true, nil
}
