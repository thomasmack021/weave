// Package usecase makes Weave multi-tenant. It resolves the target repo
// configuration and credentials for a named use case from the store, enforces
// the caller's RBAC role, and then dispatches to a per-use-case orchestrator.
// It is the layer that turns one central Weave instance into a service for
// many customer environments (see DESIGN.md §8 increment 3).
//
// Fail-before-mutate extends here: the RBAC check completes before any
// orchestrator (and therefore any git clone) is built.
package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/store"
)

// ErrUseCaseNotFound is returned when the named use case does not exist.
var ErrUseCaseNotFound = errors.New("usecase: not found")

// ErrForbidden is returned when the principal lacks the role required for an
// action on a use case.
var ErrForbidden = errors.New("usecase: forbidden")

// Store is the subset of store.Store the Service needs. *store.PostgresStore
// satisfies it.
type Store interface {
	GetUseCaseByKey(ctx context.Context, key string) (store.UseCase, error)
	EffectiveRole(ctx context.Context, useCaseID string, p store.Principal) (store.Role, bool, error)
	ListUseCasesForPrincipal(ctx context.Context, p store.Principal) ([]store.UseCase, error)
	CreateUseCase(ctx context.Context, uc store.UseCase) (store.UseCase, error)
	UpsertUser(ctx context.Context, subject, email string) (store.User, error)
	AddMembership(ctx context.Context, m store.Membership) error
	AddGroupGrant(ctx context.Context, g store.GroupGrant) error
}

// Runner is the per-use-case orchestrator surface. *orchestrate.Orchestrator
// satisfies it.
type Runner interface {
	Run(ctx context.Context, req orchestrate.Request) (orchestrate.Result, error)
	InitWorkspace(ctx context.Context, req orchestrate.InitRequest) (orchestrate.Result, error)
}

// RunnerFactory builds a Runner bound to one use case's repo config,
// credentials, and PR provider. The production factory resolves the credential
// and constructs a real orchestrator; tests substitute a fake.
type RunnerFactory interface {
	For(ctx context.Context, uc store.UseCase) (Runner, error)
}

// Service enforces tenancy: resolve a use case, check the caller's role, then
// dispatch to a use-case-bound Runner.
type Service struct {
	store   Store
	factory RunnerFactory
	// globalAdmins are subjects or group names treated as admin on every use
	// case; they also bootstrap the first use case (which no one can yet be an
	// admin of).
	globalAdmins map[string]bool
}

// NewService builds a Service. globalAdmins is the bootstrap-admin list
// (subjects and/or IdP group names).
func NewService(s Store, f RunnerFactory, globalAdmins []string) *Service {
	admins := make(map[string]bool, len(globalAdmins))
	for _, a := range globalAdmins {
		if a != "" {
			admins[a] = true
		}
	}
	return &Service{store: s, factory: f, globalAdmins: admins}
}

// List returns the use cases the principal may act on (RBAC-filtered).
func (s *Service) List(ctx context.Context, p store.Principal) ([]store.UseCase, error) {
	return s.store.ListUseCasesForPrincipal(ctx, p)
}

// Scaffold performs a Day-2 resource addition on a use case; requires developer.
func (s *Service) Scaffold(ctx context.Context, p store.Principal, key string, req orchestrate.Request) (orchestrate.Result, error) {
	uc, err := s.authorize(ctx, p, key, store.RoleDeveloper)
	if err != nil {
		return orchestrate.Result{}, err
	}
	runner, err := s.factory.For(ctx, uc)
	if err != nil {
		return orchestrate.Result{}, fmt.Errorf("usecase: building runner for %q: %w", key, err)
	}
	return runner.Run(ctx, req)
}

// InitWorkspace performs Day-1 workspace bootstrap on a use case; requires
// developer (operating a use case you belong to).
func (s *Service) InitWorkspace(ctx context.Context, p store.Principal, key string, req orchestrate.InitRequest) (orchestrate.Result, error) {
	uc, err := s.authorize(ctx, p, key, store.RoleDeveloper)
	if err != nil {
		return orchestrate.Result{}, err
	}
	runner, err := s.factory.For(ctx, uc)
	if err != nil {
		return orchestrate.Result{}, fmt.Errorf("usecase: building runner for %q: %w", key, err)
	}
	return runner.InitWorkspace(ctx, req)
}

// CreateUseCase onboards a new use case; requires a global (bootstrap) admin,
// since no one can yet be an admin of a use case that does not exist.
func (s *Service) CreateUseCase(ctx context.Context, p store.Principal, uc store.UseCase) (store.UseCase, error) {
	if !s.isGlobalAdmin(p) {
		return store.UseCase{}, fmt.Errorf("usecase: create requires a global admin: %w", ErrForbidden)
	}
	return s.store.CreateUseCase(ctx, uc)
}

// AddMember grants a subject a role on a use case; requires admin on that use
// case (or a global admin).
func (s *Service) AddMember(ctx context.Context, p store.Principal, key, subject string, role store.Role) error {
	uc, err := s.authorize(ctx, p, key, store.RoleAdmin)
	if err != nil {
		return err
	}
	if !role.Valid() {
		return fmt.Errorf("usecase: invalid role %q", role)
	}
	user, err := s.store.UpsertUser(ctx, subject, subject)
	if err != nil {
		return err
	}
	return s.store.AddMembership(ctx, store.Membership{UserID: user.ID, UseCaseID: uc.ID, Role: role})
}

// AddGroupGrant grants an IdP group a role on a use case; requires admin on
// that use case (or a global admin).
func (s *Service) AddGroupGrant(ctx context.Context, p store.Principal, key, group string, role store.Role) error {
	uc, err := s.authorize(ctx, p, key, store.RoleAdmin)
	if err != nil {
		return err
	}
	if !role.Valid() {
		return fmt.Errorf("usecase: invalid role %q", role)
	}
	return s.store.AddGroupGrant(ctx, store.GroupGrant{UseCaseID: uc.ID, GroupName: group, Role: role})
}

// authorize resolves the use case and verifies the principal holds at least
// need on it (global admins always pass). It returns ErrUseCaseNotFound or
// ErrForbidden, so callers never orchestrate for an unauthorized request.
func (s *Service) authorize(ctx context.Context, p store.Principal, key string, need store.Role) (store.UseCase, error) {
	uc, err := s.store.GetUseCaseByKey(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return store.UseCase{}, fmt.Errorf("usecase %q: %w", key, ErrUseCaseNotFound)
	}
	if err != nil {
		return store.UseCase{}, fmt.Errorf("usecase: resolving %q: %w", key, err)
	}
	if s.isGlobalAdmin(p) {
		return uc, nil
	}
	role, ok, err := s.store.EffectiveRole(ctx, uc.ID, p)
	if err != nil {
		return store.UseCase{}, fmt.Errorf("usecase: resolving role on %q: %w", key, err)
	}
	if !ok || !role.AtLeast(need) {
		return store.UseCase{}, fmt.Errorf("usecase %q requires %s: %w", key, need, ErrForbidden)
	}
	return uc, nil
}

// isGlobalAdmin reports whether the principal's subject or any of its groups is
// in the bootstrap-admin list.
func (s *Service) isGlobalAdmin(p store.Principal) bool {
	if s.globalAdmins[p.Subject] {
		return true
	}
	for _, g := range p.Groups {
		if s.globalAdmins[g] {
			return true
		}
	}
	return false
}
