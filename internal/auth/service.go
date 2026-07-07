package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/thomasmack021/weave/internal/store"
)

// cookieName is the session cookie the browser wizard carries.
const cookieName = "weave_session"

// SessionStore is the subset of store.Store the Service needs. *store.PostgresStore
// satisfies it; tests substitute a fake.
type SessionStore interface {
	UpsertUser(ctx context.Context, subject, email string) (store.User, error)
	CreateSession(ctx context.Context, userID string, groups []string, ttl time.Duration) (string, store.Session, error)
	ResolvePrincipal(ctx context.Context, rawToken string) (store.Principal, error)
	DeleteSession(ctx context.Context, rawToken string) error
}

// The production store must satisfy the consumer-side session interface.
var _ SessionStore = (*store.PostgresStore)(nil)

// Service establishes who is calling: it issues and verifies PostgreSQL-backed
// sessions and exposes a middleware that injects the current principal into the
// request context. It does not enforce authorization on business endpoints —
// that is a later increment.
type Service struct {
	auth   Authenticator
	store  SessionStore
	ttl    time.Duration
	secure bool
}

// NewService builds a Service from an Authenticator (the proxy-header or static
// identity source), a SessionStore, and the session lifetime.
func NewService(a Authenticator, s SessionStore, ttl time.Duration) *Service {
	return &Service{auth: a, store: s, ttl: ttl}
}

// SetSecureCookies marks issued cookies Secure (send only over HTTPS). Enable
// in production; leave off for plain-HTTP local runs.
func (s *Service) SetSecureCookies(secure bool) { s.secure = secure }

// principalCtxKey types the context value so it cannot collide with other keys.
type principalCtxKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p store.Principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

// PrincipalFrom returns the principal the middleware injected, if any.
func PrincipalFrom(ctx context.Context) (store.Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(store.Principal)
	return p, ok
}

// Middleware resolves the calling principal for each request and stores it in
// the context. Resolution order: a valid session cookie first, then the
// Authenticator (trusted proxy headers or the static identity). Anonymous
// requests pass through with no principal — downstream handlers decide whether
// that is allowed.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := s.resolve(r); ok {
			r = r.WithContext(WithPrincipal(r.Context(), p))
		}
		next.ServeHTTP(w, r)
	})
}

// resolve returns the principal for a request from its session cookie or,
// failing that, the Authenticator.
func (s *Service) resolve(r *http.Request) (store.Principal, bool) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		if p, err := s.store.ResolvePrincipal(r.Context(), c.Value); err == nil {
			return p, true
		}
	}
	return s.auth.Authenticate(r)
}

// HandleSession backs /api/session: POST logs in (issues a session), GET
// reports the current principal (whoami), DELETE logs out.
func (s *Service) HandleSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.login(w, r)
	case http.MethodGet:
		s.whoami(w, r)
	case http.MethodDelete:
		s.logout(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// login authenticates via the Authenticator, upserts the user, issues a session
// snapshotting the principal's groups, sets the cookie, and returns whoami.
func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	p, ok := s.auth.Authenticate(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	user, err := s.store.UpsertUser(r.Context(), p.Subject, p.Subject)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rawToken, sess, err := s.store.CreateSession(r.Context(), user.ID, p.Groups, s.ttl)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.Expires,
		MaxAge:   int(s.ttl.Seconds()),
	})
	writeJSON(w, http.StatusOK, principalJSON(p))
}

// whoami returns the principal the middleware resolved, or 401 if anonymous.
func (s *Service) whoami(w http.ResponseWriter, r *http.Request) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	writeJSON(w, http.StatusOK, principalJSON(p))
}

// logout revokes the session named by the cookie and clears it.
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		if err := s.store.DeleteSession(r.Context(), c.Value); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func principalJSON(p store.Principal) map[string]any {
	groups := p.Groups
	if groups == nil {
		groups = []string{}
	}
	return map[string]any{"subject": p.Subject, "groups": groups}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
