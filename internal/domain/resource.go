package domain

import (
	"context"
	"fmt"

	"github.com/thomasmack/weave/internal/hcl"
	"github.com/thomasmack/weave/internal/pipeline"
	"github.com/thomasmack/weave/internal/validate"
)

// AddResource performs a Day 2 resource addition: it resolves the module from
// the registry, validates the raw inputs, injects a module block into main.tf
// and the corresponding variables into <env>.tfvars, and enforces the pipeline
// step. It is idempotent — re-running with the same request reports every file
// as unchanged.
func (s *Service) AddResource(ctx context.Context, req AddResourceRequest) (ChangeSet, error) {
	var cs ChangeSet

	spec, err := s.registry.Resolve(ctx, req.ModuleType)
	if err != nil {
		return ChangeSet{}, fmt.Errorf("domain: resolving module %q: %w", req.ModuleType, err)
	}

	validated, err := validate.Inputs(spec, req.Inputs)
	if err != nil {
		return ChangeSet{}, fmt.Errorf("domain: validating inputs for %q: %w", req.ModuleType, err)
	}

	// main.tf — insert the module block (idempotent on the instance label).
	mainExisting, mainExisted, err := s.readExisting(MainTFFile)
	if err != nil {
		return ChangeSet{}, err
	}
	mainDoc, err := hcl.LoadDocument(mainExisting)
	if err != nil {
		return ChangeSet{}, fmt.Errorf("domain: parsing %s: %w", MainTFFile, err)
	}
	if _, err := hcl.InsertModule(mainDoc, req.InstanceName, spec, validated); err != nil {
		return ChangeSet{}, fmt.Errorf("domain: inserting module %q: %w", req.InstanceName, err)
	}
	change, err := s.reconcile(MainTFFile, mainExisting, mainExisted, mainDoc.Bytes())
	if err != nil {
		return ChangeSet{}, err
	}
	cs.Files = append(cs.Files, change)

	// <env>.tfvars — inject each validated input under its TfvarsKey. Iterating
	// spec.Inputs (not the validated map) keeps the write order deterministic.
	tfvarsName := s.tfvarsFile()
	tfvarsExisting, tfvarsExisted, err := s.readExisting(tfvarsName)
	if err != nil {
		return ChangeSet{}, err
	}
	tfvarsDoc, err := hcl.LoadDocument(tfvarsExisting)
	if err != nil {
		return ChangeSet{}, fmt.Errorf("domain: parsing %s: %w", tfvarsName, err)
	}
	for _, in := range spec.Inputs {
		val, ok := validated[in.Name]
		if !ok {
			continue
		}
		hcl.UpsertVariable(tfvarsDoc, in.TfvarsKey, val)
	}
	change, err = s.reconcile(tfvarsName, tfvarsExisting, tfvarsExisted, tfvarsDoc.Bytes())
	if err != nil {
		return ChangeSet{}, err
	}
	cs.Files = append(cs.Files, change)

	// pipeline.yaml — enforce the step. Day 1 owns the project context, so the
	// step already exists and this is a no-op; passing empty project/state here
	// is safe because EnsureStep keys off the step name.
	pipeExisting, pipeExisted, err := s.readExisting(PipelineFile)
	if err != nil {
		return ChangeSet{}, err
	}
	pipeNext, _, err := pipeline.EnsureStep(pipeExisting, "", "", s.env)
	if err != nil {
		return ChangeSet{}, fmt.Errorf("domain: ensuring pipeline step: %w", err)
	}
	change, err = s.reconcile(PipelineFile, pipeExisting, pipeExisted, pipeNext)
	if err != nil {
		return ChangeSet{}, err
	}
	cs.Files = append(cs.Files, change)

	return cs, nil
}
