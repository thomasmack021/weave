// Package orchestrate composes the registry, validation, domain, and git
// packages into the end-to-end GitOps flow: resolve and validate a request,
// clone the target repo into a disposable temp directory, apply the change,
// and — if anything changed — commit, push, and open a pull request.
//
// Fail-before-mutate is the central invariant: registry resolution and input
// validation run before git.Clone is ever called, so a rejected request
// touches neither the temp directory nor the remote.
package orchestrate

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/afero"

	"github.com/thomasmack021/weave/internal/domain"
	"github.com/thomasmack021/weave/internal/fs"
	"github.com/thomasmack021/weave/internal/git"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/validate"
)

// Config carries the server-owned settings for one orchestrated run: which
// repo to clone, which branch to base off, and the credentials/environment to
// use. It is never populated from the incoming request.
type Config struct {
	RepoURL    string // clone URL passed to git.Clone
	RepoSlug   string // workspace/repo slug passed to the PullRequestProvider
	BaseBranch string
	Token      string
	Env        string
}

// Orchestrator drives the resolve -> validate -> clone -> branch -> apply ->
// commit -> push -> PR composition. Its dependencies are injected; assembly
// happens only in main.
type Orchestrator struct {
	registry registry.ModuleRegistry
	pr       git.PullRequestProvider
	cfg      Config
}

// New builds an Orchestrator from its dependencies and config.
func New(reg registry.ModuleRegistry, pr git.PullRequestProvider, cfg Config) *Orchestrator {
	return &Orchestrator{registry: reg, pr: pr, cfg: cfg}
}

// Request carries the Day 2 resource-addition request from the caller (the
// HTTP layer, in the walking skeleton).
type Request struct {
	ModuleType   string
	InstanceName string
	Inputs       map[string]string
}

// Result reports the outcome of a Run.
type Result struct {
	Changed bool
	Branch  string
	PRURL   string
}

// Run executes one end-to-end GitOps request. See the package doc for the
// fail-before-mutate guarantee.
func (o *Orchestrator) Run(ctx context.Context, req Request) (Result, error) {
	spec, err := o.registry.Resolve(ctx, req.ModuleType)
	if err != nil {
		return Result{}, fmt.Errorf("orchestrate: resolving module %q: %w", req.ModuleType, err)
	}
	if _, err := validate.Inputs(spec, req.Inputs); err != nil {
		return Result{}, fmt.Errorf("orchestrate: validating inputs for %q: %w", req.ModuleType, err)
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

	branch := "weave/add-" + req.InstanceName
	if err := repo.CheckoutBranch(branch); err != nil {
		return Result{}, fmt.Errorf("orchestrate: checking out branch %q: %w", branch, err)
	}

	ws := fs.NewWorkspace(afero.NewBasePathFs(afero.NewOsFs(), dir), o.cfg.Env)
	svc := domain.NewService(o.registry, ws, o.cfg.Env)

	cs, err := svc.AddResource(ctx, domain.AddResourceRequest{
		ModuleType:   req.ModuleType,
		InstanceName: req.InstanceName,
		Inputs:       req.Inputs,
	})
	if err != nil {
		return Result{}, fmt.Errorf("orchestrate: applying change: %w", err)
	}
	if !cs.Changed() {
		return Result{Changed: false}, nil
	}

	paths := make([]string, 0, len(cs.Files))
	for _, f := range cs.Files {
		if f.Action == domain.ActionUnchanged {
			continue
		}
		paths = append(paths, ws.Path(f.Path))
	}

	if err := repo.Stage(paths...); err != nil {
		return Result{}, fmt.Errorf("orchestrate: staging changed files: %w", err)
	}
	if _, err := repo.Commit(fmt.Sprintf("weave: add %s %s", req.ModuleType, req.InstanceName)); err != nil {
		return Result{}, fmt.Errorf("orchestrate: committing: %w", err)
	}
	if err := repo.Push(ctx, "origin", o.cfg.Token); err != nil {
		return Result{}, fmt.Errorf("orchestrate: pushing branch %q: %w", branch, err)
	}

	prURL, err := o.pr.CreatePullRequest(ctx, o.cfg.RepoSlug, branch, o.cfg.BaseBranch,
		fmt.Sprintf("Add %s: %s", req.ModuleType, req.InstanceName),
		cs.Summary(),
	)
	if err != nil {
		// Push already succeeded: the branch exists on the remote with no PR.
		// Report it so the caller can recover (e.g. retry PR creation) instead
		// of losing track of the pushed branch.
		return Result{Changed: true, Branch: branch}, fmt.Errorf("orchestrate: opening pull request for pushed branch %q: %w", branch, err)
	}

	return Result{Changed: true, Branch: branch, PRURL: prURL}, nil
}
