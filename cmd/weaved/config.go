package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// config carries every runtime setting for the weaved binary. Each field is
// settable by flag or WEAVE_* environment variable, with flags taking
// precedence over the environment and the environment over defaults.
type config struct {
	Listen       string // -listen / WEAVE_LISTEN, default ":8080"
	Specs        string // -specs / WEAVE_SPECS (required): path to spec.yaml
	RepoURL      string // -repo-url / WEAVE_REPO_URL (required): clone URL of the target repo
	RepoSlug     string // -bitbucket-repo / WEAVE_BITBUCKET_REPO (required): workspace/repo slug for the PR API
	BaseBranch   string // -base-branch / WEAVE_BASE_BRANCH, default "main"
	Token        string // -git-token / WEAVE_GIT_TOKEN (required): service-account token for push + PR API
	Env          string // -env / WEAVE_ENV (required): target environment, e.g. "dev"
	BitbucketAPI string // -bitbucket-api / WEAVE_BITBUCKET_API, default "https://api.bitbucket.org"; point at an internal instance or test stub to override
	Demo         bool   // -demo: run a self-contained local demo; all required settings are synthesized
}

// loadConfig resolves the weaved configuration from flags and the injected
// environment lookup, with precedence flag > env > default. It is pure — the
// caller passes os.Args[1:] and os.Getenv in production — so it is testable
// without touching the process environment. Missing required settings are
// accumulated into a single error naming every gap.
func loadConfig(args []string, getenv func(string) string) (config, error) {
	// envOr makes the environment the flag default, which is what gives
	// flags precedence over env: an unset flag keeps the env-derived default.
	envOr := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}

	var cfg config
	fs := flag.NewFlagSet("weaved", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors are returned, not printed
	fs.StringVar(&cfg.Listen, "listen", envOr("WEAVE_LISTEN", ":8080"), "address to listen on")
	fs.StringVar(&cfg.Specs, "specs", envOr("WEAVE_SPECS", ""), "path to the module spec.yaml (required)")
	fs.StringVar(&cfg.RepoURL, "repo-url", envOr("WEAVE_REPO_URL", ""), "clone URL of the target infrastructure repo (required)")
	fs.StringVar(&cfg.RepoSlug, "bitbucket-repo", envOr("WEAVE_BITBUCKET_REPO", ""), "Bitbucket workspace/repo slug for pull requests (required)")
	fs.StringVar(&cfg.BaseBranch, "base-branch", envOr("WEAVE_BASE_BRANCH", "main"), "base branch to clone and target PRs at")
	fs.StringVar(&cfg.Token, "git-token", envOr("WEAVE_GIT_TOKEN", ""), "git service-account token for push and the PR API (required)")
	fs.StringVar(&cfg.Env, "env", envOr("WEAVE_ENV", ""), "target environment under terraform/env/ (required)")
	fs.StringVar(&cfg.BitbucketAPI, "bitbucket-api", envOr("WEAVE_BITBUCKET_API", "https://api.bitbucket.org"), "Bitbucket API base URL; override for an internal instance")
	fs.BoolVar(&cfg.Demo, "demo", false, "run a fully local, self-contained demo (no repo, token, or spec file needed)")

	if err := fs.Parse(args); err != nil {
		return config{}, fmt.Errorf("weaved: parsing flags: %w", err)
	}

	// The PR provider joins paths as base+"/2.0/...", so normalize away any
	// trailing slash an internal-instance URL may carry.
	cfg.BitbucketAPI = strings.TrimRight(cfg.BitbucketAPI, "/")

	// Demo mode synthesizes every required setting at startup, so none of the
	// checks below apply.
	if cfg.Demo {
		return cfg, nil
	}

	// Accumulate every missing required setting into one error (mirroring the
	// validate package), so a misconfigured deployment is fixed in one pass.
	var errs []error
	for _, req := range []struct{ value, env, flagName string }{
		{cfg.Specs, "WEAVE_SPECS", "-specs"},
		{cfg.RepoURL, "WEAVE_REPO_URL", "-repo-url"},
		{cfg.Token, "WEAVE_GIT_TOKEN", "-git-token"},
		{cfg.RepoSlug, "WEAVE_BITBUCKET_REPO", "-bitbucket-repo"},
		{cfg.Env, "WEAVE_ENV", "-env"},
	} {
		if req.value == "" {
			errs = append(errs, fmt.Errorf("weaved: missing required config: set %s or %s", req.env, req.flagName))
		}
	}
	if len(errs) > 0 {
		return config{}, errors.Join(errs...)
	}

	return cfg, nil
}
