package main

import (
	"strings"
	"testing"
	"time"
)

// envMap returns a getenv func backed by a map, so tests never touch the real
// process environment.
func envMap(m map[string]string) func(string) string {
	return func(key string) string { return m[key] }
}

// requiredEnv is a minimal complete environment: every required variable set,
// every optional one left to its default.
func requiredEnv() map[string]string {
	return map[string]string{
		"WEAVE_SPECS":          "/etc/weave/spec.yaml",
		"WEAVE_REPO_URL":       "https://bitbucket.org/acme/infra.git",
		"WEAVE_GIT_TOKEN":      "s3cret",
		"WEAVE_BITBUCKET_REPO": "acme/infra",
		"WEAVE_ENV":            "dev",
	}
}

// TestLoadConfig_DemoSkipsRequiredSettings pins the -demo contract: demo mode
// synthesizes every required setting at startup, so loadConfig must not
// reject an otherwise empty configuration.
func TestLoadConfig_DemoSkipsRequiredSettings(t *testing.T) {
	cfg, err := loadConfig([]string{"-demo"}, envMap(nil))
	if err != nil {
		t.Fatalf("loadConfig(-demo) error = %v, want nil", err)
	}
	if !cfg.Demo {
		t.Errorf("cfg.Demo = false, want true")
	}
	if cfg.Listen != ":8080" {
		t.Errorf("cfg.Listen = %q, want default %q", cfg.Listen, ":8080")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(nil, envMap(requiredEnv()))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}

	if cfg.Listen != ":8080" {
		t.Errorf("Listen = %q, want default %q", cfg.Listen, ":8080")
	}
	if cfg.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want default %q", cfg.BaseBranch, "main")
	}
	// Proposal A: the Bitbucket API base URL defaults to the public cloud API
	// but must be overridable for internal instances.
	if cfg.BitbucketAPI != "https://api.bitbucket.org" {
		t.Errorf("BitbucketAPI = %q, want default %q", cfg.BitbucketAPI, "https://api.bitbucket.org")
	}

	if cfg.Specs != "/etc/weave/spec.yaml" {
		t.Errorf("Specs = %q, want %q", cfg.Specs, "/etc/weave/spec.yaml")
	}
	if cfg.RepoURL != "https://bitbucket.org/acme/infra.git" {
		t.Errorf("RepoURL = %q, want %q", cfg.RepoURL, "https://bitbucket.org/acme/infra.git")
	}
	if cfg.Token != "s3cret" {
		t.Errorf("Token = %q, want %q", cfg.Token, "s3cret")
	}
	if cfg.RepoSlug != "acme/infra" {
		t.Errorf("RepoSlug = %q, want %q", cfg.RepoSlug, "acme/infra")
	}
	if cfg.Env != "dev" {
		t.Errorf("Env = %q, want %q", cfg.Env, "dev")
	}
}

func TestLoadConfigEnvOverridesDefaults(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_LISTEN"] = ":9999"
	env["WEAVE_BASE_BRANCH"] = "develop"
	env["WEAVE_BITBUCKET_API"] = "https://bitbucket.internal.acme.example"

	cfg, err := loadConfig(nil, envMap(env))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}

	if cfg.Listen != ":9999" {
		t.Errorf("Listen = %q, want env value %q", cfg.Listen, ":9999")
	}
	if cfg.BaseBranch != "develop" {
		t.Errorf("BaseBranch = %q, want env value %q", cfg.BaseBranch, "develop")
	}
	if cfg.BitbucketAPI != "https://bitbucket.internal.acme.example" {
		t.Errorf("BitbucketAPI = %q, want env value %q", cfg.BitbucketAPI, "https://bitbucket.internal.acme.example")
	}
}

func TestLoadConfigFlagsOverrideEnv(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_LISTEN"] = ":9999"
	env["WEAVE_BITBUCKET_API"] = "https://from-env.example"

	args := []string{
		"-listen", ":7777",
		"-pr-api", "https://from-flag.example",
		"-env", "prod",
	}
	cfg, err := loadConfig(args, envMap(env))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}

	if cfg.Listen != ":7777" {
		t.Errorf("Listen = %q, want flag value %q", cfg.Listen, ":7777")
	}
	if cfg.BitbucketAPI != "https://from-flag.example" {
		t.Errorf("BitbucketAPI = %q, want flag value %q", cfg.BitbucketAPI, "https://from-flag.example")
	}
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want flag value %q", cfg.Env, "prod")
	}
}

func TestLoadConfigMissingRequiredAccumulated(t *testing.T) {
	// Empty environment: all five required variables missing. loadConfig must
	// report them all in a single error (mirroring the validate package's
	// accumulate-then-join style), not fail one at a time.
	_, err := loadConfig(nil, envMap(nil))
	if err == nil {
		t.Fatal("loadConfig() error = nil, want error naming every missing required variable")
	}
	for _, name := range []string{
		"WEAVE_SPECS", "WEAVE_REPO_URL", "WEAVE_GIT_TOKEN", "WEAVE_BITBUCKET_REPO", "WEAVE_ENV",
	} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not mention missing variable %s", err, name)
		}
	}
}

func TestLoadConfigOneMissingRequired(t *testing.T) {
	env := requiredEnv()
	delete(env, "WEAVE_GIT_TOKEN")

	_, err := loadConfig(nil, envMap(env))
	if err == nil {
		t.Fatal("loadConfig() error = nil, want error for missing WEAVE_GIT_TOKEN")
	}
	if !strings.Contains(err.Error(), "WEAVE_GIT_TOKEN") {
		t.Errorf("error %q does not mention WEAVE_GIT_TOKEN", err)
	}
	if strings.Contains(err.Error(), "WEAVE_SPECS") {
		t.Errorf("error %q mentions WEAVE_SPECS, which was provided", err)
	}
}

func TestLoadConfigTrimsTrailingSlashFromBitbucketAPI(t *testing.T) {
	// Internal instances are often configured with a trailing slash; the
	// provider joins paths with "%s/2.0/...", so the base URL must come out
	// slash-free either way.
	env := requiredEnv()
	env["WEAVE_BITBUCKET_API"] = "https://bitbucket.internal.acme.example/"

	cfg, err := loadConfig(nil, envMap(env))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}
	if cfg.BitbucketAPI != "https://bitbucket.internal.acme.example" {
		t.Errorf("BitbucketAPI = %q, want trailing slash trimmed", cfg.BitbucketAPI)
	}
}

func TestLoadConfigBadFlag(t *testing.T) {
	_, err := loadConfig([]string{"-no-such-flag"}, envMap(requiredEnv()))
	if err == nil {
		t.Fatal("loadConfig() error = nil, want flag parse error for unknown flag")
	}
}

// TestLoadConfigDefaultPRProvider pins the default: with nothing set, the PR
// provider is Bitbucket Cloud and the API base URL is the public cloud API —
// exactly the pre-multi-provider behavior.
func TestLoadConfigDefaultPRProvider(t *testing.T) {
	cfg, err := loadConfig(nil, envMap(requiredEnv()))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}
	if cfg.PRProvider != "bitbucket-cloud" {
		t.Errorf("PRProvider = %q, want default %q", cfg.PRProvider, "bitbucket-cloud")
	}
	if cfg.BitbucketAPI != "https://api.bitbucket.org" {
		t.Errorf("BitbucketAPI = %q, want %q", cfg.BitbucketAPI, "https://api.bitbucket.org")
	}
}

// TestLoadConfigProviderAwareAPIDefault proves the API base URL default tracks
// the selected provider when the operator does not set one explicitly.
func TestLoadConfigProviderAwareAPIDefault(t *testing.T) {
	for _, tc := range []struct{ provider, wantAPI string }{
		{"github", "https://api.github.com"},
		{"gitlab", "https://gitlab.com"},
	} {
		env := requiredEnv()
		env["WEAVE_PR_PROVIDER"] = tc.provider
		cfg, err := loadConfig(nil, envMap(env))
		if err != nil {
			t.Fatalf("loadConfig(provider=%s) error = %v, want nil", tc.provider, err)
		}
		if cfg.PRProvider != tc.provider {
			t.Errorf("PRProvider = %q, want %q", cfg.PRProvider, tc.provider)
		}
		if cfg.BitbucketAPI != tc.wantAPI {
			t.Errorf("provider %s: BitbucketAPI = %q, want default %q", tc.provider, cfg.BitbucketAPI, tc.wantAPI)
		}
	}
}

// TestLoadConfigExplicitAPIBeatsProviderDefault proves an explicit base URL is
// never overridden by the provider default (e.g. a self-managed GitLab).
func TestLoadConfigExplicitAPIBeatsProviderDefault(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_PR_PROVIDER"] = "gitlab"
	env["WEAVE_PR_API"] = "https://gitlab.internal.acme.example"
	cfg, err := loadConfig(nil, envMap(env))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}
	if cfg.BitbucketAPI != "https://gitlab.internal.acme.example" {
		t.Errorf("BitbucketAPI = %q, want the explicit WEAVE_PR_API value", cfg.BitbucketAPI)
	}
}

// TestLoadConfigGenericRepoAlias proves WEAVE_PR_REPO is accepted as the
// provider-neutral alias for the repo identifier.
func TestLoadConfigGenericRepoAlias(t *testing.T) {
	env := requiredEnv()
	delete(env, "WEAVE_BITBUCKET_REPO")
	env["WEAVE_PR_REPO"] = "acme/infra"
	cfg, err := loadConfig(nil, envMap(env))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}
	if cfg.RepoSlug != "acme/infra" {
		t.Errorf("RepoSlug = %q, want %q from WEAVE_PR_REPO", cfg.RepoSlug, "acme/infra")
	}
}

// TestLoadConfigUnknownProvider rejects a provider the factory cannot build,
// at config time rather than at first request.
func TestLoadConfigUnknownProvider(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_PR_PROVIDER"] = "gerrit"
	_, err := loadConfig(nil, envMap(env))
	if err == nil {
		t.Fatal("loadConfig() error = nil, want an error for an unknown PR provider")
	}
	if !strings.Contains(err.Error(), "gerrit") {
		t.Errorf("error %q does not name the unknown provider", err)
	}
}

// TestLoadConfigBitbucketServerRequiresAPI proves the one provider with no
// public default demands an explicit base URL.
func TestLoadConfigBitbucketServerRequiresAPI(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_PR_PROVIDER"] = "bitbucket-server"
	_, err := loadConfig(nil, envMap(env))
	if err == nil {
		t.Fatal("loadConfig() error = nil, want an error for bitbucket-server without an API base URL")
	}
}

// TestLoadConfigAuthDefaultsOff pins the v1-compatible default: no auth mode,
// no database, and a parsed default session TTL.
func TestLoadConfigAuthDefaultsOff(t *testing.T) {
	cfg, err := loadConfig(nil, envMap(requiredEnv()))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}
	if cfg.AuthMode != "" {
		t.Errorf("AuthMode = %q, want empty (auth off by default)", cfg.AuthMode)
	}
	if cfg.SessionTTL != 12*time.Hour {
		t.Errorf("SessionTTL = %v, want 12h default", cfg.SessionTTL)
	}
}

// TestLoadConfigAuthModeRequiresDatabaseURL: sessions need a database.
func TestLoadConfigAuthModeRequiresDatabaseURL(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_AUTH_MODE"] = "header"
	_, err := loadConfig(nil, envMap(env))
	if err == nil || !strings.Contains(err.Error(), "WEAVE_DATABASE_URL") {
		t.Fatalf("loadConfig() error = %v, want it to require WEAVE_DATABASE_URL", err)
	}
}

// TestLoadConfigStaticAuthRequiresDevSubject: static identity needs a subject.
func TestLoadConfigStaticAuthRequiresDevSubject(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_AUTH_MODE"] = "static"
	env["WEAVE_DATABASE_URL"] = "postgres://localhost/weave"
	_, err := loadConfig(nil, envMap(env))
	if err == nil || !strings.Contains(err.Error(), "WEAVE_DEV_SUBJECT") {
		t.Fatalf("loadConfig() error = %v, want it to require WEAVE_DEV_SUBJECT", err)
	}
}

// TestLoadConfigUnknownAuthMode rejects an unsupported auth mode at config time.
func TestLoadConfigUnknownAuthMode(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_AUTH_MODE"] = "kerberos"
	env["WEAVE_DATABASE_URL"] = "postgres://localhost/weave"
	_, err := loadConfig(nil, envMap(env))
	if err == nil || !strings.Contains(err.Error(), "kerberos") {
		t.Fatalf("loadConfig() error = %v, want it to reject the unknown auth mode", err)
	}
}

// TestLoadConfigBootstrapAdmins parses the comma-separated global-admin list.
func TestLoadConfigBootstrapAdmins(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_BOOTSTRAP_ADMINS"] = "boss@acme.example, platform-admins ,"
	cfg, err := loadConfig(nil, envMap(env))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}
	if len(cfg.BootstrapAdmins) != 2 || cfg.BootstrapAdmins[0] != "boss@acme.example" || cfg.BootstrapAdmins[1] != "platform-admins" {
		t.Errorf("BootstrapAdmins = %v, want [boss@acme.example platform-admins] (trimmed, empties dropped)", cfg.BootstrapAdmins)
	}
}

// TestLoadConfigHeaderAuthDefaults: a valid header-auth config carries the
// standard proxy header names by default.
func TestLoadConfigHeaderAuthDefaults(t *testing.T) {
	env := requiredEnv()
	env["WEAVE_AUTH_MODE"] = "header"
	env["WEAVE_DATABASE_URL"] = "postgres://localhost/weave"
	cfg, err := loadConfig(nil, envMap(env))
	if err != nil {
		t.Fatalf("loadConfig() error = %v, want nil", err)
	}
	if cfg.AuthSubjectHeader != "X-Forwarded-Email" || cfg.AuthGroupsHeader != "X-Forwarded-Groups" {
		t.Errorf("header defaults = %q/%q, want X-Forwarded-Email/X-Forwarded-Groups", cfg.AuthSubjectHeader, cfg.AuthGroupsHeader)
	}
}
