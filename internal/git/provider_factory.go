package git

import (
	"fmt"
	"net/http"
	"strings"
)

// providerDefaults maps each selectable PR provider to its default API base
// URL. An empty default (bitbucket-server) means the caller must supply one —
// a Data Center install has no public host to assume. This is the single
// source of truth for provider validation and base-URL defaulting, consumed by
// both cmd/weaved config and the per-use-case runner.
var providerDefaults = map[string]string{
	"bitbucket-cloud":  "https://api.bitbucket.org",
	"github":           "https://api.github.com",
	"gitlab":           "https://gitlab.com",
	"bitbucket-server": "",
}

// DefaultAPIBase returns the default API base URL for a provider and whether
// the provider is known.
func DefaultAPIBase(provider string) (string, bool) {
	base, ok := providerDefaults[provider]
	return base, ok
}

// NewProvider builds the PullRequestProvider for a provider name. apiBase
// overrides the provider default when non-empty (self-hosted instances); a nil
// HTTP client is used by each provider's constructor. bitbucket-server has no
// public default, so apiBase is required for it. An unknown provider is an
// error.
func NewProvider(provider, apiBase, token string) (PullRequestProvider, error) {
	def, known := providerDefaults[provider]
	if !known {
		return nil, fmt.Errorf("git: unknown PR provider %q", provider)
	}
	base := strings.TrimRight(apiBase, "/")
	if base == "" {
		base = def
	}
	if base == "" {
		return nil, fmt.Errorf("git: provider %q requires an explicit API base URL", provider)
	}

	var client *http.Client // nil → each constructor defaults to http.DefaultClient
	switch provider {
	case "bitbucket-cloud":
		return NewHTTPProvider(base, client, token), nil
	case "github":
		return NewGitHubProvider(base, client, token), nil
	case "gitlab":
		return NewGitLabProvider(base, client, token), nil
	case "bitbucket-server":
		return NewBitbucketServerProvider(base, client, token), nil
	default:
		return nil, fmt.Errorf("git: unknown PR provider %q", provider)
	}
}
