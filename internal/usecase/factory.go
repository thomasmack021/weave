package usecase

import (
	"context"
	"fmt"

	"github.com/thomasmack021/weave/internal/git"
	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/store"
)

// The production orchestrator must satisfy the Runner surface.
var _ Runner = (*orchestrate.Orchestrator)(nil)

// OrchestratorFactory is the production RunnerFactory: it resolves a use case's
// credential, builds its PR provider, and constructs an orchestrator bound to
// the use case's repo config. The module registry is shared across tenants;
// only the target repo and credentials are per-use-case.
type OrchestratorFactory struct {
	registry registry.ModuleRegistry
	creds    store.CredentialStore
}

var _ RunnerFactory = (*OrchestratorFactory)(nil)

// NewOrchestratorFactory wires the factory with the shared module registry and
// the credential store.
func NewOrchestratorFactory(reg registry.ModuleRegistry, creds store.CredentialStore) *OrchestratorFactory {
	return &OrchestratorFactory{registry: reg, creds: creds}
}

// For builds a Runner for uc: resolve the credential, construct the provider,
// and bind an orchestrator to the use case's repo config. The provider's API
// base uses its public default (self-hosted overrides are a future column).
func (f *OrchestratorFactory) For(ctx context.Context, uc store.UseCase) (Runner, error) {
	token, err := f.creds.Resolve(ctx, uc.CredentialRef)
	if err != nil {
		return nil, fmt.Errorf("usecase: resolving credential for %q: %w", uc.Key, err)
	}
	pr, err := git.NewProvider(uc.PRProvider, "", token)
	if err != nil {
		return nil, fmt.Errorf("usecase: building PR provider for %q: %w", uc.Key, err)
	}
	return orchestrate.New(f.registry, pr, orchestrate.Config{
		RepoURL:    uc.RepoURL,
		RepoSlug:   uc.RepoSlug,
		BaseBranch: uc.BaseBranch,
		Token:      token,
		Env:        uc.Env,
	}), nil
}
