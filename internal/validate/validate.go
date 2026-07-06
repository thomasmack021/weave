// Package validate evaluates raw, user-supplied module inputs against a
// registry.ModuleSpec, coercing valid inputs into cty.Values for downstream
// HCL generation. All validation failures are accumulated and returned
// together rather than failing on the first error.
package validate

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"

	"github.com/thomasmack/weave/internal/registry"
	"github.com/zclconf/go-cty/cty"
)

// Sentinel errors describing each class of validation failure. Callers match
// them with errors.Is; multiple causes may be present in a single returned
// error when failures are accumulated.
var (
	// ErrMissingRequired indicates a required input was not supplied.
	ErrMissingRequired = errors.New("validate: missing required input")
	// ErrPatternMismatch indicates an input failed its regex Pattern rule.
	ErrPatternMismatch = errors.New("validate: input does not match required pattern")
	// ErrMaxLengthExceeded indicates an input exceeded its MaxLength rule.
	ErrMaxLengthExceeded = errors.New("validate: input exceeds maximum length")
	// ErrInvalidValue indicates an input could not be coerced to its declared type.
	ErrInvalidValue = errors.New("validate: input could not be coerced to its declared type")
	// ErrUnknownInput indicates a supplied input is not declared by the spec.
	ErrUnknownInput = errors.New("validate: unknown input not declared by module")
	// ErrUnknownChoice indicates a choice input's supplied value matches none of
	// the spec's declared options. Caller fault (422-class).
	ErrUnknownChoice = errors.New("validate: value matches no declared option")
	// ErrChoiceConflict indicates the caller supplied a direct value for an
	// input that the selected option's expansion also sets. Caller fault
	// (422-class).
	ErrChoiceConflict = errors.New("validate: input conflicts with a value set by the selected option")
	// ErrSpecInvalid indicates a spec-authoring bug (e.g. an option expanding to
	// an undeclared input, or an expansion value that cannot be coerced to the
	// target's declared type). Platform fault (500-class) — never mixed with the
	// caller-fault sentinels above.
	ErrSpecInvalid = errors.New("validate: module spec is invalid")
)

// Inputs validates rawInputs (keyed by input name) against spec and returns the
// coerced values keyed by input name. Missing optional inputs fall back to the
// spec's declared default. Every validation failure is accumulated into a
// single returned error (via errors.Join); callers may match individual causes
// with errors.Is. On any failure the returned value map is nil.
func Inputs(spec *registry.ModuleSpec, rawInputs map[string]string) (map[string]cty.Value, error) {
	var errs []error
	values := make(map[string]cty.Value, len(spec.Inputs))

	// Strict schema adherence: reject any supplied input the spec doesn't
	// declare (e.g. a typo'd flag). Iterate in sorted order for determinism.
	declared := make(map[string]registry.InputSpec, len(spec.Inputs))
	for _, in := range spec.Inputs {
		declared[in.Name] = in
	}
	unknown := make([]string, 0)
	for name := range rawInputs {
		if _, ok := declared[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		errs = append(errs, fmt.Errorf("%q: %w", name, ErrUnknownInput))
	}

	// Values produced by choice expansion, merged after the main loop so they
	// override target defaults regardless of declaration order. Direct caller
	// values for expanded inputs are rejected as conflicts, never overridden.
	expanded := make(map[string]cty.Value)

	for _, in := range spec.Inputs {
		raw, provided := rawInputs[in.Name]

		// A choice input is virtual: it never emits a value of its own — the
		// selected option's ExpandsTo supplies values for other declared inputs.
		if in.Type == "choice" {
			if !provided {
				if in.Required {
					errs = append(errs, fmt.Errorf("%q: %w", in.Name, ErrMissingRequired))
				}
				continue
			}
			errs = append(errs, expandChoice(in, raw, rawInputs, declared, expanded)...)
			continue
		}

		if !provided {
			switch {
			case in.Default != nil:
				v, err := coerceAny(in.Default, in.Type)
				if err != nil {
					errs = append(errs, fmt.Errorf("%q default: %w", in.Name, err))
					continue
				}
				values[in.Name] = v
			case in.Required:
				errs = append(errs, fmt.Errorf("%q: %w", in.Name, ErrMissingRequired))
			}
			continue
		}

		// Rules apply to the raw string form of provided inputs.
		if in.Validation != nil {
			if pat := in.Validation.Pattern; pat != "" {
				re, err := regexp.Compile(pat)
				if err != nil {
					errs = append(errs, fmt.Errorf("%q pattern %q: %w", in.Name, pat, ErrInvalidValue))
				} else if !re.MatchString(raw) {
					errs = append(errs, fmt.Errorf("%q: %w", in.Name, ErrPatternMismatch))
				}
			}
			if max := in.Validation.MaxLength; max > 0 && len(raw) > max {
				errs = append(errs, fmt.Errorf("%q (%d > %d): %w", in.Name, len(raw), max, ErrMaxLengthExceeded))
			}
		}

		v, err := coerceRaw(raw, in.Type)
		if err != nil {
			errs = append(errs, fmt.Errorf("%q: %w", in.Name, err))
			continue
		}
		values[in.Name] = v
	}

	// Expansion overrides defaults; caller-supplied duplicates were already
	// rejected as conflicts above.
	for name, v := range expanded {
		values[name] = v
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return values, nil
}

// expandChoice resolves the raw value of choice input in against its declared
// options and writes the selected option's ExpandsTo values (coerced against
// each target input's declared type) into expanded. It returns the accumulated
// failures: an unmatched value is the caller's fault (ErrUnknownChoice), as is
// directly supplying an input the option also sets (ErrChoiceConflict); an
// expansion targeting an undeclared or choice-typed input, or a value that
// cannot be coerced, is a spec-authoring bug (ErrSpecInvalid) and is never
// blamed on the caller.
func expandChoice(in registry.InputSpec, raw string, rawInputs map[string]string, declared map[string]registry.InputSpec, expanded map[string]cty.Value) []error {
	var opt *registry.OptionSpec
	for i := range in.Options {
		if in.Options[i].Value == raw {
			opt = &in.Options[i]
			break
		}
	}
	if opt == nil {
		return []error{fmt.Errorf("%q: %q: %w", in.Name, raw, ErrUnknownChoice)}
	}

	// Deterministic error order across the expansion map.
	keys := make([]string, 0, len(opt.ExpandsTo))
	for key := range opt.ExpandsTo {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var errs []error
	for _, key := range keys {
		target, ok := declared[key]
		if !ok || target.Type == "choice" {
			errs = append(errs, fmt.Errorf("%q option %q expands to undeclared or choice input %q: %w", in.Name, opt.Value, key, ErrSpecInvalid))
			continue
		}
		if _, direct := rawInputs[key]; direct {
			errs = append(errs, fmt.Errorf("%q: also set by choice %q option %q: %w", key, in.Name, opt.Value, ErrChoiceConflict))
			continue
		}
		v, err := coerceAny(opt.ExpandsTo[key], target.Type)
		if err != nil {
			// Deliberately %v, not %w: a spec bug must never wrap a
			// caller-fault sentinel like ErrInvalidValue.
			errs = append(errs, fmt.Errorf("%q option %q value for %q (%v): %w", in.Name, opt.Value, key, err, ErrSpecInvalid))
			continue
		}
		expanded[key] = v
	}
	return errs
}

// coerceRaw converts a raw string input into a cty.Value per the declared type.
func coerceRaw(raw, typ string) (cty.Value, error) {
	switch typ {
	case "string", "":
		return cty.StringVal(raw), nil
	case "number":
		v, err := cty.ParseNumberVal(raw)
		if err != nil {
			return cty.NilVal, ErrInvalidValue
		}
		return v, nil
	case "bool":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return cty.NilVal, ErrInvalidValue
		}
		return cty.BoolVal(b), nil
	default:
		return cty.NilVal, ErrInvalidValue
	}
}

// coerceAny converts a spec default (decoded from YAML as any) into a cty.Value
// per the declared type.
func coerceAny(def any, typ string) (cty.Value, error) {
	switch typ {
	case "string", "":
		s, ok := def.(string)
		if !ok {
			return cty.NilVal, ErrInvalidValue
		}
		return cty.StringVal(s), nil
	case "number":
		switch n := def.(type) {
		case int:
			return cty.NumberIntVal(int64(n)), nil
		case int64:
			return cty.NumberIntVal(n), nil
		case float64:
			return cty.NumberFloatVal(n), nil
		case string:
			v, err := cty.ParseNumberVal(n)
			if err != nil {
				return cty.NilVal, ErrInvalidValue
			}
			return v, nil
		default:
			return cty.NilVal, ErrInvalidValue
		}
	case "bool":
		b, ok := def.(bool)
		if !ok {
			return cty.NilVal, ErrInvalidValue
		}
		return cty.BoolVal(b), nil
	default:
		return cty.NilVal, ErrInvalidValue
	}
}
