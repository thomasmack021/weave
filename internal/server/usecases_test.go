package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thomasmack021/weave/internal/auth"
	"github.com/thomasmack021/weave/internal/orchestrate"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/store"
	"github.com/thomasmack021/weave/internal/usecase"
	"github.com/thomasmack021/weave/web"
)

// stubUseCases is a scripted UseCaseService for testing the HTTP layer.
type stubUseCases struct {
	list         []store.UseCase
	scaffoldRes  orchestrate.Result
	scaffoldErr  error
	created      store.UseCase
	createErr    error
	addErr       error
	gotScaffold  string // key passed to Scaffold
	gotPrincipal store.Principal
}

func (s *stubUseCases) List(_ context.Context, p store.Principal) ([]store.UseCase, error) {
	s.gotPrincipal = p
	return s.list, nil
}
func (s *stubUseCases) Scaffold(_ context.Context, p store.Principal, key string, _ orchestrate.Request) (orchestrate.Result, error) {
	s.gotScaffold, s.gotPrincipal = key, p
	return s.scaffoldRes, s.scaffoldErr
}
func (s *stubUseCases) InitWorkspace(_ context.Context, _ store.Principal, _ string, _ orchestrate.InitRequest) (orchestrate.Result, error) {
	return s.scaffoldRes, s.scaffoldErr
}
func (s *stubUseCases) CreateUseCase(_ context.Context, _ store.Principal, _ store.UseCase) (store.UseCase, error) {
	return s.created, s.createErr
}
func (s *stubUseCases) AddMember(_ context.Context, _ store.Principal, _, _ string, _ store.Role) error {
	return s.addErr
}
func (s *stubUseCases) AddGroupGrant(_ context.Context, _ store.Principal, _, _ string, _ store.Role) error {
	return s.addErr
}

// tenantServer wires a server with a static authenticator (so requests are
// authenticated as subject) and the given stub dispatcher.
func tenantServer(t *testing.T, subject string, uc UseCaseService) *Server {
	t.Helper()
	svc := auth.NewService(auth.NewStaticAuthenticator(subject, nil), nopSessionStore{}, time.Hour)
	return New(web.Assets, registry.NewFakeRegistry(), &stubScaffolder{}, &stubInitializer{}).
		WithSessions(svc).WithUseCases(uc)
}

func TestUseCases_ListReturnsTenantSafeDTO(t *testing.T) {
	uc := &stubUseCases{list: []store.UseCase{{
		Key: "payments", DisplayName: "Payments", Env: "prod",
		RepoURL: "https://secret.example/repo.git", CredentialRef: "sm://secret",
	}}}
	srv := tenantServer(t, "dev@acme.example", uc)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usecases", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "payments") {
		t.Errorf("body %s missing the use-case key", body)
	}
	// The DTO must never leak the repo URL or credential reference.
	if strings.Contains(body, "secret.example") || strings.Contains(body, "sm://secret") {
		t.Errorf("use-case DTO leaked repo/credential internals: %s", body)
	}
}

// TestUseCases_AnonymousIs401 proves the use-case endpoints require identity.
func TestUseCases_AnonymousIs401(t *testing.T) {
	// Empty static subject → anonymous.
	srv := tenantServer(t, "", &stubUseCases{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usecases", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list status = %d, want 401", rec.Code)
	}
}

func TestUseCases_ScaffoldForbiddenIs403(t *testing.T) {
	uc := &stubUseCases{scaffoldErr: usecaseForbidden()}
	srv := tenantServer(t, "dev@acme.example", uc)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/usecases/payments/scaffold",
		strings.NewReader(`{"moduleType":"cloud-run","instanceName":"x"}`)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
	}
	if uc.gotScaffold != "payments" {
		t.Errorf("Scaffold key = %q, want the path key 'payments'", uc.gotScaffold)
	}
}

func TestUseCases_ScaffoldNotFoundIs404(t *testing.T) {
	srv := tenantServer(t, "dev@acme.example", &stubUseCases{scaffoldErr: usecaseNotFound()})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/usecases/ghost/scaffold",
		strings.NewReader(`{"moduleType":"cloud-run","instanceName":"x"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rec.Code, rec.Body.String())
	}
}

func TestUseCases_ScaffoldSuccessIs201(t *testing.T) {
	uc := &stubUseCases{scaffoldRes: orchestrate.Result{Changed: true, Branch: "weave/add-x", PRURL: "https://pr/1"}}
	srv := tenantServer(t, "dev@acme.example", uc)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/usecases/payments/scaffold",
		strings.NewReader(`{"moduleType":"cloud-run","instanceName":"x"}`)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["prUrl"] != "https://pr/1" {
		t.Errorf("body = %v, want the PR url", body)
	}
}

func TestUseCases_AddMemberSuccessIs204(t *testing.T) {
	srv := tenantServer(t, "admin@acme.example", &stubUseCases{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/usecases/payments/members",
		strings.NewReader(`{"subject":"new@acme.example","role":"developer"}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
}

func TestUseCases_CreateForbiddenIs403(t *testing.T) {
	srv := tenantServer(t, "dev@acme.example", &stubUseCases{createErr: usecaseForbidden()})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/usecases",
		strings.NewReader(`{"key":"new","repoUrl":"u","env":"prod"}`)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
	}
}

// TestUseCases_OptIn proves the endpoints do not exist without WithUseCases.
func TestUseCases_OptIn(t *testing.T) {
	svc := auth.NewService(auth.NewStaticAuthenticator("dev@acme.example", nil), nopSessionStore{}, time.Hour)
	srv := New(web.Assets, registry.NewFakeRegistry(), &stubScaffolder{}, &stubInitializer{}).WithSessions(svc)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usecases", nil))
	if rec.Code == http.StatusOK {
		t.Errorf("without WithUseCases, /api/usecases should not be a live endpoint (got 200)")
	}
}

// usecaseForbidden / usecaseNotFound wrap the real usecase sentinels so the
// server's errors.Is classification is exercised exactly as in production.
func usecaseForbidden() error { return fmt.Errorf("denied: %w", usecase.ErrForbidden) }
func usecaseNotFound() error  { return fmt.Errorf("missing: %w", usecase.ErrUseCaseNotFound) }
