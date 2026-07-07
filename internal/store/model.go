// Package store is the persistence and RBAC foundation for Weave's
// multi-tenant, use-case-scoped operation (see DESIGN.md). It holds the domain
// entities, the pure authorization decision, and a Postgres-backed Store. It
// deliberately does not touch the HTTP boundary yet: identity middleware and
// endpoint enforcement are later increments.
package store

import (
	"crypto/sha256"
	"time"
)

// Role is a principal's role within a single use case. admin outranks
// developer.
type Role string

const (
	RoleDeveloper Role = "developer"
	RoleAdmin     Role = "admin"
)

// rank orders roles for AtLeast / EffectiveRole comparisons. Unknown roles rank
// below everything (0), so a corrupt value never satisfies a requirement.
func (r Role) rank() int {
	switch r {
	case RoleDeveloper:
		return 1
	case RoleAdmin:
		return 2
	default:
		return 0
	}
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r.rank() > 0 }

// AtLeast reports whether r satisfies a requirement for role need (admin
// satisfies a developer requirement, but not vice versa).
func (r Role) AtLeast(need Role) bool { return r.rank() >= need.rank() }

// User is an authenticated principal, keyed by a stable subject from the IdP
// (typically an email). Rows are created lazily on first sight.
type User struct {
	ID      string
	Subject string
	Email   string
	Created time.Time
}

// UseCase is a tenant: a target CD-pipeline repo plus the provider config and
// credential reference Weave needs to open PRs against it.
type UseCase struct {
	ID            string
	Key           string
	DisplayName   string
	RepoURL       string
	RepoSlug      string
	PRProvider    string
	BaseBranch    string
	Env           string
	CredentialRef string
	Created       time.Time
}

// Membership is a direct grant of a role to one user on one use case.
type Membership struct {
	UserID    string
	UseCaseID string
	Role      Role
}

// GroupGrant grants a role on a use case to any principal whose forwarded IdP
// groups include GroupName (the Entra path).
type GroupGrant struct {
	UseCaseID string
	GroupName string
	Role      Role
}

// Session is a PostgreSQL-backed session. Only the token hash is persisted; the
// raw token is returned once at creation and never stored.
type Session struct {
	UserID  string
	Expires time.Time
	Created time.Time
}

// Principal is who is asking: their subject and the groups the IdP asserts for
// them (forwarded by the trusted proxy).
type Principal struct {
	Subject string
	Groups  []string
}

// EffectiveRole computes a principal's role on a use case as the highest of any
// direct membership role and any group grant whose group the principal belongs
// to. ok is false when neither source grants access — the use case is then
// invisible and un-actionable for this principal.
func EffectiveRole(direct []Role, grants []GroupGrant, principalGroups []string) (role Role, ok bool) {
	best := 0
	for _, r := range direct {
		if r.rank() > best {
			best, role = r.rank(), r
		}
	}
	inGroup := make(map[string]bool, len(principalGroups))
	for _, g := range principalGroups {
		inGroup[g] = true
	}
	for _, g := range grants {
		if inGroup[g.GroupName] && g.Role.rank() > best {
			best, role = g.Role.rank(), g.Role
		}
	}
	return role, best > 0
}

// HashSessionToken returns the SHA-256 of a raw session token. The database
// stores only this hash, so a table leak never exposes usable tokens.
func HashSessionToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
