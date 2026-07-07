package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStaticAuthenticator(t *testing.T) {
	a := NewStaticAuthenticator("dev@acme.example", []string{"platform-admins"})

	p, ok := a.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil))
	if !ok {
		t.Fatal("StaticAuthenticator.Authenticate ok = false, want true")
	}
	if p.Subject != "dev@acme.example" {
		t.Errorf("subject = %q, want %q", p.Subject, "dev@acme.example")
	}
	if len(p.Groups) != 1 || p.Groups[0] != "platform-admins" {
		t.Errorf("groups = %v, want [platform-admins]", p.Groups)
	}
}

// TestStaticAuthenticator_EmptySubjectIsAnonymous guards the zero value: a
// static authenticator with no subject must not silently authenticate everyone.
func TestStaticAuthenticator_EmptySubjectIsAnonymous(t *testing.T) {
	a := NewStaticAuthenticator("", nil)
	if _, ok := a.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("empty-subject StaticAuthenticator authenticated a request, want anonymous")
	}
}

func TestHeaderAuthenticator(t *testing.T) {
	a := NewHeaderAuthenticator("X-Forwarded-Email", "X-Forwarded-Groups", ",")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Email", "  dev@acme.example ")
	req.Header.Set("X-Forwarded-Groups", "platform-admins, payments-devs ,,")

	p, ok := a.Authenticate(req)
	if !ok {
		t.Fatal("HeaderAuthenticator.Authenticate ok = false, want true")
	}
	if p.Subject != "dev@acme.example" {
		t.Errorf("subject = %q, want trimmed %q", p.Subject, "dev@acme.example")
	}
	want := []string{"platform-admins", "payments-devs"}
	if len(p.Groups) != len(want) || p.Groups[0] != want[0] || p.Groups[1] != want[1] {
		t.Errorf("groups = %v, want %v (trimmed, empties dropped)", p.Groups, want)
	}
}

// TestHeaderAuthenticator_NoSubjectHeaderIsAnonymous proves a request without
// the trusted identity header is anonymous, never a blank-subject principal.
func TestHeaderAuthenticator_NoSubjectHeaderIsAnonymous(t *testing.T) {
	a := NewHeaderAuthenticator("X-Forwarded-Email", "X-Forwarded-Groups", ",")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Groups", "platform-admins") // groups but no subject

	if _, ok := a.Authenticate(req); ok {
		t.Error("request without the subject header authenticated, want anonymous")
	}
}

// TestHeaderAuthenticator_DefaultSeparator proves an empty separator falls back
// to a comma rather than treating the whole value as one group.
func TestHeaderAuthenticator_DefaultSeparator(t *testing.T) {
	a := NewHeaderAuthenticator("X-Forwarded-Email", "X-Forwarded-Groups", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-Email", "dev@acme.example")
	req.Header.Set("X-Forwarded-Groups", "a,b")

	p, _ := a.Authenticate(req)
	if len(p.Groups) != 2 {
		t.Errorf("groups = %v, want the value split on the default comma", p.Groups)
	}
}
