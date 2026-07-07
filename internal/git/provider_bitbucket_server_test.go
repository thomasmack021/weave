package git

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBitbucketServerProvider_CreatePullRequest verifies the Bitbucket
// Server/DC REST 1.0 call: the projects/{key}/repos/{slug}/pull-requests path,
// bearer auth, the fromRef/toRef payload (branch refs plus the repository
// coordinates DC requires), and that the URL is parsed from links.self[0].href.
func TestBitbucketServerProvider_CreatePullRequest(t *testing.T) {
	const (
		token      = "http-access-token"
		repo       = "PLAT/infra" // "<projectKey>/<repoSlug>"
		branch     = "weave/add-cloud-run-123"
		baseBranch = "main"
		title      = "Add Cloud Run module"
		body       = "Provisioned via the Weave IDP."
		wantURL    = "https://bitbucket.acme.example/projects/PLAT/repos/infra/pull-requests/42/overview"
	)

	type ref struct {
		ID         string `json:"id"`
		Repository struct {
			Slug    string `json:"slug"`
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
		} `json:"repository"`
	}
	type serverPayload struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		FromRef     ref    `json:"fromRef"`
		ToRef       ref    `json:"toRef"`
	}
	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotBody   serverPayload
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"links": map[string]any{
				"self": []map[string]any{{"href": wantURL}},
			},
		})
	}))
	defer srv.Close()

	provider := NewBitbucketServerProvider(srv.URL, srv.Client(), token)

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
	if want := "/rest/api/1.0/projects/PLAT/repos/infra/pull-requests"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer "+token)
	}
	if gotBody.Title != title || gotBody.Description != body {
		t.Errorf("payload title/description = %q/%q, want %q/%q", gotBody.Title, gotBody.Description, title, body)
	}
	// fromRef is the source branch; toRef the target — each fully qualified.
	if gotBody.FromRef.ID != "refs/heads/"+branch {
		t.Errorf("fromRef.id = %q, want %q", gotBody.FromRef.ID, "refs/heads/"+branch)
	}
	if gotBody.ToRef.ID != "refs/heads/"+baseBranch {
		t.Errorf("toRef.id = %q, want %q", gotBody.ToRef.ID, "refs/heads/"+baseBranch)
	}
	if gotBody.FromRef.Repository.Slug != "infra" || gotBody.FromRef.Repository.Project.Key != "PLAT" {
		t.Errorf("fromRef.repository = %+v, want slug infra / project PLAT", gotBody.FromRef.Repository)
	}
	if gotBody.ToRef.Repository.Slug != "infra" || gotBody.ToRef.Repository.Project.Key != "PLAT" {
		t.Errorf("toRef.repository = %+v, want slug infra / project PLAT", gotBody.ToRef.Repository)
	}
}

// TestBitbucketServerProvider_BadRepoIdentifier proves the "<projectKey>/<slug>"
// contract is enforced: a repo string without the required single slash is a
// caller error surfaced before any HTTP call.
func TestBitbucketServerProvider_BadRepoIdentifier(t *testing.T) {
	provider := NewBitbucketServerProvider("https://bitbucket.acme.example", http.DefaultClient, "t")
	for _, bad := range []string{"noslash", "too/many/slashes", "/infra", "PLAT/"} {
		if _, err := provider.CreatePullRequest(context.Background(), bad, "b", "main", "t", "b"); err == nil {
			t.Errorf("CreatePullRequest(repo=%q) error = nil, want an error for a malformed identifier", bad)
		}
	}
}
