// Command weaved is the Weave IDP server: the first runnable binary of the
// walking skeleton. Per the engagement rules it contains assembly only — all
// dependencies are constructed here and injected; no package holds globals.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/thomasmack/weave/internal/demo"
	"github.com/thomasmack/weave/internal/git"
	"github.com/thomasmack/weave/internal/orchestrate"
	"github.com/thomasmack/weave/internal/registry"
	"github.com/thomasmack/weave/internal/server"
	"github.com/thomasmack/weave/web"
)

func main() {
	if err := run(os.Args[1:], os.Getenv); err != nil {
		log.Fatal(err)
	}
}

// run assembles the dependency graph from config and serves HTTP. It exists
// so main stays a one-line exit-code adapter.
func run(args []string, getenv func(string) string) error {
	cfg, err := loadConfig(args, getenv)
	if err != nil {
		return err
	}

	if cfg.Demo {
		dir, err := os.MkdirTemp("", "weave-demo-*")
		if err != nil {
			return fmt.Errorf("weaved: creating demo dir: %w", err)
		}
		env, err := demo.Setup(dir)
		if err != nil {
			return fmt.Errorf("weaved: setting up demo environment: %w", err)
		}
		defer env.Close()
		cfg.Specs = env.SpecsPath
		cfg.RepoURL = env.RepoURL
		cfg.RepoSlug = env.RepoSlug
		cfg.BaseBranch = env.BaseBranch
		cfg.Token = env.Token
		cfg.Env = env.EnvName
		cfg.BitbucketAPI = env.BitbucketAPI
		url := cfg.Listen
		if url[0] == ':' {
			url = "localhost" + url
		}
		log.Printf("demo mode: workspace repo %s, fake Bitbucket %s", env.RepoURL, env.BitbucketAPI)
		log.Printf("demo mode: open http://%s and provision something!", url)
	}

	reg := registry.NewFileSource(cfg.Specs)
	pr := git.NewHTTPProvider(cfg.BitbucketAPI, nil, cfg.Token)
	orch := orchestrate.New(reg, pr, orchestrate.Config{
		RepoURL:    cfg.RepoURL,
		RepoSlug:   cfg.RepoSlug,
		BaseBranch: cfg.BaseBranch,
		Token:      cfg.Token,
		Env:        cfg.Env,
	})
	srv := server.New(web.Assets, reg, orch)

	log.Printf("weaved listening on %s (specs=%s repo=%s env=%s bitbucket=%s)",
		cfg.Listen, cfg.Specs, cfg.RepoSlug, cfg.Env, cfg.BitbucketAPI)
	if err := http.ListenAndServe(cfg.Listen, srv.Handler()); err != nil {
		return fmt.Errorf("weaved: serving on %s: %w", cfg.Listen, err)
	}
	return nil
}
