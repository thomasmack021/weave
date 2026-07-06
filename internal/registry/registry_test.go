package registry

import (
	"errors"
	"testing"
)

// validManifest is a representative manifest as published by the central IaC
// library. It exercises: metadata, module ordering, required vs. optional
// inputs, defaults, validation rules, and the moduleArg/tfvarsKey decoupling.
const validManifest = `
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
        description: "Service name (must be DNS-1123)."
        validation:
          pattern: "^[a-z]([-a-z0-9]*[a-z0-9])?$"
          maxLength: 63
        tfvarsKey: service_name
        moduleArg: name
      - name: image
        type: string
        required: true
        description: "Container image URI."
        tfvarsKey: container_image
        moduleArg: image
      - name: min_instances
        type: number
        required: false
        default: 0
        tfvarsKey: min_instances
        moduleArg: min_instances
    outputs:
      - name: service_url
        description: "HTTPS endpoint of the deployed service."
  - name: bigquery-dataset
    displayName: "BigQuery Dataset"
    description: "A BigQuery dataset within the landing zone."
    category: data
    source: "git::https://github.com/acme/iac-modules.git//modules/bigquery-dataset"
    version: "v2.4.0"
    stability: beta
    inputs:
      - name: dataset_id
        type: string
        required: true
        tfvarsKey: dataset_id
        moduleArg: dataset_id
    outputs:
      - name: dataset_self_link
        description: "Self link of the created dataset."
`

func TestParseManifest_Metadata(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	if got, want := m.APIVersion, "weave.dev/v1"; got != want {
		t.Errorf("APIVersion = %q, want %q", got, want)
	}
	if got, want := m.Kind, "ModuleManifest"; got != want {
		t.Errorf("Kind = %q, want %q", got, want)
	}
	if got, want := m.Metadata.Registry, "git::https://github.com/acme/iac-modules.git"; got != want {
		t.Errorf("Metadata.Registry = %q, want %q", got, want)
	}
	if got, want := m.Metadata.DefaultRef, "v2.4.0"; got != want {
		t.Errorf("Metadata.DefaultRef = %q, want %q", got, want)
	}
}

func TestParseManifest_ModuleOrderPreserved(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	if got, want := len(m.Modules), 2; got != want {
		t.Fatalf("len(Modules) = %d, want %d", got, want)
	}

	wantOrder := []string{"cloud-run", "bigquery-dataset"}
	for i, want := range wantOrder {
		if got := m.Modules[i].Name; got != want {
			t.Errorf("Modules[%d].Name = %q, want %q", i, got, want)
		}
	}
}

func TestParseManifest_ModuleFields(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	mod := m.Modules[0]
	if got, want := mod.DisplayName, "GCP Cloud Run Service"; got != want {
		t.Errorf("DisplayName = %q, want %q", got, want)
	}
	if got, want := mod.Description, "Stateless container service on Cloud Run."; got != want {
		t.Errorf("Description = %q, want %q", got, want)
	}
	if got, want := mod.Category, "compute"; got != want {
		t.Errorf("Category = %q, want %q", got, want)
	}
	if got, want := mod.Source, "git::https://github.com/acme/iac-modules.git//modules/cloud-run"; got != want {
		t.Errorf("Source = %q, want %q", got, want)
	}
	if got, want := mod.Version, "v2.4.0"; got != want {
		t.Errorf("Version = %q, want %q", got, want)
	}
	if got, want := mod.Stability, "stable"; got != want {
		t.Errorf("Stability = %q, want %q", got, want)
	}
}

func TestParseManifest_InputOrderAndFields(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	inputs := m.Modules[0].Inputs
	if got, want := len(inputs), 3; got != want {
		t.Fatalf("len(Inputs) = %d, want %d", got, want)
	}

	wantOrder := []string{"name", "image", "min_instances"}
	for i, want := range wantOrder {
		if got := inputs[i].Name; got != want {
			t.Errorf("Inputs[%d].Name = %q, want %q", i, got, want)
		}
	}

	// Required input with full metadata and the moduleArg/tfvarsKey decoupling.
	name := inputs[0]
	if !name.Required {
		t.Errorf("Inputs[name].Required = false, want true")
	}
	if got, want := name.Type, "string"; got != want {
		t.Errorf("Inputs[name].Type = %q, want %q", got, want)
	}
	if got, want := name.TfvarsKey, "service_name"; got != want {
		t.Errorf("Inputs[name].TfvarsKey = %q, want %q", got, want)
	}
	if got, want := name.ModuleArg, "name"; got != want {
		t.Errorf("Inputs[name].ModuleArg = %q, want %q", got, want)
	}
}

func TestParseManifest_InputValidationParsed(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	name := m.Modules[0].Inputs[0]
	if name.Validation == nil {
		t.Fatalf("Inputs[name].Validation = nil, want non-nil")
	}
	if got, want := name.Validation.Pattern, "^[a-z]([-a-z0-9]*[a-z0-9])?$"; got != want {
		t.Errorf("Validation.Pattern = %q, want %q", got, want)
	}
	if got, want := name.Validation.MaxLength, 63; got != want {
		t.Errorf("Validation.MaxLength = %d, want %d", got, want)
	}
}

func TestParseManifest_OptionalInputDefaults(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	minInstances := m.Modules[0].Inputs[2]
	if minInstances.Required {
		t.Errorf("Inputs[min_instances].Required = true, want false")
	}
	if minInstances.Default == nil {
		t.Errorf("Inputs[min_instances].Default = nil, want a non-nil default (0)")
	}

	// A required input must carry no default.
	if name := m.Modules[0].Inputs[0]; name.Default != nil {
		t.Errorf("Inputs[name].Default = %v, want nil for a required input", name.Default)
	}

	// An input without an explicit validation block must parse as nil, not a
	// zero-valued struct, so callers can distinguish "no rules" from "empty rules".
	if image := m.Modules[0].Inputs[1]; image.Validation != nil {
		t.Errorf("Inputs[image].Validation = %+v, want nil", image.Validation)
	}
}

func TestParseManifest_Outputs(t *testing.T) {
	m, err := ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	outputs := m.Modules[0].Outputs
	if got, want := len(outputs), 1; got != want {
		t.Fatalf("len(Outputs) = %d, want %d", got, want)
	}
	if got, want := outputs[0].Name, "service_url"; got != want {
		t.Errorf("Outputs[0].Name = %q, want %q", got, want)
	}
	if got, want := outputs[0].Description, "HTTPS endpoint of the deployed service."; got != want {
		t.Errorf("Outputs[0].Description = %q, want %q", got, want)
	}
}

func TestParseManifest_UnsupportedAPIVersion(t *testing.T) {
	const futureManifest = `
apiVersion: weave.dev/v2
kind: ModuleManifest
metadata:
  registry: "git::https://github.com/acme/iac-modules.git"
  defaultRef: "v3.0.0"
modules: []
`

	_, err := ParseManifest([]byte(futureManifest))
	if err == nil {
		t.Fatalf("ParseManifest accepted an unsupported apiVersion, want error")
	}
	if !errors.Is(err, ErrUnsupportedAPIVersion) {
		t.Errorf("error = %v, want it to wrap ErrUnsupportedAPIVersion", err)
	}
}

func TestParseManifest_MalformedYAML(t *testing.T) {
	const malformed = `
apiVersion: weave.dev/v1
modules:
  - name: cloud-run
    inputs: [this is : not valid yaml
`

	if _, err := ParseManifest([]byte(malformed)); err == nil {
		t.Fatalf("ParseManifest accepted malformed YAML, want error")
	}
}

func TestParseManifest_EmptyInput(t *testing.T) {
	if _, err := ParseManifest(nil); err == nil {
		t.Fatalf("ParseManifest accepted empty input, want error")
	}
}

// choiceManifest exercises the Phase 2 choice-input schema: a virtual
// `type: choice` input whose options carry platform-owned expandsTo maps
// referencing declared inputs of the same module.
const choiceManifest = `
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
      - name: cpu
        type: number
        required: false
        default: 1
        tfvarsKey: cpu
        moduleArg: cpu
      - name: memory
        type: string
        required: false
        default: "512Mi"
        tfvarsKey: memory
        moduleArg: memory
      - name: size
        type: choice
        required: true
        description: "T-shirt size."
        options:
          - value: small
            label: "Small — for prototypes"
            description: "1 CPU, 512Mi memory."
            expandsTo:
              cpu: 1
              memory: "512Mi"
          - value: large
            label: "Large — for production workloads"
            expandsTo:
              cpu: 4
              memory: "4096Mi"
`

func TestParseManifest_ChoiceInputOptionsParsed(t *testing.T) {
	m, err := ParseManifest([]byte(choiceManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	size := m.Modules[0].Inputs[2]
	if got, want := size.Type, "choice"; got != want {
		t.Fatalf("Inputs[size].Type = %q, want %q", got, want)
	}
	if got, want := len(size.Options), 2; got != want {
		t.Fatalf("len(Inputs[size].Options) = %d, want %d", got, want)
	}

	small := size.Options[0]
	if got, want := small.Value, "small"; got != want {
		t.Errorf("Options[0].Value = %q, want %q", got, want)
	}
	if got, want := small.Label, "Small — for prototypes"; got != want {
		t.Errorf("Options[0].Label = %q, want %q", got, want)
	}
	if got, want := small.Description, "1 CPU, 512Mi memory."; got != want {
		t.Errorf("Options[0].Description = %q, want %q", got, want)
	}
	if got, want := len(small.ExpandsTo), 2; got != want {
		t.Fatalf("len(Options[0].ExpandsTo) = %d, want %d", got, want)
	}
	if got, want := small.ExpandsTo["cpu"], 1; got != want {
		t.Errorf("Options[0].ExpandsTo[cpu] = %v (%T), want %v", got, got, want)
	}
	if got, want := small.ExpandsTo["memory"], "512Mi"; got != want {
		t.Errorf("Options[0].ExpandsTo[memory] = %v (%T), want %v", got, got, want)
	}

	large := size.Options[1]
	if got, want := large.Value, "large"; got != want {
		t.Errorf("Options[1].Value = %q, want %q", got, want)
	}
	// description is optional on an option; absent must parse as empty.
	if got, want := large.Description, ""; got != want {
		t.Errorf("Options[1].Description = %q, want %q", got, want)
	}
	if got, want := large.ExpandsTo["cpu"], 4; got != want {
		t.Errorf("Options[1].ExpandsTo[cpu] = %v (%T), want %v", got, got, want)
	}
}

func TestParseManifest_NonChoiceInputHasNoOptions(t *testing.T) {
	m, err := ParseManifest([]byte(choiceManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned unexpected error: %v", err)
	}

	for _, in := range m.Modules[0].Inputs[:2] {
		if in.Options != nil {
			t.Errorf("Inputs[%s].Options = %v, want nil (no options block)", in.Name, in.Options)
		}
	}
}
