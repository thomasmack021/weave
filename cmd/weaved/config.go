package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// config carries every runtime setting for the weaved binary. Each field is
// settable by flag or WEAVE_* environment variable, with flags taking
// precedence over the environment and the environment over defaults.
type config struct {
	Listen       string // -listen / WEAVE_LISTEN, default ":8080"
	Specs        string // -specs / WEAVE_SPECS (required): path to spec.yaml
	RepoURL      string // -repo-url / WEAVE_REPO_URL (required): clone URL of the target repo
	RepoSlug     string // -pr-repo / WEAVE_PR_REPO (or legacy WEAVE_BITBUCKET_REPO) (required): the repo identifier the PR provider addresses (see PRProvider)
	BaseBranch   string // -base-branch / WEAVE_BASE_BRANCH, default "main"
	Token        string // -git-token / WEAVE_GIT_TOKEN (required): service-account token for push + PR API
	Env          string // -env / WEAVE_ENV (required): target environment, e.g. "dev"
	PRProvider   string // -pr-provider / WEAVE_PR_PROVIDER, default "bitbucket-cloud"; one of bitbucket-cloud|github|gitlab|bitbucket-server
	BitbucketAPI string // -pr-api / WEAVE_PR_API (or legacy WEAVE_BITBUCKET_API): PR API base URL. Defaulted per provider when unset; required for bitbucket-server (no public host)
	Demo         bool   // -demo: run a self-contained local demo; all required settings are synthesized

	// Identity & sessions (Gate 1 increment 2). Auth is off unless AuthMode is
	// set; when set it requires a database. All optional in v1 / demo runs.
	DatabaseURL       string        // WEAVE_DATABASE_URL: Postgres DSN for sessions/RBAC
	AuthMode          string        // WEAVE_AUTH_MODE: ""(off) | "header" | "static"
	AuthSubjectHeader string        // WEAVE_AUTH_SUBJECT_HEADER, default "X-Forwarded-Email"
	AuthGroupsHeader  string        // WEAVE_AUTH_GROUPS_HEADER, default "X-Forwarded-Groups"
	DevSubject        string        // WEAVE_DEV_SUBJECT: identity for auth-mode "static"
	DevGroups         []string      // WEAVE_DEV_GROUPS: comma-separated groups for "static"
	SessionTTL        time.Duration // WEAVE_SESSION_TTL, default 12h
	SecureCookies     bool          // WEAVE_SECURE_COOKIES: mark session cookies Secure
}

// knownAuthModes is the set of accepted WEAVE_AUTH_MODE values ("" means off).
var knownAuthModes = map[string]bool{"": true, "header": true, "static": true}

// knownPRProviders maps each selectable provider to its default API base URL.
// An empty default (bitbucket-server) means the operator must supply one — a
// Data Center install has no public host to assume. The map is the single
// source of truth for both validation and the base-URL default.
var knownPRProviders = map[string]string{
	"bitbucket-cloud":  "https://api.bitbucket.org",
	"github":           "https://api.github.com",
	"gitlab":           "https://gitlab.com",
	"bitbucket-server": "",
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
	// firstEnv prefers the provider-neutral name and falls back to the legacy
	// Bitbucket-specific one, so existing deployments keep working while new
	// ones can use the generic WEAVE_PR_* names.
	firstEnv := func(preferred, legacy, fallback string) string {
		return envOr(preferred, envOr(legacy, fallback))
	}

	var cfg config
	fs := flag.NewFlagSet("weaved", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors are returned, not printed
	fs.StringVar(&cfg.Listen, "listen", envOr("WEAVE_LISTEN", ":8080"), "address to listen on")
	fs.StringVar(&cfg.Specs, "specs", envOr("WEAVE_SPECS", ""), "path to the module spec.yaml (required)")
	fs.StringVar(&cfg.RepoURL, "repo-url", envOr("WEAVE_REPO_URL", ""), "clone URL of the target infrastructure repo (required)")
	fs.StringVar(&cfg.RepoSlug, "pr-repo", firstEnv("WEAVE_PR_REPO", "WEAVE_BITBUCKET_REPO", ""), "repo identifier the PR provider addresses (required)")
	fs.StringVar(&cfg.BaseBranch, "base-branch", envOr("WEAVE_BASE_BRANCH", "main"), "base branch to clone and target PRs at")
	fs.StringVar(&cfg.Token, "git-token", envOr("WEAVE_GIT_TOKEN", ""), "git service-account token for push and the PR API (required)")
	fs.StringVar(&cfg.Env, "env", envOr("WEAVE_ENV", ""), "target environment under terraform/env/ (required)")
	fs.StringVar(&cfg.PRProvider, "pr-provider", envOr("WEAVE_PR_PROVIDER", "bitbucket-cloud"), "PR provider: bitbucket-cloud|github|gitlab|bitbucket-server")
	fs.StringVar(&cfg.BitbucketAPI, "pr-api", firstEnv("WEAVE_PR_API", "WEAVE_BITBUCKET_API", ""), "PR API base URL; defaulted per provider when unset")
	fs.BoolVar(&cfg.Demo, "demo", false, "run a fully local, self-contained demo (no repo, token, or spec file needed)")

	fs.StringVar(&cfg.DatabaseURL, "database-url", envOr("WEAVE_DATABASE_URL", ""), "PostgreSQL DSN for sessions/RBAC (required when auth is on)")
	fs.StringVar(&cfg.AuthMode, "auth-mode", envOr("WEAVE_AUTH_MODE", ""), "identity source: \"\"(off)|header|static")
	fs.StringVar(&cfg.AuthSubjectHeader, "auth-subject-header", envOr("WEAVE_AUTH_SUBJECT_HEADER", "X-Forwarded-Email"), "trusted header carrying the caller identity")
	fs.StringVar(&cfg.AuthGroupsHeader, "auth-groups-header", envOr("WEAVE_AUTH_GROUPS_HEADER", "X-Forwarded-Groups"), "trusted header carrying the caller's IdP groups")
	fs.StringVar(&cfg.DevSubject, "dev-subject", envOr("WEAVE_DEV_SUBJECT", ""), "identity used by auth-mode static (solo dev)")
	sessionTTL := fs.String("session-ttl", envOr("WEAVE_SESSION_TTL", "12h"), "session lifetime (Go duration)")
	fs.BoolVar(&cfg.SecureCookies, "secure-cookies", envOr("WEAVE_SECURE_COOKIES", "") == "true", "mark session cookies Secure (HTTPS only)")

	if err := fs.Parse(args); err != nil {
		return config{}, fmt.Errorf("weaved: parsing flags: %w", err)
	}

	if groups := envOr("WEAVE_DEV_GROUPS", ""); groups != "" {
		for _, g := range strings.Split(groups, ",") {
			if g = strings.TrimSpace(g); g != "" {
				cfg.DevGroups = append(cfg.DevGroups, g)
			}
		}
	}
	ttl, err := time.ParseDuration(*sessionTTL)
	if err != nil {
		return config{}, fmt.Errorf("weaved: invalid WEAVE_SESSION_TTL %q: %w", *sessionTTL, err)
	}
	cfg.SessionTTL = ttl

	if !knownAuthModes[cfg.AuthMode] {
		return config{}, fmt.Errorf("weaved: unknown auth mode %q; set WEAVE_AUTH_MODE or -auth-mode to one of header, static (or leave empty to disable)", cfg.AuthMode)
	}

	// Validate the provider selection and, when the operator did not supply an
	// API base URL, default it to the provider's public host.
	defaultAPI, known := knownPRProviders[cfg.PRProvider]
	if !known {
		return config{}, fmt.Errorf("weaved: unknown PR provider %q; set WEAVE_PR_PROVIDER or -pr-provider to one of bitbucket-cloud, github, gitlab, bitbucket-server", cfg.PRProvider)
	}
	if cfg.BitbucketAPI == "" {
		cfg.BitbucketAPI = defaultAPI
	}
	// Providers join paths onto this base, so normalize away any trailing slash
	// an internal-instance URL may carry.
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
		{cfg.RepoSlug, "WEAVE_PR_REPO / WEAVE_BITBUCKET_REPO", "-pr-repo"},
		{cfg.Env, "WEAVE_ENV", "-env"},
	} {
		if req.value == "" {
			errs = append(errs, fmt.Errorf("weaved: missing required config: set %s or %s", req.env, req.flagName))
		}
	}
	// bitbucket-server is the one provider with no public host to assume, so an
	// explicit base URL is required rather than defaulted.
	if cfg.PRProvider == "bitbucket-server" && cfg.BitbucketAPI == "" {
		errs = append(errs, fmt.Errorf("weaved: missing required config: bitbucket-server needs an explicit base URL — set WEAVE_PR_API or -pr-api"))
	}
	// Identity/sessions need a database; static identity needs a subject.
	if cfg.AuthMode != "" && cfg.DatabaseURL == "" {
		errs = append(errs, fmt.Errorf("weaved: missing required config: auth needs a database — set WEAVE_DATABASE_URL or -database-url"))
	}
	if cfg.AuthMode == "static" && cfg.DevSubject == "" {
		errs = append(errs, fmt.Errorf("weaved: missing required config: auth-mode static needs an identity — set WEAVE_DEV_SUBJECT or -dev-subject"))
	}
	if len(errs) > 0 {
		return config{}, errors.Join(errs...)
	}

	return cfg, nil
}
