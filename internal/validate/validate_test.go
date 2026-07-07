package validate

import (
	"errors"
	"strings"
	"testing"

	"github.com/thomasmack021/weave/internal/registry"
	"github.com/zclconf/go-cty/cty"
)

// testSpec returns a ModuleSpec exercising every validation dimension:
//   - name:          required string with a Pattern + MaxLength rule
//   - image:         required string, no rules
//   - min_instances: optional number with a default (0)
//   - enabled:       optional bool with a default (true)
func testSpec() *registry.ModuleSpec {
	return &registry.ModuleSpec{
		Name: "cloud-run",
		Inputs: []registry.InputSpec{
			{
				Name:     "name",
				Type:     "string",
				Required: true,
				Validation: &registry.InputValidation{
					Pattern:   "^[a-z]([-a-z0-9]*[a-z0-9])?$",
					MaxLength: 63,
				},
				TfvarsKey: "service_name",
				ModuleArg: "name",
			},
			{
				Name:      "image",
				Type:      "string",
				Required:  true,
				TfvarsKey: "container_image",
				ModuleArg: "image",
			},
			{
				Name:      "min_instances",
				Type:      "number",
				Required:  false,
				Default:   0,
				TfvarsKey: "min_instances",
				ModuleArg: "min_instances",
			},
			{
				Name:      "enabled",
				Type:      "bool",
				Required:  false,
				Default:   true,
				TfvarsKey: "enabled",
				ModuleArg: "enabled",
			},
		},
	}
}

// assertValues compares two cty.Value maps by key using RawEquals.
func assertValues(t *testing.T, got, want map[string]cty.Value) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("result has %d values, want %d (got=%v)", len(got), len(want), got)
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("result missing key %q", k)
			continue
		}
		if !g.RawEquals(w) {
			t.Errorf("value[%q] = %#v, want %#v", k, g, w)
		}
	}
}

func TestInputs_Table(t *testing.T) {
	tests := []struct {
		name       string
		raw        map[string]string
		wantErrs   []error              // sentinels expected via errors.Is; empty => no error
		wantValues map[string]cty.Value // checked only when wantErrs is empty
	}{
		{
			name: "all required valid, optionals fall back to defaults",
			raw: map[string]string{
				"name":  "api",
				"image": "gcr.io/x/api:1",
			},
			wantValues: map[string]cty.Value{
				"name":          cty.StringVal("api"),
				"image":         cty.StringVal("gcr.io/x/api:1"),
				"min_instances": cty.NumberIntVal(0),
				"enabled":       cty.BoolVal(true),
			},
		},
		{
			name: "supplied optionals override defaults and coerce by type",
			raw: map[string]string{
				"name":          "api",
				"image":         "gcr.io/x/api:1",
				"min_instances": "3",
				"enabled":       "false",
			},
			wantValues: map[string]cty.Value{
				"name":          cty.StringVal("api"),
				"image":         cty.StringVal("gcr.io/x/api:1"),
				"min_instances": cty.NumberIntVal(3),
				"enabled":       cty.BoolVal(false),
			},
		},
		{
			name: "missing required input",
			raw: map[string]string{
				"name": "api",
			},
			wantErrs: []error{ErrMissingRequired},
		},
		{
			name: "pattern mismatch (uppercase not allowed)",
			raw: map[string]string{
				"name":  "Api",
				"image": "gcr.io/x/api:1",
			},
			wantErrs: []error{ErrPatternMismatch},
		},
		{
			name: "max length exceeded",
			raw: map[string]string{
				"name":  strings.Repeat("a", 64), // 64 > MaxLength 63, still matches pattern
				"image": "gcr.io/x/api:1",
			},
			wantErrs: []error{ErrMaxLengthExceeded},
		},
		{
			name: "invalid number coercion",
			raw: map[string]string{
				"name":          "api",
				"image":         "gcr.io/x/api:1",
				"min_instances": "not-a-number",
			},
			wantErrs: []error{ErrInvalidValue},
		},
		{
			name: "invalid bool coercion",
			raw: map[string]string{
				"name":    "api",
				"image":   "gcr.io/x/api:1",
				"enabled": "maybe",
			},
			wantErrs: []error{ErrInvalidValue},
		},
		{
			name: "undeclared input rejected (typo'd flag)",
			raw: map[string]string{
				"name":  "api",
				"image": "gcr.io/x/api:1",
				"bogus": "x",
			},
			wantErrs: []error{ErrUnknownInput},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Inputs(testSpec(), tc.raw)

			if len(tc.wantErrs) == 0 {
				if err != nil {
					t.Fatalf("Inputs returned unexpected error: %v", err)
				}
				assertValues(t, got, tc.wantValues)
				return
			}

			if err == nil {
				t.Fatalf("Inputs returned nil error, want errors: %v", tc.wantErrs)
			}
			for _, want := range tc.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("error %v does not wrap %v", err, want)
				}
			}
			if got != nil {
				t.Errorf("Inputs returned values %v alongside an error, want nil", got)
			}
		})
	}
}

// choiceSpec returns a ModuleSpec with a required `type: choice` input whose
// options expand to concrete declared inputs (cpu: number, memory: string).
// Mirrors the registry choiceManifest fixture from Phase 2 step 1.
func choiceSpec() *registry.ModuleSpec {
	return &registry.ModuleSpec{
		Name: "cloud-run",
		Inputs: []registry.InputSpec{
			{
				Name:      "name",
				Type:      "string",
				Required:  true,
				TfvarsKey: "service_name",
				ModuleArg: "name",
			},
			{
				Name:      "cpu",
				Type:      "number",
				Required:  false,
				TfvarsKey: "cpu",
				ModuleArg: "cpu",
			},
			{
				Name:      "memory",
				Type:      "string",
				Required:  false,
				TfvarsKey: "memory",
				ModuleArg: "memory",
			},
			{
				Name:     "size",
				Type:     "choice",
				Required: true,
				Options: []registry.OptionSpec{
					{
						Value:       "small",
						Label:       "Small — for prototypes",
						Description: "1 vCPU, 512Mi",
						ExpandsTo:   map[string]any{"cpu": 1, "memory": "512Mi"},
					},
					{
						Value:     "large",
						Label:     "Large — production workloads",
						ExpandsTo: map[string]any{"cpu": 4, "memory": "2Gi"},
					},
				},
			},
		},
	}
}

// brokenChoiceSpec returns choiceSpec with the named option corrupted by a
// spec-authoring bug, for the ErrSpecInvalid cases (500-class, never 422).
func brokenChoiceSpec(expandsTo map[string]any) *registry.ModuleSpec {
	spec := choiceSpec()
	for i := range spec.Inputs {
		if spec.Inputs[i].Name == "size" {
			spec.Inputs[i].Options[0].ExpandsTo = expandsTo
		}
	}
	return spec
}

// TestInputs_ChoiceExpansion_Table drives Phase 2 step 2: a `type: choice`
// input never emits a value of its own — the selected option's ExpandsTo
// supplies concrete values for other declared inputs, coerced against each
// target's declared type. Caller-fault failures (unknown choice, conflict,
// missing required) use 422-class sentinels; spec-authoring bugs use
// ErrSpecInvalid (500-class).
func TestInputs_ChoiceExpansion_Table(t *testing.T) {
	tests := []struct {
		name       string
		spec       *registry.ModuleSpec
		raw        map[string]string
		wantErrs   []error              // sentinels expected via errors.Is; empty => no error
		wantValues map[string]cty.Value // checked only when wantErrs is empty
	}{
		{
			name: "choice expands into concrete declared inputs; choice emits no value",
			spec: choiceSpec(),
			raw: map[string]string{
				"name": "api",
				"size": "small",
			},
			// "size" itself must be absent: only its expansion lands.
			wantValues: map[string]cty.Value{
				"name":   cty.StringVal("api"),
				"cpu":    cty.NumberIntVal(1),
				"memory": cty.StringVal("512Mi"),
			},
		},
		{
			name: "second option expands with its own values",
			spec: choiceSpec(),
			raw: map[string]string{
				"name": "api",
				"size": "large",
			},
			wantValues: map[string]cty.Value{
				"name":   cty.StringVal("api"),
				"cpu":    cty.NumberIntVal(4),
				"memory": cty.StringVal("2Gi"),
			},
		},
		{
			name: "selected value matches no option (caller fault)",
			spec: choiceSpec(),
			raw: map[string]string{
				"name": "api",
				"size": "medium",
			},
			wantErrs: []error{ErrUnknownChoice},
		},
		{
			name: "direct caller value conflicts with the option's expansion (session-9 ruling: reject)",
			spec: choiceSpec(),
			raw: map[string]string{
				"name": "api",
				"size": "small",
				"cpu":  "2", // small also expands cpu -> conflict
			},
			wantErrs: []error{ErrChoiceConflict},
		},
		{
			name: "missing required choice behaves like any missing required input",
			spec: choiceSpec(),
			raw: map[string]string{
				"name": "api",
			},
			wantErrs: []error{ErrMissingRequired},
		},
		{
			name: "spec bug: expandsTo references an undeclared input (never 422)",
			spec: brokenChoiceSpec(map[string]any{"disk": "100Gi"}),
			raw: map[string]string{
				"name": "api",
				"size": "small",
			},
			wantErrs: []error{ErrSpecInvalid},
		},
		{
			name: "spec bug: expandsTo value uncoercible against target's declared type (never 422)",
			spec: brokenChoiceSpec(map[string]any{"cpu": "lots"}), // cpu is number
			raw: map[string]string{
				"name": "api",
				"size": "small",
			},
			wantErrs: []error{ErrSpecInvalid},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Inputs(tc.spec, tc.raw)

			if len(tc.wantErrs) == 0 {
				if err != nil {
					t.Fatalf("Inputs returned unexpected error: %v", err)
				}
				assertValues(t, got, tc.wantValues)
				if _, ok := got["size"]; ok {
					t.Errorf("choice input %q emitted its own value %v, want none", "size", got["size"])
				}
				return
			}

			if err == nil {
				t.Fatalf("Inputs returned nil error, want errors: %v", tc.wantErrs)
			}
			for _, want := range tc.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("error %v does not wrap %v", err, want)
				}
			}
			if got != nil {
				t.Errorf("Inputs returned values %v alongside an error, want nil", got)
			}
		})
	}
}

// TestInputs_ChoiceSpecBugNeverMapsToCallerFault pins the session-9 ruling
// that spec-authoring bugs are the platform team's fault: an ErrSpecInvalid
// failure must NOT also wrap any caller-fault sentinel the server maps to 422.
func TestInputs_ChoiceSpecBugNeverMapsToCallerFault(t *testing.T) {
	_, err := Inputs(brokenChoiceSpec(map[string]any{"disk": "100Gi"}), map[string]string{
		"name": "api",
		"size": "small",
	})
	if err == nil {
		t.Fatalf("Inputs returned nil error, want ErrSpecInvalid")
	}
	if !errors.Is(err, ErrSpecInvalid) {
		t.Fatalf("error %v does not wrap ErrSpecInvalid", err)
	}
	for _, callerFault := range []error{ErrUnknownChoice, ErrChoiceConflict, ErrUnknownInput, ErrInvalidValue} {
		if errors.Is(err, callerFault) {
			t.Errorf("spec-bug error %v also wraps caller-fault sentinel %v; server would misreport 500 as 422", err, callerFault)
		}
	}
}

// TestInputs_AccumulatesMultipleFailures is the crucial guarantee: validation
// must not stop at the first failure. Here three independent failures occur in
// one call — a missing required input, a pattern mismatch, and a bad number —
// and all three must be present in the single returned error. A fail-on-first
// implementation would surface only one of them.
func TestInputs_AccumulatesMultipleFailures(t *testing.T) {
	raw := map[string]string{
		"name":          "Api",          // pattern mismatch (uppercase)
		"min_instances": "not-a-number", // invalid number coercion
		// "image" omitted               // missing required
	}

	_, err := Inputs(testSpec(), raw)
	if err == nil {
		t.Fatalf("Inputs returned nil error, want an aggregated error")
	}

	for _, want := range []error{ErrMissingRequired, ErrPatternMismatch, ErrInvalidValue} {
		if !errors.Is(err, want) {
			t.Errorf("aggregated error %v does not wrap %v", err, want)
		}
	}
}
