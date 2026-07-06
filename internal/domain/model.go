// Package domain orchestrates Day 1 (scaffold) and Day 2 (add resource)
// operations, wiring the discovery and filesystem boundaries (behind
// interfaces) together with the pure HCL, validation, and pipeline packages.
package domain

import (
	"strings"

	"github.com/thomasmack/weave/internal/fs"
	"github.com/thomasmack/weave/internal/registry"
)

// Service is the Day 1 / Day 2 orchestrator. It accepts interfaces for its I/O
// boundaries and is therefore fully unit-testable with no real filesystem.
type Service struct {
	registry registry.ModuleRegistry
	ws       fs.ReadWriter
	env      string
}

// NewService wires the orchestrator. Invariant: ws MUST be scoped to env (e.g.
// fs.NewWorkspace(appFs, env)); env is held here because pipeline.EnsureStep and
// the <env>.tfvars filename consume it as a value.
func NewService(reg registry.ModuleRegistry, ws fs.ReadWriter, env string) *Service {
	return &Service{registry: reg, ws: ws, env: env}
}

// ScaffoldRequest carries the Day 1 project context.
type ScaffoldRequest struct {
	ProjectID   string
	StatePrefix string
}

// AddResourceRequest carries a Day 2 resource addition. Inputs are raw strings
// from the CLI; validate.Inputs coerces and validates them inside the Service.
type AddResourceRequest struct {
	ModuleType   string
	InstanceName string
	Inputs       map[string]string
}

// ChangeAction is the outcome recorded for a single file.
type ChangeAction string

const (
	ActionCreated   ChangeAction = "created"
	ActionUpdated   ChangeAction = "updated"
	ActionUnchanged ChangeAction = "unchanged"
)

// FileChange records the outcome for one workspace file. Path is the logical
// filename (e.g. "main.tf").
type FileChange struct {
	Path   string
	Action ChangeAction
}

// ChangeSet is the aggregate result of an orchestration call.
type ChangeSet struct {
	Files []FileChange
}

// Changed reports whether anything was actually written (any Created/Updated).
func (c ChangeSet) Changed() bool {
	for _, f := range c.Files {
		if f.Action == ActionCreated || f.Action == ActionUpdated {
			return true
		}
	}
	return false
}

// Summary renders a human-readable, one-line-per-file report for the terminal.
func (c ChangeSet) Summary() string {
	var b strings.Builder
	for i, f := range c.Files {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(f.Action))
		b.WriteByte(' ')
		b.WriteString(f.Path)
	}
	return b.String()
}

// Workspace file conventions (resolved under terraform/env/<env>/).
const (
	MainTFFile   = "main.tf"
	PipelineFile = "pipeline.yaml"
)

// tfvarsFile returns the per-environment variables filename, e.g. "dev.tfvars".
func (s *Service) tfvarsFile() string {
	return s.env + ".tfvars"
}
