package orchestrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/thomasmack021/weave/internal/git"
	"github.com/thomasmack021/weave/internal/registry"
)

// fakePRProvider is a spy git.PullRequestProvider: it records every call so
// tests can assert a PR was (or, for the negative path, was NOT) opened.
type fakePRProvider struct {
	calls int
	url   string
	err   error
}

func (f *fakePRProvider) CreatePullRequest(_ context.Context, _, _, _, _, _ string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.url, nil
}

// newBareRemoteWithCommit builds a local bare repository seeded with one
// commit (containing main.tf) on branch "master", standing in for the target
// repo the orchestrator clones. Mirrors the fixture in
// internal/git/clone_test.go, duplicated here because that helper is
// unexported and test-only.
func newBareRemoteWithCommit(t *testing.T) (remoteDir string) {
	t.Helper()

	seedDir := t.TempDir()
	if _, err := gogit.PlainInit(seedDir, false); err != nil {
		t.Fatalf("PlainInit seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, "main.tf"), []byte("# weave\n"), 0o644); err != nil {
		t.Fatalf("writing seed main.tf: %v", err)
	}
	seed, err := git.OpenWithAuthor(seedDir, git.Author{Name: "Weave Test", Email: "test@weave.dev"})
	if err != nil {
		t.Fatalf("OpenWithAuthor: %v", err)
	}
	if err := seed.Stage("main.tf"); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := seed.Commit("initial commit"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	remoteDir = t.TempDir()
	if _, err := gogit.PlainInit(remoteDir, true); err != nil { // bare = "git init --bare"
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

// branchNames returns the short names of every local branch in the repo at
// dir, so tests can assert no new branch was created.
func branchNames(t *testing.T, dir string) []string {
	t.Helper()
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatalf("PlainOpen %s: %v", dir, err)
	}
	iter, err := repo.Branches()
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	var names []string
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		names = append(names, ref.Name().Short())
		return nil
	}); err != nil {
		t.Fatalf("iterating branches: %v", err)
	}
	return names
}

// TestRun_InvalidInput_NoRemoteMutation is the key negative test for the
// fail-before-mutate boundary: an invalid request (unknown module type) must
// be rejected during the pre-flight resolve/validate step, before git.Clone is
// ever reached. The remote must therefore end up with exactly the branches it
// started with, and no pull request may be opened.
func TestRun_InvalidInput_NoRemoteMutation(t *testing.T) {
	remoteDir := newBareRemoteWithCommit(t)
	before := branchNames(t, remoteDir)

	reg := registry.NewFakeRegistry() // empty: any ModuleType resolution fails
	pr := &fakePRProvider{url: "https://example.invalid/pr/1"}

	o := New(reg, pr, Config{
		RepoURL:    remoteDir,
		BaseBranch: "master",
		Token:      "",
		Env:        "dev",
	})

	_, err := o.Run(context.Background(), Request{
		ModuleType:   "does-not-exist",
		InstanceName: "example",
		Inputs:       map[string]string{},
	})

	if err == nil {
		t.Fatal("Run returned nil error, want an error for an unresolvable module type")
	}
	if !errors.Is(err, registry.ErrModuleNotFound) {
		t.Errorf("Run error = %v, want it to wrap registry.ErrModuleNotFound", err)
	}

	after := branchNames(t, remoteDir)
	if len(after) != len(before) {
		t.Errorf("remote branches after rejected Run = %v, want unchanged from %v", after, before)
	}

	if pr.calls != 0 {
		t.Errorf("CreatePullRequest called %d times, want 0 (invalid input must never reach the PR step)", pr.calls)
	}
}

// TestRun_ValidInput_PushesBranchAndOpensPR is the Green counterpart to the
// negative test above: a valid request must flow all the way through
// clone -> branch -> apply -> commit -> push -> PR, ending with the new
// branch present on the remote and exactly one PullRequestProvider call.
func TestRun_ValidInput_PushesBranchAndOpensPR(t *testing.T) {
	remoteDir := newBareRemoteWithCommit(t)

	reg := registry.NewFakeRegistry(registry.ModuleSpec{
		Name:    "cloud-run",
		Source:  "git::https://github.com/acme/iac-modules.git//modules/cloud-run",
		Version: "v2.4.0",
		Inputs: []registry.InputSpec{
			{Name: "service_name", Type: "string", Required: true, TfvarsKey: "service_name", ModuleArg: "name"},
		},
	})
	pr := &fakePRProvider{url: "https://bitbucket.example/pr/1"}

	o := New(reg, pr, Config{
		RepoURL:    remoteDir,
		RepoSlug:   "acme/infra",
		BaseBranch: "master",
		Token:      "",
		Env:        "dev",
	})

	result, err := o.Run(context.Background(), Request{
		ModuleType:   "cloud-run",
		InstanceName: "api",
		Inputs:       map[string]string{"service_name": "api"},
	})
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if !result.Changed {
		t.Fatal("Result.Changed = false, want true for a first-time module addition")
	}
	const wantBranch = "weave/add-api"
	if result.Branch != wantBranch {
		t.Errorf("Result.Branch = %q, want %q", result.Branch, wantBranch)
	}
	if result.PRURL != pr.url {
		t.Errorf("Result.PRURL = %q, want %q", result.PRURL, pr.url)
	}
	if pr.calls != 1 {
		t.Errorf("CreatePullRequest called %d times, want 1", pr.calls)
	}

	after := branchNames(t, remoteDir)
	found := false
	for _, n := range after {
		if n == wantBranch {
			found = true
		}
	}
	if !found {
		t.Errorf("remote branches = %v, want to include %q", after, wantBranch)
	}
}

// TestRun_PRCreationFails_ReportsPushedBranch covers the risky seam documented
// in ARCHITECTURE.md: once Push succeeds, a CreatePullRequest failure must
// still surface the pushed branch name in the Result, since the remote branch
// now exists with no PR and the caller needs to know to recover it.
func TestRun_PRCreationFails_ReportsPushedBranch(t *testing.T) {
	remoteDir := newBareRemoteWithCommit(t)

	reg := registry.NewFakeRegistry(registry.ModuleSpec{
		Name:    "cloud-run",
		Source:  "git::https://github.com/acme/iac-modules.git//modules/cloud-run",
		Version: "v2.4.0",
		Inputs: []registry.InputSpec{
			{Name: "service_name", Type: "string", Required: true, TfvarsKey: "service_name", ModuleArg: "name"},
		},
	})
	pr := &fakePRProvider{err: errors.New("bitbucket: rate limited")}

	o := New(reg, pr, Config{
		RepoURL:    remoteDir,
		RepoSlug:   "acme/infra",
		BaseBranch: "master",
		Token:      "",
		Env:        "dev",
	})

	result, err := o.Run(context.Background(), Request{
		ModuleType:   "cloud-run",
		InstanceName: "api",
		Inputs:       map[string]string{"service_name": "api"},
	})

	if err == nil {
		t.Fatal("Run returned nil error, want an error from the failing PullRequestProvider")
	}
	const wantBranch = "weave/add-api"
	if result.Branch != wantBranch {
		t.Errorf("Result.Branch = %q, want %q (branch must be reported even when PR creation fails)", result.Branch, wantBranch)
	}

	after := branchNames(t, remoteDir)
	found := false
	for _, n := range after {
		if n == wantBranch {
			found = true
		}
	}
	if !found {
		t.Errorf("remote branches = %v, want to include %q (push must have succeeded before the PR call)", after, wantBranch)
	}
}
