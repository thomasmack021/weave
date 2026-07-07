package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGitHubProvider_CreatePullRequest verifies the GitHub REST v3 pulls call:
// the {owner}/{repo} path, bearer auth, the {title, head, base, body} payload,
// and that the returned URL is parsed from html_url.
func TestGitHubProvider_CreatePullRequest(t *testing.T) {
	const (
		token      = "ghp_service_token"
		repo       = "acme/infra"
		branch     = "weave/add-cloud-run-123"
		baseBranch = "main"
		title      = "Add Cloud Run module"
		body       = "Provisioned via the Weave IDP."
		wantURL    = "https://github.com/acme/infra/pull/42"
	)

	type ghPayload struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotAccept string
		gotBody   ghPayload
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": wantURL})
	}))
	defer srv.Close()

	provider := NewGitHubProvider(srv.URL, srv.Client(), token)

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
	if want := "/repos/acme/infra/pulls"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer "+token)
	}
	if !strings.Contains(gotAccept, "github") {
		t.Errorf("Accept = %q, want it to request the GitHub media type", gotAccept)
	}
	if gotBody.Title != title || gotBody.Body != body {
		t.Errorf("payload title/body = %q/%q, want %q/%q", gotBody.Title, gotBody.Body, title, body)
	}
	if gotBody.Head != branch {
		t.Errorf("payload head = %q, want %q", gotBody.Head, branch)
	}
	if gotBody.Base != baseBranch {
		t.Errorf("payload base = %q, want %q", gotBody.Base, baseBranch)
	}
}

// TestGitHubProvider_APIErrorSurfacesStatus proves a non-2xx response becomes
// an error carrying the status and a body snippet, not a silent empty URL.
func TestGitHubProvider_APIErrorSurfacesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"A pull request already exists"}`))
	}))
	defer srv.Close()

	provider := NewGitHubProvider(srv.URL, srv.Client(), "t")
	_, err := provider.CreatePullRequest(context.Background(), "acme/infra", "b", "main", "t", "b")
	if err == nil {
		t.Fatal("CreatePullRequest error = nil, want an error for a 422 response")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want it to include the API body snippet", err)
	}
}
