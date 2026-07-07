package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thomasmack021/weave/internal/store"
)

// fakeStore is an in-memory SessionStore for unit-testing Service without a
// database. It uses the subject as the user id for simplicity.
type fakeStore struct {
	sessions    map[string]store.Session // rawToken -> session (UserID == subject)
	n           int
	upsertCalls int
	lastGroups  []string
	deleted     []string
}

func newFakeStore() *fakeStore { return &fakeStore{sessions: map[string]store.Session{}} }

func (f *fakeStore) UpsertUser(_ context.Context, subject, _ string) (store.User, error) {
	f.upsertCalls++
	return store.User{ID: subject, Subject: subject}, nil
}

func (f *fakeStore) CreateSession(_ context.Context, userID string, groups []string, ttl time.Duration) (string, store.Session, error) {
	f.n++
	f.lastGroups = groups
	tok := fmt.Sprintf("tok-%d", f.n)
	sess := store.Session{UserID: userID, Groups: groups, Expires: time.Now().Add(ttl)}
	f.sessions[tok] = sess
	return tok, sess, nil
}

func (f *fakeStore) ResolvePrincipal(_ context.Context, rawToken string) (store.Principal, error) {
	sess, ok := f.sessions[rawToken]
	if !ok || !sess.Expires.After(time.Now()) {
		return store.Principal{}, store.ErrSessionInvalid
	}
	return store.Principal{Subject: sess.UserID, Groups: sess.Groups}, nil
}

func (f *fakeStore) DeleteSession(_ context.Context, rawToken string) error {
	f.deleted = append(f.deleted, rawToken)
	delete(f.sessions, rawToken)
	return nil
}

// echoPrincipal is a downstream handler that reports the context principal, so
// tests can assert what the middleware injected.
func echoPrincipal(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"subject": p.Subject, "groups": p.Groups})
}

func TestService_LoginIssuesSessionAndCookie(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(NewStaticAuthenticator("dev@acme.example", []string{"platform-admins"}), fs, time.Hour)

	rec := httptest.NewRecorder()
	svc.HandleSession(rec, httptest.NewRequest(http.MethodPost, "/api/session", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	if fs.upsertCalls != 1 {
		t.Errorf("UpsertUser called %d times, want 1", fs.upsertCalls)
	}
	if len(fs.lastGroups) != 1 || fs.lastGroups[0] != "platform-admins" {
		t.Errorf("session snapshotted groups %v, want [platform-admins]", fs.lastGroups)
	}
	cookie := findCookie(rec.Result().Cookies(), "weave_session")
	if cookie == nil {
		t.Fatal("login did not set the weave_session cookie")
	}
	if !cookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
}

func TestService_LoginAnonymousIs401(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(NewStaticAuthenticator("", nil), fs, time.Hour) // no identity

	rec := httptest.NewRecorder()
	svc.HandleSession(rec, httptest.NewRequest(http.MethodPost, "/api/session", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous login status = %d, want 401", rec.Code)
	}
	if len(fs.sessions) != 0 {
		t.Error("no session should be created for an anonymous login")
	}
}

// TestService_MiddlewareResolvesSessionCookie proves a request carrying a valid
// session cookie is authenticated even with no proxy headers present.
func TestService_MiddlewareResolvesSessionCookie(t *testing.T) {
	fs := newFakeStore()
	// Static authenticator returns anonymous, so only the cookie can authenticate.
	svc := NewService(NewStaticAuthenticator("", nil), fs, time.Hour)
	fs.sessions["tok-1"] = store.Session{UserID: "dev@acme.example", Groups: []string{"g1"}, Expires: time.Now().Add(time.Hour)}

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.AddCookie(&http.Cookie{Name: "weave_session", Value: "tok-1"})
	rec := httptest.NewRecorder()
	svc.Middleware(http.HandlerFunc(echoPrincipal)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (cookie should authenticate); body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Subject string `json:"subject"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Subject != "dev@acme.example" {
		t.Errorf("subject = %q, want the session's subject", got.Subject)
	}
}

// TestService_MiddlewareFallsBackToAuthenticator proves the stateless proxy
// path: no cookie, but proxy headers present → authenticated.
func TestService_MiddlewareFallsBackToAuthenticator(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(NewHeaderAuthenticator("X-Forwarded-Email", "X-Forwarded-Groups", ","), fs, time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Forwarded-Email", "dev@acme.example")
	rec := httptest.NewRecorder()
	svc.Middleware(http.HandlerFunc(echoPrincipal)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (headers should authenticate)", rec.Code)
	}
}

// TestService_MiddlewareAnonymousPassesThrough proves the middleware does not
// itself reject anonymous requests — enforcement is a downstream concern.
func TestService_MiddlewareAnonymousPassesThrough(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(NewStaticAuthenticator("", nil), fs, time.Hour)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	reached := false
	svc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if _, ok := PrincipalFrom(r.Context()); ok {
			t.Error("anonymous request should have no principal in context")
		}
	})).ServeHTTP(rec, req)

	if !reached {
		t.Error("middleware must call next even for anonymous requests")
	}
}

func TestService_Logout(t *testing.T) {
	fs := newFakeStore()
	svc := NewService(NewStaticAuthenticator("", nil), fs, time.Hour)
	fs.sessions["tok-1"] = store.Session{UserID: "dev@acme.example", Expires: time.Now().Add(time.Hour)}

	req := httptest.NewRequest(http.MethodDelete, "/api/session", nil)
	req.AddCookie(&http.Cookie{Name: "weave_session", Value: "tok-1"})
	rec := httptest.NewRecorder()
	svc.HandleSession(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rec.Code)
	}
	if len(fs.deleted) != 1 || fs.deleted[0] != "tok-1" {
		t.Errorf("deleted sessions = %v, want [tok-1]", fs.deleted)
	}
	cookie := findCookie(rec.Result().Cookies(), "weave_session")
	if cookie == nil || cookie.MaxAge >= 0 {
		t.Error("logout must clear the session cookie (MaxAge < 0)")
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}
