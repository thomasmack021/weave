// Package demo assembles a complete, self-contained Weave environment on the
// local machine: a bare git "workspace" repository seeded with the Day 1
// scaffold, an example module catalog showcasing t-shirt-size choices, and an
// in-process fake Bitbucket API that records pull requests and serves a page
// for each. It exists so anyone can experience the full wizard -> golden
// module -> pull request loop in seconds, with zero external dependencies —
// and so the end-to-end test can drive the real production graph.
//
// Nothing in this package is imported by the production path; cmd/weaved only
// calls Setup when the operator explicitly passes -demo.
package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/afero"

	"github.com/thomasmack/weave/internal/domain"
	"github.com/thomasmack/weave/internal/fs"
	"github.com/thomasmack/weave/internal/git"
	"github.com/thomasmack/weave/internal/registry"
)

// specYAML is the demo module catalog: two golden modules, each with a
// business-language choice input, so the wizard's t-shirt-size flow is
// demoable out of the box.
const specYAML = `apiVersion: weave.dev/v1
kind: ModuleManifest
metadata:
  registry: "git::https://github.com/acme/iac-modules.git"
  defaultRef: "v2.4.0"
modules:
  - name: cloud-run
    displayName: "Cloud Run Service"
    description: "Deploy a container as a fully managed, autoscaling HTTPS service."
    category: compute
    source: "git::https://github.com/acme/iac-modules.git//modules/cloud-run"
    version: "v2.4.0"
    stability: stable
    inputs:
      - name: service_name
        type: string
        required: true
        description: "Name of the service"
        tfvarsKey: service_name
        moduleArg: name
        validation:
          pattern: "^[a-z]([-a-z0-9]*[a-z0-9])?$"
          maxLength: 63
      - name: image
        type: string
        required: true
        description: "Container image to deploy"
        tfvarsKey: container_image
        moduleArg: image
      - name: cpu
        type: number
        tfvarsKey: cpu
        moduleArg: cpu
      - name: memory
        type: string
        tfvarsKey: memory
        moduleArg: memory
      - name: size
        type: choice
        required: true
        description: "How big should this service be?"
        options:
          - value: small
            label: "Small — prototypes & internal tools"
            description: "1 vCPU · 512Mi memory"
            expandsTo:
              cpu: 1
              memory: "512Mi"
          - value: medium
            label: "Medium — steady production traffic"
            description: "2 vCPU · 1Gi memory"
            expandsTo:
              cpu: 2
              memory: "1Gi"
          - value: large
            label: "Large — high-throughput services"
            description: "4 vCPU · 2Gi memory"
            expandsTo:
              cpu: 4
              memory: "2Gi"
  - name: bucket
    displayName: "Cloud Storage Bucket"
    description: "Provision an object storage bucket in the project's landing zone."
    category: storage
    source: "git::https://github.com/acme/iac-modules.git//modules/bucket"
    version: "v2.4.0"
    stability: stable
    inputs:
      - name: bucket_name
        type: string
        required: true
        description: "Globally unique bucket name"
        tfvarsKey: bucket_name
        moduleArg: name
      - name: storage_class
        type: string
        tfvarsKey: storage_class
        moduleArg: storage_class
      - name: tier
        type: choice
        required: true
        description: "How will this data be accessed?"
        options:
          - value: standard
            label: "Standard — frequently accessed data"
            description: "Hot storage, no retrieval fees"
            expandsTo:
              storage_class: "STANDARD"
          - value: archive
            label: "Archive — long-term retention"
            description: "Cheapest storage for rarely accessed data"
            expandsTo:
              storage_class: "ARCHIVE"
`

// Environment describes a ready-to-use local demo: pass its fields straight
// into the production dependency graph (registry.NewFileSource,
// git.NewHTTPProvider, orchestrate.Config).
type Environment struct {
	Dir          string // root directory holding everything below
	RepoURL      string // clone URL of the bare demo workspace repo (a local path)
	RepoSlug     string // workspace/repo slug the fake Bitbucket accepts
	SpecsPath    string // path to the demo module catalog (spec.yaml)
	BitbucketAPI string // base URL of the in-process fake Bitbucket
	BaseBranch   string // "main"
	EnvName      string // "dev"
	Token        string // empty: local path pushes need no auth

	bitbucket *http.Server
}

// Close shuts down the fake Bitbucket server. The caller owns Dir's lifetime.
func (e *Environment) Close() {
	if e.bitbucket != nil {
		e.bitbucket.Close()
	}
}

// Setup builds the full demo environment under dir. See the package doc.
func Setup(dir string) (*Environment, error) {
	env := &Environment{
		Dir:        dir,
		RepoSlug:   "platform/demo-workspace",
		BaseBranch: "main",
		EnvName:    "dev",
	}

	// 1. The module catalog the platform team would normally publish.
	env.SpecsPath = filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(env.SpecsPath, []byte(specYAML), 0o644); err != nil {
		return nil, fmt.Errorf("demo: writing spec catalog: %w", err)
	}

	// 2. Seed repo on main with the Day 1 scaffold, produced by the real
	// domain layer — the demo runs the same code as production.
	seedDir := filepath.Join(dir, "seed")
	if _, err := gogit.PlainInitWithOptions(seedDir, &gogit.PlainInitOptions{
		InitOptions: gogit.InitOptions{DefaultBranch: plumbing.Main},
	}); err != nil {
		return nil, fmt.Errorf("demo: initializing seed repo: %w", err)
	}
	ws := fs.NewWorkspace(afero.NewBasePathFs(afero.NewOsFs(), seedDir), env.EnvName)
	svc := domain.NewService(registry.NewFakeRegistry(), ws, env.EnvName)
	if _, err := svc.Scaffold(context.Background(), domain.ScaffoldRequest{
		ProjectID:   "acme-demo-project",
		StatePrefix: "weave/dev",
	}); err != nil {
		return nil, fmt.Errorf("demo: scaffolding Day 1 workspace: %w", err)
	}
	seed, err := git.OpenWithAuthor(seedDir, git.Author{Name: "Weave Demo", Email: "demo@weave.dev"})
	if err != nil {
		return nil, fmt.Errorf("demo: opening seed repo: %w", err)
	}
	if err := seed.Stage("terraform"); err != nil {
		return nil, fmt.Errorf("demo: staging scaffold: %w", err)
	}
	if _, err := seed.Commit("weave demo: Day 1 scaffold"); err != nil {
		return nil, fmt.Errorf("demo: committing scaffold: %w", err)
	}

	// 3. Bare mirror = the "remote" workspace repo the orchestrator clones
	// from and pushes to.
	bareDir := filepath.Join(dir, "workspace.git")
	if _, err := gogit.PlainClone(bareDir, true, &gogit.CloneOptions{URL: seedDir}); err != nil {
		return nil, fmt.Errorf("demo: creating bare workspace repo: %w", err)
	}
	env.RepoURL = bareDir

	// 4. In-process fake Bitbucket: records PRs, serves a page per PR.
	api, srv, err := startFakeBitbucket(bareDir)
	if err != nil {
		return nil, err
	}
	env.BitbucketAPI = api
	env.bitbucket = srv

	return env, nil
}

// prRecord is one pull request accepted by the fake Bitbucket.
type prRecord struct {
	Title       string
	Source      string
	Destination string
	Description string
}

// startFakeBitbucket serves the two endpoints the demo needs on a random
// localhost port: the Bitbucket Cloud "create pull request" API and a human
// -readable page per created PR. It returns the base URL and the server.
func startFakeBitbucket(repoPath string) (string, *http.Server, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("demo: starting fake bitbucket listener: %w", err)
	}
	baseURL := "http://" + l.Addr().String()

	var (
		mu  sync.Mutex
		prs []prRecord
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/2.0/repositories/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/pullrequests") {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Title  string `json:"title"`
			Source struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
			} `json:"source"`
			Destination struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
			} `json:"destination"`
			Description string `json:"description"`
		}
		if err := decodeJSON(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		prs = append(prs, prRecord{
			Title:       body.Title,
			Source:      body.Source.Branch.Name,
			Destination: body.Destination.Branch.Name,
			Description: body.Description,
		})
		id := len(prs)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"links":{"html":{"href":"%s/pr/%d"}}}`, baseURL, id)
	})
	mux.HandleFunc("/pr/", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/pr/"))
		mu.Lock()
		defer mu.Unlock()
		if err != nil || id < 1 || id > len(prs) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, "<h1>Weave demo Bitbucket</h1><p>%d pull request(s) so far.</p>", len(prs))
			return
		}
		pr := prs[id-1]
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Demo PR #%d</title></head><body style="font-family:sans-serif;max-width:640px;margin:48px auto">
<h1>🎭 Demo pull request #%d</h1>
<p>In production this page would be your Bitbucket PR, ready for platform-team review.</p>
<table border="0" cellpadding="6">
<tr><td><b>Title</b></td><td>%s</td></tr>
<tr><td><b>Source branch</b></td><td><code>%s</code></td></tr>
<tr><td><b>Target branch</b></td><td><code>%s</code></td></tr>
<tr><td><b>Changes</b></td><td><pre>%s</pre></td></tr>
</table>
<p>Inspect the real commit locally:<br><code>git clone %s && git -C workspace log --stat %s</code></p>
</body></html>`, id, id, pr.Title, pr.Source, pr.Destination, pr.Description, repoPath, pr.Source)
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(l) //nolint:errcheck // Close() shutdown error is expected
	return baseURL, srv, nil
}

// decodeJSON decodes the request body into v.
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
