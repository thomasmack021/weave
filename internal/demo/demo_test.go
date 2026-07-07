package demo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/thomasmack021/weave/internal/demo"
	"github.com/thomasmack021/weave/internal/git"
	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/server"
	"github.com/thomasmack021/weave/web"
)

// TestSetup_CreatesWorkingEnvironment asserts demo.Setup produces everything a
// zero-config local run needs: a bare workspace repo on branch main with the
// Day 1 scaffold committed, a parseable module spec with at least one choice
// input, and a reachable fake Bitbucket API.
func TestSetup_CreatesWorkingEnvironment(t *testing.T) {
	env, err := demo.Setup(t.TempDir())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer env.Close()

	// The spec manifest must parse through the production FileSource and
	// contain a choice-bearing module (the t-shirt-size showcase).
	specs, err := registry.NewFileSource(env.SpecsPath).List(context.Background())
	if err != nil {
		t.Fatalf("listing demo specs: %v", err)
	}
	if len(specs) < 2 {
		t.Fatalf("demo catalog has %d modules, want >= 2", len(specs))
	}
	hasChoice := false
	for _, spec := range specs {
		for _, in := range spec.Inputs {
			if in.Type == "choice" && len(in.Options) > 0 {
				hasChoice = true
			}
		}
	}
	if !hasChoice {
		t.Errorf("demo catalog has no choice input; the t-shirt-size flow cannot be demoed")
	}

	// The bare workspace repo must exist with the scaffold on main.
	repo, err := gogit.PlainOpen(env.RepoURL)
	if err != nil {
		t.Fatalf("opening demo workspace repo %s: %v", env.RepoURL, err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("reading demo repo HEAD: %v", err)
	}
	if got := head.Name().Short(); got != env.BaseBranch {
		t.Errorf("demo repo HEAD = %q, want %q", got, env.BaseBranch)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("reading HEAD commit: %v", err)
	}
	if _, err := commit.File("terraform/env/" + env.EnvName + "/main.tf"); err != nil {
		t.Errorf("demo repo missing scaffolded main.tf: %v", err)
	}

	// The fake Bitbucket must answer HTTP.
	resp, err := http.Get(env.BitbucketAPI + "/pr/0")
	if err != nil {
		t.Fatalf("fake bitbucket unreachable: %v", err)
	}
	resp.Body.Close()
}

// countBranches returns the number of refs/heads/* references in the repo at
// path.
func countBranches(t *testing.T, path string) int {
	t.Helper()
	repo, err := gogit.PlainOpen(path)
	if err != nil {
		t.Fatalf("opening repo %s: %v", path, err)
	}
	iter, err := repo.Branches()
	if err != nil {
		t.Fatalf("listing branches: %v", err)
	}
	n := 0
	if err := iter.ForEach(func(_ *plumbing.Reference) error { n++; return nil }); err != nil {
		t.Fatalf("iterating branches: %v", err)
	}
	return n
}

// newBareRepoWithoutScaffold builds a bare repo standing in for a developer's
// CD-pipeline repo before Weave has ever touched it: one commit containing
// only a README, no terraform/ tree. It is the fixture for the Day 1
// workspace-init happy path.
func newBareRepoWithoutScaffold(t *testing.T) (remoteDir string) {
	t.Helper()
	seedDir := t.TempDir()
	if _, err := gogit.PlainInitWithOptions(seedDir, &gogit.PlainInitOptions{
		InitOptions: gogit.InitOptions{DefaultBranch: plumbing.Main},
	}); err != nil {
		t.Fatalf("PlainInit seed: %v", err)
	}
	if err := os.WriteFile(seedDir+"/README.md", []byte("# CD pipeline repo\n"), 0o644); err != nil {
		t.Fatalf("writing README: %v", err)
	}
	seed, err := git.OpenWithAuthor(seedDir, git.Author{Name: "Weave Test", Email: "test@weave.dev"})
	if err != nil {
		t.Fatalf("OpenWithAuthor: %v", err)
	}
	if err := seed.Stage("README.md"); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := seed.Commit("initial commit"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	remoteDir = t.TempDir()
	if _, err := gogit.PlainClone(remoteDir, true, &gogit.CloneOptions{URL: seedDir}); err != nil {
		t.Fatalf("PlainClone bare: %v", err)
	}
	return remoteDir
}

// TestEndToEnd_WorkspaceInit is the Day 1 counterpart to the capstone below:
// it drives POST /api/workspace through the real production graph (real HTTP
// handler, real go-git, real Bitbucket HTTP provider) and proves both ends of
// the loop:
//  1. a fresh CD-pipeline repo with no terraform/ tree gets a 201 with a
//     branch carrying the full scaffold and the injected project_id, and the
//     PR URL serves a page;
//  2. re-initializing the demo's already-scaffolded repo is an idempotent
//     no-op (200, changed:false) that opens no branch — the real-world safety
//     property once the init PR has merged.
func TestEndToEnd_WorkspaceInit(t *testing.T) {
	env, err := demo.Setup(t.TempDir())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer env.Close()

	pr := git.NewHTTPProvider(env.BitbucketAPI, nil, env.Token)
	reg := registry.NewFileSource(env.SpecsPath)

	// --- Fresh repo: 201 with the scaffold PR. ---
	freshRepo := newBareRepoWithoutScaffold(t)
	freshOrch := orchestrate.New(reg, pr, orchestrate.Config{
		RepoURL:    freshRepo,
		RepoSlug:   env.RepoSlug,
		BaseBranch: env.BaseBranch,
		Token:      env.Token,
		Env:        env.EnvName,
	})
	freshTS := httptest.NewServer(server.New(web.Assets, reg, freshOrch, freshOrch).Handler())
	defer freshTS.Close()

	body := `{"projectId":"acme-prod-project","statePrefix":"weave/dev"}`
	resp, err := http.Post(freshTS.URL+"/api/workspace", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/workspace (fresh): %v", err)
	}
	initBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("workspace init: status %d, want 201; body %s", resp.StatusCode, initBody)
	}
	var result struct {
		PRURL  string `json:"prUrl"`
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal(initBody, &result); err != nil {
		t.Fatalf("decoding workspace response %s: %v", initBody, err)
	}
	if result.Branch != "weave/init-dev" {
		t.Errorf("branch = %q, want %q", result.Branch, "weave/init-dev")
	}
	resp, err = http.Get(result.PRURL)
	if err != nil {
		t.Fatalf("GET pr url %s: %v", result.PRURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: status %d, want 200", result.PRURL, resp.StatusCode)
	}

	// The pushed branch must carry the scaffold with the injected project_id.
	repo, err := gogit.PlainOpen(freshRepo)
	if err != nil {
		t.Fatalf("opening fresh repo: %v", err)
	}
	ref, err := repo.Reference("refs/heads/weave/init-dev", false)
	if err != nil {
		t.Fatalf("remote missing pushed init branch: %v", err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("reading init branch commit: %v", err)
	}
	tfvarsFile, err := commit.File("terraform/env/" + env.EnvName + "/" + env.EnvName + ".tfvars")
	if err != nil {
		t.Fatalf("init branch missing tfvars: %v", err)
	}
	tfvars, err := tfvarsFile.Contents()
	if err != nil {
		t.Fatalf("reading tfvars: %v", err)
	}
	if !strings.Contains(tfvars, `project_id = "acme-prod-project"`) {
		t.Errorf("scaffolded tfvars missing injected project_id:\n%s", tfvars)
	}
	if _, err := commit.File("terraform/env/" + env.EnvName + "/main.tf"); err != nil {
		t.Errorf("init branch missing scaffolded main.tf: %v", err)
	}

	// --- Already-scaffolded demo repo: idempotent no-op. ---
	demoOrch := orchestrate.New(reg, pr, orchestrate.Config{
		RepoURL:    env.RepoURL,
		RepoSlug:   env.RepoSlug,
		BaseBranch: env.BaseBranch,
		Token:      env.Token,
		Env:        env.EnvName,
	})
	demoTS := httptest.NewServer(server.New(web.Assets, reg, demoOrch, demoOrch).Handler())
	defer demoTS.Close()

	branchesBefore := countBranches(t, env.RepoURL)
	// Match the exact project the demo seeded (see demo.Setup): re-init with
	// identical context is the true no-op. A different project_id would be a
	// legitimate change, not a no-op.
	seededBody := `{"projectId":"acme-demo-project","statePrefix":"weave/dev"}`
	resp, err = http.Post(demoTS.URL+"/api/workspace", "application/json", strings.NewReader(seededBody))
	if err != nil {
		t.Fatalf("POST /api/workspace (already scaffolded): %v", err)
	}
	noopBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-init: status %d, want 200; body %s", resp.StatusCode, noopBody)
	}
	if !bytes.Contains(noopBody, []byte(`"changed":false`)) {
		t.Errorf("re-init body = %s, want changed:false", noopBody)
	}
	if got := countBranches(t, env.RepoURL); got != branchesBefore {
		t.Errorf("no-op re-init mutated the remote: %d branches, want %d", got, branchesBefore)
	}
}

// TestEndToEnd_DemoLoop is the v1 capstone: the full production dependency
// graph (FileSource registry, orchestrator, go-git, Bitbucket HTTP provider)
// assembled exactly as cmd/weaved does, driven through the real HTTP API.
//
// It proves, with zero mocks in the production path:
//  1. fail-before-mutate END TO END: a rejected request (unknown t-shirt
//     size) returns 422 and leaves the remote with zero new branches;
//  2. the happy path: choice input -> expanded golden-module PR, branch on
//     the remote containing the expanded values, PR URL that serves a page.
func TestEndToEnd_DemoLoop(t *testing.T) {
	env, err := demo.Setup(t.TempDir())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer env.Close()

	// Assemble the production graph exactly as cmd/weaved's run() does.
	reg := registry.NewFileSource(env.SpecsPath)
	pr := git.NewHTTPProvider(env.BitbucketAPI, nil, env.Token)
	orch := orchestrate.New(reg, pr, orchestrate.Config{
		RepoURL:    env.RepoURL,
		RepoSlug:   env.RepoSlug,
		BaseBranch: env.BaseBranch,
		Token:      env.Token,
		Env:        env.EnvName,
	})
	ts := httptest.NewServer(server.New(web.Assets, reg, orch, orch).Handler())
	defer ts.Close()

	// Liveness + catalog sanity: the wizard's data must be there.
	resp, err := http.Get(ts.URL + "/api/catalog")
	if err != nil {
		t.Fatalf("GET /api/catalog: %v", err)
	}
	catalogBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/catalog: status %d, body %s", resp.StatusCode, catalogBody)
	}
	if !bytes.Contains(catalogBody, []byte(`"cloud-run"`)) || !bytes.Contains(catalogBody, []byte(`"options"`)) {
		t.Fatalf("catalog missing cloud-run module or choice options:\n%s", catalogBody)
	}
	if bytes.Contains(catalogBody, []byte("expandsTo")) {
		t.Fatalf("catalog leaked expandsTo:\n%s", catalogBody)
	}

	branchesBefore := countBranches(t, env.RepoURL)

	// NEGATIVE FIRST — the whole-loop fail-before-mutate proof: an unknown
	// t-shirt size must be rejected as the caller's fault (422) and the
	// remote must be completely untouched.
	badReq := `{"moduleType":"cloud-run","instanceName":"checkout-api","inputs":{"service_name":"checkout-api","image":"gcr.io/acme/checkout:1.0.0","size":"galactic"}}`
	resp, err = http.Post(ts.URL+"/api/scaffold", "application/json", strings.NewReader(badReq))
	if err != nil {
		t.Fatalf("POST /api/scaffold (invalid): %v", err)
	}
	badBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid size: status %d, want 422; body %s", resp.StatusCode, badBody)
	}
	if got := countBranches(t, env.RepoURL); got != branchesBefore {
		t.Fatalf("rejected request mutated the remote: %d branches, want %d", got, branchesBefore)
	}

	// HAPPY PATH — small t-shirt size through the real loop.
	goodReq := `{"moduleType":"cloud-run","instanceName":"checkout-api","inputs":{"service_name":"checkout-api","image":"gcr.io/acme/checkout:1.0.0","size":"small"}}`
	resp, err = http.Post(ts.URL+"/api/scaffold", "application/json", strings.NewReader(goodReq))
	if err != nil {
		t.Fatalf("POST /api/scaffold: %v", err)
	}
	goodBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("scaffold: status %d, want 201; body %s", resp.StatusCode, goodBody)
	}
	var result struct {
		PRURL  string `json:"prUrl"`
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal(goodBody, &result); err != nil {
		t.Fatalf("decoding scaffold response %s: %v", goodBody, err)
	}
	if result.Branch != "weave/add-checkout-api" {
		t.Errorf("branch = %q, want %q", result.Branch, "weave/add-checkout-api")
	}

	// The PR URL must serve an actual page on the fake Bitbucket.
	resp, err = http.Get(result.PRURL)
	if err != nil {
		t.Fatalf("GET pr url %s: %v", result.PRURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s: status %d, want 200", result.PRURL, resp.StatusCode)
	}

	// The remote branch must exist and carry the EXPANDED small values —
	// and no trace of the virtual "size" key.
	repo, err := gogit.PlainOpen(env.RepoURL)
	if err != nil {
		t.Fatalf("opening demo repo: %v", err)
	}
	ref, err := repo.Reference("refs/heads/weave/add-checkout-api", false)
	if err != nil {
		t.Fatalf("remote missing pushed branch: %v", err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("reading branch commit: %v", err)
	}
	tfvarsFile, err := commit.File("terraform/env/" + env.EnvName + "/" + env.EnvName + ".tfvars")
	if err != nil {
		t.Fatalf("branch commit missing tfvars: %v", err)
	}
	tfvars, err := tfvarsFile.Contents()
	if err != nil {
		t.Fatalf("reading tfvars: %v", err)
	}
	if !strings.Contains(tfvars, "cpu = 1") || !strings.Contains(tfvars, `memory = "512Mi"`) {
		t.Errorf("tfvars missing expanded t-shirt values:\n%s", tfvars)
	}
	if strings.Contains(tfvars, "size") {
		t.Errorf("tfvars leaked the virtual choice key:\n%s", tfvars)
	}
}
