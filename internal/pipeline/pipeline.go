// Package pipeline renders and idempotently merges the CI pipeline step that
// drives terraform execution for a given environment. It operates purely on
// byte slices so it can be unit-tested without touching the filesystem. Existing
// content is parsed as a yaml.Node so developers' comments and formatting are
// preserved when a step is appended.
package pipeline

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// step is the terraform execution step injected into the pipeline.
type step struct {
	Name        string `yaml:"name"`
	Run         string `yaml:"run,omitempty"`
	ProjectID   string `yaml:"project_id,omitempty"`
	StatePrefix string `yaml:"state_prefix,omitempty"`
	Env         string `yaml:"env,omitempty"`
}

// pipelineDoc is used only to render a fresh pipeline from scratch.
type pipelineDoc struct {
	Steps []step `yaml:"steps"`
}

// EnsureStep ensures the pipeline described by existingYAML contains a terraform
// execution step for env, injecting projectID and statePrefix. When existingYAML
// is empty a fresh pipeline is generated. Re-running with an already-present step
// is a no-op (changed == false) that returns the input unchanged.
func EnsureStep(existingYAML []byte, projectID string, statePrefix string, env string) (output []byte, changed bool, err error) {
	stepName := "terraform-apply-" + env
	newStep := step{
		Name:        stepName,
		Run:         "terraform apply -auto-approve",
		ProjectID:   projectID,
		StatePrefix: statePrefix,
		Env:         env,
	}

	// No existing pipeline: render a fresh one.
	if len(bytes.TrimSpace(existingYAML)) == 0 {
		out, err := marshalFresh(newStep)
		if err != nil {
			return nil, false, err
		}
		return out, true, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(existingYAML, &doc); err != nil {
		return nil, false, fmt.Errorf("pipeline: parsing existing yaml: %w", err)
	}

	stepsSeq, err := findStepsSequence(&doc)
	if err != nil {
		return nil, false, err
	}

	// Idempotency: if the step already exists, return the input untouched.
	for _, item := range stepsSeq.Content {
		if mappingValue(item, "name") == stepName {
			return existingYAML, false, nil
		}
	}

	// Append the new step as a node, preserving all existing comments/formatting.
	var stepNode yaml.Node
	if err := stepNode.Encode(newStep); err != nil {
		return nil, false, fmt.Errorf("pipeline: encoding step: %w", err)
	}
	stepsSeq.Content = append(stepsSeq.Content, &stepNode)

	out, err := marshalNode(&doc)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// findStepsSequence navigates a parsed document to the value sequence under the
// top-level "steps" key.
func findStepsSequence(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("pipeline: unexpected document structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("pipeline: expected a top-level mapping")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "steps" {
			val := root.Content[i+1]
			if val.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("pipeline: 'steps' is not a sequence")
			}
			return val, nil
		}
	}
	return nil, fmt.Errorf("pipeline: no 'steps' key found")
}

// mappingValue returns the scalar value for key in a mapping node, or "".
func mappingValue(m *yaml.Node, key string) string {
	if m.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1].Value
		}
	}
	return ""
}

// marshalNode encodes a node with a fixed 2-space indent.
func marshalNode(n *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, fmt.Errorf("pipeline: encoding document: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("pipeline: closing encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// marshalFresh renders a brand-new pipeline document containing only s.
func marshalFresh(s step) ([]byte, error) {
	var node yaml.Node
	if err := node.Encode(pipelineDoc{Steps: []step{s}}); err != nil {
		return nil, fmt.Errorf("pipeline: encoding fresh pipeline: %w", err)
	}
	return marshalNode(&node)
}
