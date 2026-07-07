package store

import "testing"

// TestEffectiveRole covers the hybrid RBAC decision: the effective role is the
// highest of any direct membership and any group grant matching the
// principal's forwarded groups; no match means no access.
func TestEffectiveRole(t *testing.T) {
	tests := []struct {
		name      string
		direct    []Role
		grants    []GroupGrant
		groups    []string
		wantRole  Role
		wantAllow bool
	}{
		{
			name:      "no membership and no matching grant denies",
			direct:    nil,
			grants:    []GroupGrant{{GroupName: "platform-admins", Role: RoleAdmin}},
			groups:    []string{"some-other-group"},
			wantAllow: false,
		},
		{
			name:      "direct developer membership grants developer",
			direct:    []Role{RoleDeveloper},
			groups:    nil,
			wantRole:  RoleDeveloper,
			wantAllow: true,
		},
		{
			name:      "matching admin group grant grants admin",
			grants:    []GroupGrant{{GroupName: "platform-admins", Role: RoleAdmin}},
			groups:    []string{"platform-admins", "everyone"},
			wantRole:  RoleAdmin,
			wantAllow: true,
		},
		{
			name:      "highest of direct and group wins (group admin beats direct developer)",
			direct:    []Role{RoleDeveloper},
			grants:    []GroupGrant{{GroupName: "platform-admins", Role: RoleAdmin}},
			groups:    []string{"platform-admins"},
			wantRole:  RoleAdmin,
			wantAllow: true,
		},
		{
			name:      "highest of direct and group wins (direct admin beats group developer)",
			direct:    []Role{RoleAdmin},
			grants:    []GroupGrant{{GroupName: "devs", Role: RoleDeveloper}},
			groups:    []string{"devs"},
			wantRole:  RoleAdmin,
			wantAllow: true,
		},
		{
			name:      "non-matching group grant is ignored",
			grants:    []GroupGrant{{GroupName: "devs", Role: RoleDeveloper}},
			groups:    []string{"not-devs"},
			wantAllow: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			role, allow := EffectiveRole(tc.direct, tc.grants, tc.groups)
			if allow != tc.wantAllow {
				t.Fatalf("EffectiveRole allow = %v, want %v", allow, tc.wantAllow)
			}
			if allow && role != tc.wantRole {
				t.Errorf("EffectiveRole role = %q, want %q", role, tc.wantRole)
			}
		})
	}
}

// TestRoleAtLeast pins the role ordering used by enforcement (admin outranks
// developer; a role is always at least itself).
func TestRoleAtLeast(t *testing.T) {
	if !RoleAdmin.AtLeast(RoleDeveloper) {
		t.Error("admin should satisfy a developer requirement")
	}
	if !RoleAdmin.AtLeast(RoleAdmin) {
		t.Error("admin should satisfy an admin requirement")
	}
	if RoleDeveloper.AtLeast(RoleAdmin) {
		t.Error("developer must NOT satisfy an admin requirement")
	}
	if !RoleDeveloper.AtLeast(RoleDeveloper) {
		t.Error("developer should satisfy a developer requirement")
	}
}

// TestValidRole rejects unknown role strings so a corrupt DB row or bad input
// cannot silently grant access.
func TestValidRole(t *testing.T) {
	for _, r := range []Role{RoleDeveloper, RoleAdmin} {
		if !r.Valid() {
			t.Errorf("%q should be a valid role", r)
		}
	}
	for _, bad := range []Role{"", "superuser", "Admin", "owner"} {
		if bad.Valid() {
			t.Errorf("%q should NOT be a valid role", bad)
		}
	}
}

// TestHashSessionToken is deterministic and never returns the raw token, so a
// database leak does not expose usable session tokens.
func TestHashSessionToken(t *testing.T) {
	const raw = "opaque-session-token-abc123"
	h1 := HashSessionToken(raw)
	h2 := HashSessionToken(raw)
	if string(h1) != string(h2) {
		t.Error("HashSessionToken must be deterministic")
	}
	if len(h1) != 32 {
		t.Errorf("HashSessionToken length = %d, want 32 (sha256)", len(h1))
	}
	if string(h1) == raw {
		t.Error("HashSessionToken must not return the raw token")
	}
	if string(HashSessionToken("different")) == string(h1) {
		t.Error("different tokens must hash differently")
	}
}
