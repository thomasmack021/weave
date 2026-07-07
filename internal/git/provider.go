package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// PullRequestProvider opens a pull request on a remote Git provider (Bitbucket,
// GitHub, GitLab, …) through its REST API and returns the URL of the created
// PR. It is the seam that lets the GitOps engine request human review of a
// pushed branch. Each implementation interprets repo in its own provider's
// terms (Bitbucket Cloud/Server: "project/repo"; GitHub: "owner/repo"; GitLab:
// the project path "group/project"), documented on each constructor.
type PullRequestProvider interface {
	// CreatePullRequest opens a PR on repo from branch into baseBranch with the
	// given title and body, returning the web URL of the created pull request.
	CreatePullRequest(ctx context.Context, repo string, branch string, baseBranch string, title string, body string) (string, error)
}

// postPRJSON marshals payload as JSON, POSTs it to url with the given headers,
// and decodes a successful (2xx) response into out. Headers carry
// provider-specific auth (Bearer, PRIVATE-TOKEN, …) and any Accept override. A
// non-2xx response becomes an error carrying the status and a body snippet, so
// no caller ever mistakes a rejection for an empty URL. It is the shared HTTP
// spine of every PullRequestProvider implementation.
func postPRJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("git: encoding pull request payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("git: building pull request request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("git: creating pull request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("git: pull request API returned %s: %s", resp.Status, snippet)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("git: decoding pull request response: %w", err)
	}
	return nil
}

// HTTPProvider is a PullRequestProvider backed by the Bitbucket Cloud REST API
// over HTTP. Its dependencies (base URL, HTTP client, service-account token)
// are injected rather than read from globals, per the engagement rules.
type HTTPProvider struct {
	baseURL string
	client  *http.Client
	token   string
}

// Compile-time assertion that *HTTPProvider implements PullRequestProvider.
var _ PullRequestProvider = (*HTTPProvider)(nil)

// NewHTTPProvider constructs an HTTPProvider with its dependencies injected. A
// nil client falls back to http.DefaultClient.
func NewHTTPProvider(baseURL string, client *http.Client, token string) *HTTPProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPProvider{baseURL: baseURL, client: client, token: token}
}

// bitbucketPRRequest is the Bitbucket Cloud "create a pull request" payload.
type bitbucketPRRequest struct {
	Title       string          `json:"title"`
	Source      bitbucketBranch `json:"source"`
	Destination bitbucketBranch `json:"destination"`
	Description string          `json:"description"`
}

// bitbucketBranch is the nested {"branch": {"name": ...}} shape Bitbucket uses
// for both source and destination of a pull request.
type bitbucketBranch struct {
	Branch struct {
		Name string `json:"name"`
	} `json:"branch"`
}

// bitbucketPRResponse captures the fields we need from the created PR: the web
// URL lives under links.html.href.
type bitbucketPRResponse struct {
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

// CreatePullRequest opens a pull request via the Bitbucket Cloud REST API and
// returns the web URL of the created PR. repo is the "workspace/repo" slug.
func (p *HTTPProvider) CreatePullRequest(ctx context.Context, repo string, branch string, baseBranch string, title string, body string) (string, error) {
	reqBody := bitbucketPRRequest{Title: title, Description: body}
	reqBody.Source.Branch.Name = branch
	reqBody.Destination.Branch.Name = baseBranch

	headers := map[string]string{}
	if p.token != "" {
		headers["Authorization"] = "Bearer " + p.token
	}
	url := fmt.Sprintf("%s/2.0/repositories/%s/pullrequests", p.baseURL, repo)

	var prResp bitbucketPRResponse
	if err := postPRJSON(ctx, p.client, url, headers, reqBody, &prResp); err != nil {
		return "", err
	}
	if prResp.Links.HTML.Href == "" {
		return "", errors.New("git: pull request response missing links.html.href")
	}
	return prResp.Links.HTML.Href, nil
}
