package hcl

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// UpsertVariable sets key = val in doc's top-level body. If key is absent it is
// appended (preceded by a blank line for separation); if it is present with a
// different value it is overwritten; if it is present with an identical value
// the document is left untouched. It returns true when the document was
// modified.
func UpsertVariable(doc *Document, key string, val cty.Value) bool {
	body := doc.file.Body()

	attr := body.GetAttribute(key)
	if attr == nil {
		// Separate the new variable so hclwrite does not re-align an adjacent
		// existing attribute into the same alignment group.
		body.AppendNewline()
		body.SetAttributeValue(key, val)
		return true
	}

	// Present: skip the write only if the current value is provably identical.
	if existing, ok := evalAttrValue(attr); ok && existing.RawEquals(val) {
		return false
	}

	body.SetAttributeValue(key, val)
	return true
}

// evalAttrValue evaluates an existing attribute's expression to a cty.Value.
// .tfvars values are literal primitives for our use case, so a nil EvalContext
// is sufficient. ok is false when the expression cannot be parsed or evaluated
// (e.g. it references a function or variable), in which case callers should
// treat the value as "unknown" and overwrite rather than risk a false skip.
func evalAttrValue(attr *hclwrite.Attribute) (cty.Value, bool) {
	src := attr.Expr().BuildTokens(nil).Bytes()

	expr, diags := hclsyntax.ParseExpression(src, "tfvars", hcl.InitialPos)
	if diags.HasErrors() {
		return cty.NilVal, false
	}
	v, diags := expr.Value(nil)
	if diags.HasErrors() {
		return cty.NilVal, false
	}
	return v, true
}
