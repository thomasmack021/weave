//go:build integration

// These tests exercise PostgresStore against a real PostgreSQL via
// testcontainers-go. They are gated behind the `integration` build tag so the
// default `go test ./...` stays fast and Docker-free. Run explicitly with:
//
//	go test -tags=integration ./internal/store/
//
// Docker must be available.
package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestStore starts a throwaway PostgreSQL, applies the migrations, and
// returns a connected PostgresStore. The container and pool are torn down via
// t.Cleanup.
func newTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	ctx := context.Background()

	pgC, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("weave"),
		postgres.WithUsername("weave"),
		postgres.WithPassword("weave"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("starting postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pgC.Terminate(ctx) })

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	if err := Migrate(dsn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// A second Migrate must be a clean no-op (idempotency).
	if err := Migrate(dsn); err != nil {
		t.Fatalf("second Migrate (want no-op): %v", err)
	}

	st, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestPostgresStore_UserAndUseCaseLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// UpsertUser creates, then is idempotent and refreshes the email.
	u1, err := st.UpsertUser(ctx, "dev@acme.example", "dev@acme.example")
	if err != nil {
		t.Fatalf("UpsertUser create: %v", err)
	}
	u2, err := st.UpsertUser(ctx, "dev@acme.example", "dev.new@acme.example")
	if err != nil {
		t.Fatalf("UpsertUser update: %v", err)
	}
	if u1.ID != u2.ID {
		t.Errorf("UpsertUser minted a new id on conflict: %q vs %q", u1.ID, u2.ID)
	}
	if u2.Email != "dev.new@acme.example" {
		t.Errorf("UpsertUser email = %q, want refreshed", u2.Email)
	}

	// CreateUseCase applies defaults and round-trips via GetUseCaseByKey.
	created, err := st.CreateUseCase(ctx, UseCase{
		Key:           "payments-prod",
		DisplayName:   "Payments (prod)",
		RepoURL:       "https://github.com/acme/payments-cd.git",
		RepoSlug:      "acme/payments-cd",
		Env:           "prod",
		CredentialRef: "sm://acme/payments",
		// PRProvider and BaseBranch omitted → defaults.
	})
	if err != nil {
		t.Fatalf("CreateUseCase: %v", err)
	}
	if created.ID == "" {
		t.Error("CreateUseCase returned empty id")
	}
	if created.PRProvider != "bitbucket-cloud" || created.BaseBranch != "main" {
		t.Errorf("defaults not applied: provider=%q base=%q", created.PRProvider, created.BaseBranch)
	}

	got, err := st.GetUseCaseByKey(ctx, "payments-prod")
	if err != nil {
		t.Fatalf("GetUseCaseByKey: %v", err)
	}
	if got != created {
		t.Errorf("GetUseCaseByKey = %+v, want %+v", got, created)
	}

	if _, err := st.GetUseCaseByKey(ctx, "does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUseCaseByKey(missing) error = %v, want ErrNotFound", err)
	}
}

func TestPostgresStore_RBAC(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	payments, err := st.CreateUseCase(ctx, UseCase{Key: "payments", RepoURL: "u", RepoSlug: "a/b", Env: "prod"})
	if err != nil {
		t.Fatalf("CreateUseCase payments: %v", err)
	}
	billing, err := st.CreateUseCase(ctx, UseCase{Key: "billing", RepoURL: "u", RepoSlug: "a/c", Env: "prod"})
	if err != nil {
		t.Fatalf("CreateUseCase billing: %v", err)
	}
	// A third use case nobody can reach — must never appear in listings.
	if _, err := st.CreateUseCase(ctx, UseCase{Key: "secret", RepoURL: "u", RepoSlug: "a/d", Env: "prod"}); err != nil {
		t.Fatalf("CreateUseCase secret: %v", err)
	}

	dev, err := st.UpsertUser(ctx, "dev@acme.example", "dev@acme.example")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	// Direct developer membership on payments.
	if err := st.AddMembership(ctx, Membership{UserID: dev.ID, UseCaseID: payments.ID, Role: RoleDeveloper}); err != nil {
		t.Fatalf("AddMembership: %v", err)
	}
	// Admin group grant on billing for the "platform-admins" group.
	if err := st.AddGroupGrant(ctx, GroupGrant{UseCaseID: billing.ID, GroupName: "platform-admins", Role: RoleAdmin}); err != nil {
		t.Fatalf("AddGroupGrant: %v", err)
	}

	principal := Principal{Subject: "dev@acme.example", Groups: []string{"platform-admins", "everyone"}}

	// EffectiveRole: developer on payments (direct), admin on billing (group).
	if role, ok, err := st.EffectiveRole(ctx, payments.ID, principal); err != nil || !ok || role != RoleDeveloper {
		t.Errorf("EffectiveRole(payments) = (%q,%v,%v), want (developer,true,nil)", role, ok, err)
	}
	if role, ok, err := st.EffectiveRole(ctx, billing.ID, principal); err != nil || !ok || role != RoleAdmin {
		t.Errorf("EffectiveRole(billing) = (%q,%v,%v), want (admin,true,nil)", role, ok, err)
	}

	// A principal not in the group and with no membership is denied on billing.
	stranger := Principal{Subject: "other@acme.example", Groups: []string{"nobody"}}
	if _, ok, err := st.EffectiveRole(ctx, billing.ID, stranger); err != nil || ok {
		t.Errorf("EffectiveRole(billing, stranger) ok = %v, want false", ok)
	}

	// ListUseCasesForPrincipal returns exactly payments + billing, ordered by key.
	list, err := st.ListUseCasesForPrincipal(ctx, principal)
	if err != nil {
		t.Fatalf("ListUseCasesForPrincipal: %v", err)
	}
	gotKeys := []string{}
	for _, uc := range list {
		gotKeys = append(gotKeys, uc.Key)
	}
	want := []string{"billing", "payments"}
	if len(gotKeys) != len(want) || gotKeys[0] != want[0] || gotKeys[1] != want[1] {
		t.Errorf("ListUseCasesForPrincipal keys = %v, want %v (no access to 'secret')", gotKeys, want)
	}

	// The stranger sees nothing.
	if list, err := st.ListUseCasesForPrincipal(ctx, stranger); err != nil || len(list) != 0 {
		t.Errorf("ListUseCasesForPrincipal(stranger) = %v (err %v), want empty", list, err)
	}
}

func TestPostgresStore_Sessions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	u, err := st.UpsertUser(ctx, "dev@acme.example", "dev@acme.example")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	groups := []string{"platform-admins", "payments-devs"}

	// Valid session round-trips, snapshotting the principal's groups.
	raw, sess, err := st.CreateSession(ctx, u.ID, groups, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if raw == "" {
		t.Fatal("CreateSession returned empty raw token")
	}
	if sess.UserID != u.ID {
		t.Errorf("session UserID = %q, want %q", sess.UserID, u.ID)
	}

	// ResolvePrincipal reconstructs the full principal (subject + groups) with
	// no proxy headers — the point of a self-contained session.
	p, err := st.ResolvePrincipal(ctx, raw)
	if err != nil {
		t.Fatalf("ResolvePrincipal: %v", err)
	}
	if p.Subject != "dev@acme.example" {
		t.Errorf("principal subject = %q, want %q", p.Subject, "dev@acme.example")
	}
	if len(p.Groups) != 2 || p.Groups[0] != "platform-admins" || p.Groups[1] != "payments-devs" {
		t.Errorf("principal groups = %v, want %v", p.Groups, groups)
	}

	// Unknown token is invalid.
	if _, err := st.ResolvePrincipal(ctx, "not-a-real-token"); !errors.Is(err, ErrSessionInvalid) {
		t.Errorf("ResolvePrincipal(unknown) error = %v, want ErrSessionInvalid", err)
	}

	// Deleting revokes it.
	if err := st.DeleteSession(ctx, raw); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := st.ResolvePrincipal(ctx, raw); !errors.Is(err, ErrSessionInvalid) {
		t.Errorf("ResolvePrincipal(deleted) error = %v, want ErrSessionInvalid", err)
	}

	// An already-expired session is invalid.
	expiredRaw, _, err := st.CreateSession(ctx, u.ID, nil, -time.Minute)
	if err != nil {
		t.Fatalf("CreateSession(expired): %v", err)
	}
	if _, err := st.ResolvePrincipal(ctx, expiredRaw); !errors.Is(err, ErrSessionInvalid) {
		t.Errorf("ResolvePrincipal(expired) error = %v, want ErrSessionInvalid", err)
	}
}
