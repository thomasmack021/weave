package git

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// GitHubProvider opens pull requests through the GitHub REST API (v3). It works
// against github.com (baseURL https://api.github.com) and GitHub Enterprise
// Server (baseURL https://<host>/api/v3).
type GitHubProvider struct {
	baseURL string
	client  *http.Client
	token   string
}

var _ PullRequestProvider = (*GitHubProvider)(nil)

// NewGitHubProvider builds a GitHubProvider. baseURL is the API root
// (https://api.github.com, or https://<host>/api/v3 for Enterprise); a nil
// client defaults to http.DefaultClient; token is a personal or app
// installation access token. In CreatePullRequest, repo is the "owner/repo"
// pair.
func NewGitHubProvider(baseURL string, client *http.Client, token string) *GitHubProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &GitHubProvider{baseURL: baseURL, client: client, token: token}
}

// githubPRRequest is the GitHub "create a pull request" payload: head is the
// source branch, base is the target branch.
type githubPRRequest struct {
	Title string `json:"title"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Body  string `json:"body"`
}

// githubPRResponse carries the fields we consume from the created PR.
type githubPRResponse struct {
	HTMLURL string `json:"html_url"`
}

// CreatePullRequest opens a PR via the GitHub REST API and returns its html_url.
func (p *GitHubProvider) CreatePullRequest(ctx context.Context, repo string, branch string, baseBranch string, title string, body string) (string, error) {
	payload := githubPRRequest{Title: title, Head: branch, Base: baseBranch, Body: body}

	headers := map[string]string{"Accept": "application/vnd.github+json"}
	if p.token != "" {
		headers["Authorization"] = "Bearer " + p.token
	}
	url := fmt.Sprintf("%s/repos/%s/pulls", p.baseURL, repo)

	var prResp githubPRResponse
	if err := postPRJSON(ctx, p.client, url, headers, payload, &prResp); err != nil {
		return "", err
	}
	if prResp.HTMLURL == "" {
		return "", errors.New("git: pull request response missing html_url")
	}
	return prResp.HTMLURL, nil
}
