package git

import "testing"

func TestNewProvider(t *testing.T) {
	tests := []struct {
		provider string
		wantType string
	}{
		{"bitbucket-cloud", "*git.HTTPProvider"},
		{"github", "*git.GitHubProvider"},
		{"gitlab", "*git.GitLabProvider"},
		{"bitbucket-server", "*git.BitbucketServerProvider"},
	}
	for _, tc := range tests {
		// bitbucket-server has no default base, so supply one.
		base := ""
		if tc.provider == "bitbucket-server" {
			base = "https://bitbucket.acme.example"
		}
		p, err := NewProvider(tc.provider, base, "token")
		if err != nil {
			t.Errorf("NewProvider(%q) error = %v, want nil", tc.provider, err)
			continue
		}
		if got := typeName(p); got != tc.wantType {
			t.Errorf("NewProvider(%q) type = %s, want %s", tc.provider, got, tc.wantType)
		}
	}
}

// TestNewProvider_UnknownIsError rejects an unsupported provider name.
func TestNewProvider_UnknownIsError(t *testing.T) {
	if _, err := NewProvider("gerrit", "", "t"); err == nil {
		t.Error("NewProvider(gerrit) error = nil, want an error")
	}
}

// TestNewProvider_BitbucketServerRequiresBase proves the one provider with no
// public host demands an explicit base URL.
func TestNewProvider_BitbucketServerRequiresBase(t *testing.T) {
	if _, err := NewProvider("bitbucket-server", "", "t"); err == nil {
		t.Error("NewProvider(bitbucket-server, no base) error = nil, want an error")
	}
}

// TestDefaultAPIBase returns the public host for cloud providers and an empty
// string for bitbucket-server.
func TestDefaultAPIBase(t *testing.T) {
	for provider, want := range map[string]string{
		"bitbucket-cloud":  "https://api.bitbucket.org",
		"github":           "https://api.github.com",
		"gitlab":           "https://gitlab.com",
		"bitbucket-server": "",
	} {
		got, ok := DefaultAPIBase(provider)
		if !ok {
			t.Errorf("DefaultAPIBase(%q) ok = false, want known", provider)
		}
		if got != want {
			t.Errorf("DefaultAPIBase(%q) = %q, want %q", provider, got, want)
		}
	}
	if _, ok := DefaultAPIBase("gerrit"); ok {
		t.Error("DefaultAPIBase(gerrit) ok = true, want false for unknown provider")
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *HTTPProvider:
		return "*git.HTTPProvider"
	case *GitHubProvider:
		return "*git.GitHubProvider"
	case *GitLabProvider:
		return "*git.GitLabProvider"
	case *BitbucketServerProvider:
		return "*git.BitbucketServerProvider"
	default:
		return "unknown"
	}
}
