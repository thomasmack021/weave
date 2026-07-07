package domain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/thomasmack021/weave/internal/hcl"
	"github.com/thomasmack021/weave/internal/pipeline"
	"github.com/zclconf/go-cty/cty"
)

const mainTFBaseline = "# Managed by Weave CLI - Day 1 Scaffold\n"

// Scaffold performs Day 1 initialization: it lays down main.tf, <env>.tfvars,
// and pipeline.yaml in the workspace, injecting the project context. It is
// idempotent — re-running against an already-scaffolded workspace reports every
// file as unchanged and writes nothing.
func (s *Service) Scaffold(ctx context.Context, req ScaffoldRequest) (ChangeSet, error) {
	var cs ChangeSet

	// main.tf — baseline header on create; never modified on re-run.
	mainExisting, mainExisted, err := s.readExisting(MainTFFile)
	if err != nil {
		return ChangeSet{}, err
	}
	mainNext := mainExisting
	if !mainExisted {
		mainNext = []byte(mainTFBaseline)
	}
	change, err := s.reconcile(MainTFFile, mainExisting, mainExisted, mainNext)
	if err != nil {
		return ChangeSet{}, err
	}
	cs.Files = append(cs.Files, change)

	// <env>.tfvars — inject project_id. (StatePrefix is intentionally NOT written
	// here: Terraform backend config cannot be interpolated from tfvars.)
	tfvarsName := s.tfvarsFile()
	tfvarsExisting, tfvarsExisted, err := s.readExisting(tfvarsName)
	if err != nil {
		return ChangeSet{}, err
	}
	doc, err := hcl.LoadDocument(tfvarsExisting)
	if err != nil {
		return ChangeSet{}, fmt.Errorf("domain: parsing %s: %w", tfvarsName, err)
	}
	hcl.UpsertVariable(doc, "project_id", cty.StringVal(req.ProjectID))
	change, err = s.reconcile(tfvarsName, tfvarsExisting, tfvarsExisted, doc.Bytes())
	if err != nil {
		return ChangeSet{}, err
	}
	cs.Files = append(cs.Files, change)

	// pipeline.yaml — ensure the terraform execution step for this env.
	pipeExisting, pipeExisted, err := s.readExisting(PipelineFile)
	if err != nil {
		return ChangeSet{}, err
	}
	pipeNext, _, err := pipeline.EnsureStep(pipeExisting, req.ProjectID, req.StatePrefix, s.env)
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

// readExisting reads name from the workspace, distinguishing "absent" (existed
// == false, no error) from genuine read failures.
func (s *Service) readExisting(name string) (data []byte, existed bool, err error) {
	data, err = s.ws.Read(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("domain: reading %s: %w", name, err)
	}
	return data, true, nil
}

// reconcile writes next only when it differs from the current state, classifying
// the outcome as created, updated, or unchanged.
func (s *Service) reconcile(name string, existing []byte, existed bool, next []byte) (FileChange, error) {
	switch {
	case !existed:
		if err := s.ws.WriteAtomic(name, next); err != nil {
			return FileChange{}, fmt.Errorf("domain: writing %s: %w", name, err)
		}
		return FileChange{Path: name, Action: ActionCreated}, nil
	case bytes.Equal(existing, next):
		return FileChange{Path: name, Action: ActionUnchanged}, nil
	default:
		if err := s.ws.WriteAtomic(name, next); err != nil {
			return FileChange{}, fmt.Errorf("domain: writing %s: %w", name, err)
		}
		return FileChange{Path: name, Action: ActionUpdated}, nil
	}
}
