package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGitLabProvider_CreatePullRequest verifies the GitLab merge-request call:
// the URL-encoded project path in the /api/v4/projects/{id}/merge_requests
// path, PRIVATE-TOKEN auth, the {source_branch, target_branch, title,
// description} payload, and that the returned URL is parsed from web_url.
func TestGitLabProvider_CreatePullRequest(t *testing.T) {
	const (
		token      = "glpat-service-token"
		repo       = "acme/platform/infra" // a subgroup path, to prove full-path encoding
		branch     = "weave/add-cloud-run-123"
		baseBranch = "main"
		title      = "Add Cloud Run module"
		body       = "Provisioned via the Weave IDP."
		wantURL    = "https://gitlab.example.com/acme/platform/infra/-/merge_requests/42"
	)

	type glPayload struct {
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Title        string `json:"title"`
		Description  string `json:"description"`
	}
	var (
		gotMethod  string
		gotRawPath string
		gotPrivTok string
		gotBody    glPayload
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		// r.URL.EscapedPath() preserves the %2F encoding of the project path.
		gotRawPath = r.URL.EscapedPath()
		gotPrivTok = r.Header.Get("PRIVATE-TOKEN")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"web_url": wantURL})
	}))
	defer srv.Close()

	provider := NewGitLabProvider(srv.URL, srv.Client(), token)

	url, err := provider.CreatePullRequest(context.Background(), repo, branch, baseBranch, title, body)
	if err != nil {
		t.Fatalf("CreatePullRequest returned unexpected error: %v", err)
	}
	if url != wantURL {
		t.Errorf("PR url = %q, want %q", url, wantURL)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("request method = %q, want POST", gotMethod)
	}
	if want := "/api/v4/projects/acme%2Fplatform%2Finfra/merge_requests"; gotRawPath != want {
		t.Errorf("request path = %q, want %q (project path must be URL-encoded)", gotRawPath, want)
	}
	if gotPrivTok != token {
		t.Errorf("PRIVATE-TOKEN = %q, want %q", gotPrivTok, token)
	}
	if gotBody.SourceBranch != branch {
		t.Errorf("payload source_branch = %q, want %q", gotBody.SourceBranch, branch)
	}
	if gotBody.TargetBranch != baseBranch {
		t.Errorf("payload target_branch = %q, want %q", gotBody.TargetBranch, baseBranch)
	}
	if gotBody.Title != title || gotBody.Description != body {
		t.Errorf("payload title/description = %q/%q, want %q/%q", gotBody.Title, gotBody.Description, title, body)
	}
}
