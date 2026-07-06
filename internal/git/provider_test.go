package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHTTPProvider_CreatePullRequest verifies the Bitbucket Cloud PR request:
// method, bearer auth, JSON payload (title, source/destination branches,
// description), and that the returned URL is parsed from links.html.href.
func TestHTTPProvider_CreatePullRequest(t *testing.T) {
	const (
		token      = "service-account-token"
		repo       = "platform/workspace-prod"
		branch     = "weave/add-cloud-run-123"
		baseBranch = "main"
		title      = "Add Cloud Run module"
		body       = "Provisioned via the Weave IDP."
		wantURL    = "https://bitbucket.example.com/platform/workspace-prod/pull-requests/42"
	)

	// branchRef mirrors Bitbucket's nested {"branch": {"name": ...}} shape.
	type branchRef struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	}
	// prPayload mirrors the Bitbucket Cloud "create pull request" request body.
	type prPayload struct {
		Title       string    `json:"title"`
		Source      branchRef `json:"source"`
		Destination branchRef `json:"destination"`
		Description string    `json:"description"`
	}

	var (
		gotMethod string
		gotAuth   string
		gotBody   prPayload
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"links": map[string]any{
				"html": map[string]any{"href": wantURL},
			},
		})
	}))
	defer srv.Close()

	provider := NewHTTPProvider(srv.URL, srv.Client(), token)

	url, err := provider.CreatePullRequest(context.Background(), repo, branch, baseBranch, title, body)
	if err != nil {
		t.Fatalf("CreatePullRequest returned unexpected error: %v", err)
	}
	if url != wantURL {
		t.Errorf("PR url = %q, want %q", url, wantURL)
	}

	// The client must POST the correct payload with bearer auth.
	if gotMethod != http.MethodPost {
		t.Errorf("request method = %q, want POST", gotMethod)
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("Authorization header = %q, want %q", gotAuth, "Bearer "+token)
	}
	if gotBody.Title != title {
		t.Errorf("payload title = %q, want %q", gotBody.Title, title)
	}
	if gotBody.Source.Branch.Name != branch {
		t.Errorf("payload source branch = %q, want %q", gotBody.Source.Branch.Name, branch)
	}
	if gotBody.Destination.Branch.Name != baseBranch {
		t.Errorf("payload destination branch = %q, want %q", gotBody.Destination.Branch.Name, baseBranch)
	}
	if gotBody.Description != body {
		t.Errorf("payload description = %q, want %q", gotBody.Description, body)
	}
}
