package orchestrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/afero"

	"github.com/thomasmack021/weave/internal/domain"
	"github.com/thomasmack021/weave/internal/fs"
	"github.com/thomasmack021/weave/internal/git"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/validate"
)

// newBareRemoteScaffolded builds a local bare repository whose single commit
// already contains the Day 1 workspace for env, produced by the real
// domain.Scaffold — the fixture for idempotency tests, mirroring how
// internal/demo seeds its workspace repo.
func newBareRemoteScaffolded(t *testing.T, projectID, statePrefix, env string) (remoteDir string) {
	t.Helper()

	seedDir := t.TempDir()
	if _, err := gogit.PlainInit(seedDir, false); err != nil {
		t.Fatalf("PlainInit seed: %v", err)
	}
	ws := fs.NewWorkspace(afero.NewBasePathFs(afero.NewOsFs(), seedDir), env)
	svc := domain.NewService(registry.NewFakeRegistry(), ws, env)
	if _, err := svc.Scaffold(context.Background(), domain.ScaffoldRequest{
		ProjectID:   projectID,
		StatePrefix: statePrefix,
	}); err != nil {
		t.Fatalf("Scaffold (seeding): %v", err)
	}
	seed, err := git.OpenWithAuthor(seedDir, git.Author{Name: "Weave Test", Email: "test@weave.dev"})
	if err != nil {
		t.Fatalf("OpenWithAuthor: %v", err)
	}
	if err := seed.Stage("terraform"); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := seed.Commit("seed: Day 1 scaffold"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	remoteDir = t.TempDir()
	if _, err := gogit.PlainInit(remoteDir, true); err != nil {
		t.Fatalf("PlainInit bare: %v", err)
	}
	seedRepo, err := gogit.PlainOpen(seedDir)
	if err != nil {
		t.Fatalf("PlainOpen seed: %v", err)
	}
	if _, err := seedRepo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("CreateRemote: %v", err)
	}
	if err := seed.Push(context.Background(), "origin", ""); err != nil {
		t.Fatalf("Push (seeding remote): %v", err)
	}
	return remoteDir
}

// readBranchFile clones branch from remoteDir and returns the contents of the
// repo-relative path, so tests can assert on what was actually pushed.
func readBranchFile(t *testing.T, remoteDir, branch, path string) []byte {
	t.Helper()
	dir := t.TempDir()
	if _, err := gogit.PlainClone(dir, false, &gogit.CloneOptions{
		URL:           remoteDir,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
	}); err != nil {
		t.Fatalf("PlainClone %s@%s: %v", remoteDir, branch, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		t.Fatalf("reading %s from branch %s: %v", path, branch, err)
	}
	return data
}

// TestInitWorkspace_MissingProjectID_NoRemoteMutation pins fail-before-mutate
// for Day 1: a request without a project ID is the caller's fault
// (validate.ErrMissingRequired → 422 at the API boundary) and must be
// rejected before git.Clone is ever reached — zero new remote branches, zero
// PR-provider calls.
func TestInitWorkspace_MissingProjectID_NoRemoteMutation(t *testing.T) {
	remoteDir := newBareRemoteWithCommit(t)
	before := branchNames(t, remoteDir)

	pr := &fakePRProvider{url: "https://example.invalid/pr/1"}
	o := New(registry.NewFakeRegistry(), pr, Config{
		RepoURL:    remoteDir,
		BaseBranch: "master",
		Env:        "dev",
	})

	_, err := o.InitWorkspace(context.Background(), InitRequest{
		ProjectID:   "   ",
		StatePrefix: "weave/dev",
	})

	if err == nil {
		t.Fatal("InitWorkspace returned nil error, want an error for a missing project ID")
	}
	if !errors.Is(err, validate.ErrMissingRequired) {
		t.Errorf("InitWorkspace error = %v, want it to wrap validate.ErrMissingRequired", err)
	}

	after := branchNames(t, remoteDir)
	if len(after) != len(before) {
		t.Errorf("remote branches after rejected InitWorkspace = %v, want unchanged from %v", after, before)
	}
	if pr.calls != 0 {
		t.Errorf("CreatePullRequest called %d times, want 0", pr.calls)
	}
}

// TestInitWorkspace_FreshRepo_PushesScaffoldAndOpensPR is the Day 1 happy
// path: a repo with no workspace for the environment gains a branch
// containing the full scaffold (main.tf, <env>.tfvars with the project ID
// injected, pipeline.yaml) and exactly one pull request is opened.
func TestInitWorkspace_FreshRepo_PushesScaffoldAndOpensPR(t *testing.T) {
	remoteDir := newBareRemoteWithCommit(t)

	pr := &fakePRProvider{url: "https://bitbucket.example/pr/7"}
	o := New(registry.NewFakeRegistry(), pr, Config{
		RepoURL:    remoteDir,
		RepoSlug:   "acme/infra",
		BaseBranch: "master",
		Env:        "dev",
	})

	result, err := o.InitWorkspace(context.Background(), InitRequest{
		ProjectID:   "acme-prod-project",
		StatePrefix: "weave/dev",
	})
	if err != nil {
		t.Fatalf("InitWorkspace returned unexpected error: %v", err)
	}
	if !result.Changed {
		t.Fatal("Result.Changed = false, want true for a repo without a workspace")
	}
	const wantBranch = "weave/init-dev"
	if result.Branch != wantBranch {
		t.Errorf("Result.Branch = %q, want %q", result.Branch, wantBranch)
	}
	if result.PRURL != pr.url {
		t.Errorf("Result.PRURL = %q, want %q", result.PRURL, pr.url)
	}
	if pr.calls != 1 {
		t.Errorf("CreatePullRequest called %d times, want 1", pr.calls)
	}

	tfvars := readBranchFile(t, remoteDir, wantBranch, "terraform/env/dev/dev.tfvars")
	if !strings.Contains(string(tfvars), `project_id = "acme-prod-project"`) {
		t.Errorf("dev.tfvars on %s = %q, want it to contain the injected project_id", wantBranch, tfvars)
	}
	for _, f := range []string{"terraform/env/dev/main.tf", "terraform/env/dev/pipeline.yaml"} {
		readBranchFile(t, remoteDir, wantBranch, f) // fails the test if absent
	}
}

// TestInitWorkspace_EmptyStatePrefix_DerivesDefault pins the north-star
// contract that the developer never supplies Terraform backend plumbing: when
// the request omits the state prefix, the orchestrator derives "weave/<env>"
// so the wizard can ask only for business-language fields.
func TestInitWorkspace_EmptyStatePrefix_DerivesDefault(t *testing.T) {
	remoteDir := newBareRemoteWithCommit(t)

	pr := &fakePRProvider{url: "https://bitbucket.example/pr/1"}
	o := New(registry.NewFakeRegistry(), pr, Config{
		RepoURL:    remoteDir,
		RepoSlug:   "acme/infra",
		BaseBranch: "master",
		Env:        "dev",
	})

	result, err := o.InitWorkspace(context.Background(), InitRequest{
		ProjectID: "acme-prod-project",
		// StatePrefix intentionally omitted.
	})
	if err != nil {
		t.Fatalf("InitWorkspace returned unexpected error: %v", err)
	}
	if !result.Changed {
		t.Fatal("Result.Changed = false, want true")
	}

	pipeline := readBranchFile(t, remoteDir, "weave/init-dev", "terraform/env/dev/pipeline.yaml")
	if !strings.Contains(string(pipeline), "weave/dev") {
		t.Errorf("pipeline.yaml = %q, want the derived state prefix \"weave/dev\"", pipeline)
	}
}

// TestInitWorkspace_AlreadyScaffolded_NoOp pins Day 1 idempotency end to end:
// re-initializing a workspace that already matches the request must change
// nothing — no new branch, no pull request, Changed == false.
func TestInitWorkspace_AlreadyScaffolded_NoOp(t *testing.T) {
	remoteDir := newBareRemoteScaffolded(t, "acme-prod-project", "weave/dev", "dev")
	before := branchNames(t, remoteDir)

	pr := &fakePRProvider{url: "https://example.invalid/pr/1"}
	o := New(registry.NewFakeRegistry(), pr, Config{
		RepoURL:    remoteDir,
		RepoSlug:   "acme/infra",
		BaseBranch: "master",
		Env:        "dev",
	})

	result, err := o.InitWorkspace(context.Background(), InitRequest{
		ProjectID:   "acme-prod-project",
		StatePrefix: "weave/dev",
	})
	if err != nil {
		t.Fatalf("InitWorkspace returned unexpected error: %v", err)
	}
	if result.Changed {
		t.Fatal("Result.Changed = true, want false for an already-scaffolded workspace")
	}
	if result.Branch != "" || result.PRURL != "" {
		t.Errorf("Result = %+v, want no branch and no PR URL for a no-op", result)
	}
	if pr.calls != 0 {
		t.Errorf("CreatePullRequest called %d times, want 0", pr.calls)
	}

	after := branchNames(t, remoteDir)
	if len(after) != len(before) {
		t.Errorf("remote branches after no-op = %v, want unchanged from %v", after, before)
	}
}
