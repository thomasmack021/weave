// Command weaved is the Weave IDP server: the first runnable binary of the
// walking skeleton. Per the engagement rules it contains assembly only — all
// dependencies are constructed here and injected; no package holds globals.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/thomasmack021/weave/internal/auth"
	"github.com/thomasmack021/weave/internal/demo"
	"github.com/thomasmack021/weave/internal/git"
	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/server"
	"github.com/thomasmack021/weave/internal/store"
	"github.com/thomasmack021/weave/internal/usecase"
	"github.com/thomasmack021/weave/web"
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
	pr, err := git.NewProvider(cfg.PRProvider, cfg.BitbucketAPI, cfg.Token)
	if err != nil {
		return err
	}
	orch := orchestrate.New(reg, pr, orchestrate.Config{
		RepoURL:    cfg.RepoURL,
		RepoSlug:   cfg.RepoSlug,
		BaseBranch: cfg.BaseBranch,
		Token:      cfg.Token,
		Env:        cfg.Env,
	})
	srv := server.New(web.Assets, reg, orch, orch)

	// Identity, sessions & multi-tenant RBAC (opt-in): when an auth mode is
	// configured, open the database, apply migrations, and attach both the
	// session layer and the use-case dispatcher.
	if cfg.AuthMode != "" {
		sessions, useCases, closeStore, err := setupTenancy(context.Background(), cfg, reg)
		if err != nil {
			return fmt.Errorf("weaved: initializing tenancy: %w", err)
		}
		defer closeStore()
		srv = srv.WithSessions(sessions).WithUseCases(useCases)
		log.Printf("weaved: multi-tenant mode enabled (auth-mode=%s, bootstrap-admins=%d)", cfg.AuthMode, len(cfg.BootstrapAdmins))
	}

	log.Printf("weaved listening on %s (specs=%s repo=%s env=%s provider=%s api=%s)",
		cfg.Listen, cfg.Specs, cfg.RepoSlug, cfg.Env, cfg.PRProvider, cfg.BitbucketAPI)
	if err := http.ListenAndServe(cfg.Listen, srv.Handler()); err != nil {
		return fmt.Errorf("weaved: serving on %s: %w", cfg.Listen, err)
	}
	return nil
}

// setupTenancy opens the database, applies migrations, and builds the session
// Service (identity) and the use-case dispatcher (multi-tenant RBAC), both
// sharing one connection pool. It returns a cleanup that closes the pool.
func setupTenancy(ctx context.Context, cfg config, reg registry.ModuleRegistry) (*auth.Service, *usecase.Service, func(), error) {
	st, err := store.NewPostgresStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := store.Migrate(cfg.DatabaseURL); err != nil {
		st.Close()
		return nil, nil, nil, fmt.Errorf("applying migrations: %w", err)
	}

	var authr auth.Authenticator
	switch cfg.AuthMode {
	case "static":
		authr = auth.NewStaticAuthenticator(cfg.DevSubject, cfg.DevGroups)
	default: // "header"
		authr = auth.NewHeaderAuthenticator(cfg.AuthSubjectHeader, cfg.AuthGroupsHeader, ",")
	}
	sessions := auth.NewService(authr, st, cfg.SessionTTL)
	sessions.SetSecureCookies(cfg.SecureCookies)

	// Per-use-case credentials: one platform service-account token for now
	// (WEAVE_GIT_TOKEN), resolved through the CredentialStore seam.
	creds := store.NewSharedCredentialStore(cfg.Token)
	factory := usecase.NewOrchestratorFactory(reg, creds)
	useCases := usecase.NewService(st, factory, cfg.BootstrapAdmins)

	return sessions, useCases, st.Close, nil
}
