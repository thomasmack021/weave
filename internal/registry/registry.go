// Package registry models the module manifest published by the central IaC
// library and parses it into typed Go structures the CLI can reason about.
package registry

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// SupportedAPIVersion is the manifest contract version this build understands.
const SupportedAPIVersion = "weave.dev/v1"

// ErrUnsupportedAPIVersion is returned when a manifest declares an apiVersion
// this build of the CLI does not know how to interpret. Callers should match it
// with errors.Is.
var ErrUnsupportedAPIVersion = errors.New("unsupported manifest apiVersion")

// Manifest is the top-level document published by the IaC library.
type Manifest struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   ManifestMetadata `yaml:"metadata"`
	Modules    []ModuleSpec     `yaml:"modules"`
}

// ManifestMetadata carries registry-wide settings.
type ManifestMetadata struct {
	Registry   string `yaml:"registry"`
	DefaultRef string `yaml:"defaultRef"`
}

// ModuleSpec describes a single module the CLI can offer to developers.
type ModuleSpec struct {
	Name        string       `yaml:"name"`
	DisplayName string       `yaml:"displayName"`
	Description string       `yaml:"description"`
	Category    string       `yaml:"category"`
	Source      string       `yaml:"source"`
	Version     string       `yaml:"version"`
	Stability   string       `yaml:"stability"`
	Inputs      []InputSpec  `yaml:"inputs"`
	Outputs     []OutputSpec `yaml:"outputs"`
}

// InputSpec describes one configurable input of a module. Default is typed as
// any so type-checking is deferred to the domain validation layer. Validation
// is a pointer so an absent validation block ("no rules") is distinguishable
// from an empty one.
type InputSpec struct {
	Name        string           `yaml:"name"`
	Type        string           `yaml:"type"`
	Required    bool             `yaml:"required"`
	Default     any              `yaml:"default"`
	Description string           `yaml:"description"`
	Validation  *InputValidation `yaml:"validation"`
	TfvarsKey   string           `yaml:"tfvarsKey"`
	ModuleArg   string           `yaml:"moduleArg"`
	Options     []OptionSpec     `yaml:"options"`
}

// OptionSpec is one platform-defined choice of a `type: choice` input. A
// choice input never emits its own value; the selected option's ExpandsTo
// map supplies concrete values for other declared inputs of the same
// module. ExpandsTo values are typed as any; coercion against the target
// input's declared type is the validation layer's job, not the parser's.
type OptionSpec struct {
	Value       string         `yaml:"value"`
	Label       string         `yaml:"label"`
	Description string         `yaml:"description"`
	ExpandsTo   map[string]any `yaml:"expandsTo"`
}

// InputValidation holds the validation rules for a single input.
type InputValidation struct {
	Pattern   string `yaml:"pattern"`
	MaxLength int    `yaml:"maxLength"`
}

// OutputSpec describes an informational output surfaced to the developer.
type OutputSpec struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ParseManifest unmarshals a YAML manifest into a Manifest and verifies the
// declared apiVersion is supported. It performs no schema validation beyond the
// version check; that is the responsibility of the domain layer.
func ParseManifest(data []byte) (*Manifest, error) {
	if len(data) == 0 {
		return nil, errors.New("registry: empty manifest")
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("registry: parsing manifest: %w", err)
	}

	if m.APIVersion != SupportedAPIVersion {
		return nil, fmt.Errorf("registry: %q: %w", m.APIVersion, ErrUnsupportedAPIVersion)
	}

	return &m, nil
}
