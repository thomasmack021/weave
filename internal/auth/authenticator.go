// Package auth resolves the calling principal for an HTTP request and manages
// PostgreSQL-backed sessions. It is the seam between the transport (trusted
// proxy headers or a session cookie) and the RBAC model in internal/store.
//
// This package does not enforce authorization on the business endpoints — it
// only establishes *who* is calling. Enforcement (which use cases a principal
// may act on) is a later increment.
package auth

import (
	"net/http"
	"strings"

	"github.com/thomasmack021/weave/internal/store"
)

// Authenticator resolves the calling principal from a request. ok is false when
// the request carries no identity (anonymous).
type Authenticator interface {
	Authenticate(r *http.Request) (principal store.Principal, ok bool)
}

// StaticAuthenticator always returns a single configured principal. It backs
// the solo-developer / dev mode where no SSO proxy is present.
type StaticAuthenticator struct {
	principal store.Principal
}

// NewStaticAuthenticator builds a StaticAuthenticator for the given subject and
// groups. A blank subject yields an authenticator that treats every request as
// anonymous (so the zero value never silently authenticates everyone).
func NewStaticAuthenticator(subject string, groups []string) StaticAuthenticator {
	return StaticAuthenticator{principal: store.Principal{Subject: subject, Groups: groups}}
}

// Authenticate returns the configured principal, or anonymous if no subject was
// configured.
func (a StaticAuthenticator) Authenticate(*http.Request) (store.Principal, bool) {
	if a.principal.Subject == "" {
		return store.Principal{}, false
	}
	return a.principal, true
}

// HeaderAuthenticator reads the caller's identity and groups from trusted
// request headers set by an upstream SSO proxy (oauth2-proxy, Azure App Proxy,
// an ingress doing OIDC against Entra ID). Weave trusts these by network
// placement and never handles credentials itself.
type HeaderAuthenticator struct {
	subjectHeader string
	groupsHeader  string
	groupsSep     string
}

// NewHeaderAuthenticator builds a HeaderAuthenticator. subjectHeader is the
// header carrying the verified identity (e.g. "X-Forwarded-Email");
// groupsHeader (optional) carries the IdP groups; groupsSep splits the groups
// value and defaults to a comma when empty.
func NewHeaderAuthenticator(subjectHeader, groupsHeader, groupsSep string) HeaderAuthenticator {
	if groupsSep == "" {
		groupsSep = ","
	}
	return HeaderAuthenticator{subjectHeader: subjectHeader, groupsHeader: groupsHeader, groupsSep: groupsSep}
}

// Authenticate reads the principal from the trusted headers. A request without
// the subject header is anonymous, even if it carries groups.
func (a HeaderAuthenticator) Authenticate(r *http.Request) (store.Principal, bool) {
	subject := strings.TrimSpace(r.Header.Get(a.subjectHeader))
	if subject == "" {
		return store.Principal{}, false
	}
	var groups []string
	if a.groupsHeader != "" {
		for _, g := range strings.Split(r.Header.Get(a.groupsHeader), a.groupsSep) {
			if g = strings.TrimSpace(g); g != "" {
				groups = append(groups, g)
			}
		}
	}
	return store.Principal{Subject: subject, Groups: groups}, true
}
