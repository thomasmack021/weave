package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thomasmack021/weave/internal/auth"
	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/store"
	"github.com/thomasmack021/weave/web"
)

// nopSessionStore satisfies auth.SessionStore but is never exercised by the
// whoami path (static authenticator + no cookie resolves via headers only).
type nopSessionStore struct{}

func (nopSessionStore) UpsertUser(context.Context, string, string) (store.User, error) {
	return store.User{}, nil
}
func (nopSessionStore) CreateSession(context.Context, string, []string, time.Duration) (string, store.Session, error) {
	return "", store.Session{}, nil
}
func (nopSessionStore) ResolvePrincipal(context.Context, string) (store.Principal, error) {
	return store.Principal{}, store.ErrSessionInvalid
}
func (nopSessionStore) DeleteSession(context.Context, string) error { return nil }

// TestSessionsAreOptIn proves the session surface only exists when a Service is
// attached: without it, /api/session is not routed (served by the SPA file
// server as a 404), preserving the v1 / demo behaviour.
func TestSessionsAreOptIn(t *testing.T) {
	srv := New(web.Assets, registry.NewFakeRegistry(), &stubScaffolder{}, &stubInitializer{})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))

	if rec.Code == http.StatusOK {
		t.Errorf("without WithSessions, /api/session should not be a live endpoint (got 200)")
	}
}

// TestWithSessionsWhoami proves the wiring: WithSessions mounts /api/session and
// wraps the mux with the principal-injecting middleware, so whoami reports the
// authenticated principal end to end through the server.
func TestWithSessionsWhoami(t *testing.T) {
	svc := auth.NewService(auth.NewStaticAuthenticator("dev@acme.example", []string{"platform-admins"}), nopSessionStore{}, time.Hour)
	srv := New(web.Assets, registry.NewFakeRegistry(), &stubScaffolder{}, &stubInitializer{}).WithSessions(svc)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("whoami status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Subject string   `json:"subject"`
		Groups  []string `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding whoami body: %v", err)
	}
	if body.Subject != "dev@acme.example" {
		t.Errorf("whoami subject = %q, want %q", body.Subject, "dev@acme.example")
	}
}
