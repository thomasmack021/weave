package orchestrate

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/afero"

	"github.com/thomasmack021/weave/internal/domain"
	"github.com/thomasmack021/weave/internal/fs"
	"github.com/thomasmack021/weave/internal/git"
	"github.com/thomasmack021/weave/internal/validate"
)

// InitRequest carries the Day 1 workspace-initialization request from the
// caller. The target repo, base branch, and environment come from the
// server-owned Config, never from the client.
type InitRequest struct {
	ProjectID   string
	StatePrefix string
}

// InitWorkspace executes one end-to-end Day 1 initialization: it lays down
// the terraform/env/<env>/ scaffold (main.tf, <env>.tfvars, pipeline.yaml)
// in the target repo and opens a pull request, under the same
// fail-before-mutate guarantee as Run. Re-running against an
// already-initialized workspace is a no-op that touches nothing.
func (o *Orchestrator) InitWorkspace(ctx context.Context, req InitRequest) (Result, error) {
	// Pre-flight: the project ID is injected into <env>.tfvars, so a blank
	// one is a caller fault — rejected before git.Clone is ever reached.
	if strings.TrimSpace(req.ProjectID) == "" {
		return Result{}, fmt.Errorf("orchestrate: validating init request: %w: projectId", validate.ErrMissingRequired)
	}

	// State prefix is Terraform backend plumbing, not something the developer
	// should ever supply (invariant: the developer never sees Terraform). When
	// omitted, derive the conventional "weave/<env>" so the wizard can ask for
	// business-language fields only.
	statePrefix := req.StatePrefix
	if strings.TrimSpace(statePrefix) == "" {
		statePrefix = "weave/" + o.cfg.Env
	}

	dir, err := os.MkdirTemp("", "weave-orchestrate-*")
	if err != nil {
		return Result{}, fmt.Errorf("orchestrate: creating temp clone dir: %w", err)
	}
	defer os.RemoveAll(dir)

	repo, err := git.Clone(ctx, o.cfg.RepoURL, o.cfg.BaseBranch, o.cfg.Token, dir)
	if err != nil {
		return Result{}, fmt.Errorf("orchestrate: cloning %s: %w", o.cfg.RepoURL, err)
	}

	branch := "weave/init-" + o.cfg.Env
	if err := repo.CheckoutBranch(branch); err != nil {
		return Result{}, fmt.Errorf("orchestrate: checking out branch %q: %w", branch, err)
	}

	ws := fs.NewWorkspace(afero.NewBasePathFs(afero.NewOsFs(), dir), o.cfg.Env)
	svc := domain.NewService(o.registry, ws, o.cfg.Env)

	cs, err := svc.Scaffold(ctx, domain.ScaffoldRequest{
		ProjectID:   req.ProjectID,
		StatePrefix: statePrefix,
	})
	if err != nil {
		return Result{}, fmt.Errorf("orchestrate: scaffolding workspace: %w", err)
	}
	if !cs.Changed() {
		return Result{Changed: false}, nil
	}

	return o.publish(ctx, repo, ws, cs, branch,
		fmt.Sprintf("weave: init %s workspace", o.cfg.Env),
		fmt.Sprintf("Initialize %s workspace", o.cfg.Env),
	)
}
