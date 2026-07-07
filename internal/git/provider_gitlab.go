package git

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// GitLabProvider opens merge requests through the GitLab REST API (v4). It
// works against gitlab.com (baseURL https://gitlab.com) and self-managed
// instances (baseURL https://<host>).
type GitLabProvider struct {
	baseURL string
	client  *http.Client
	token   string
}

var _ PullRequestProvider = (*GitLabProvider)(nil)

// NewGitLabProvider builds a GitLabProvider. baseURL is the instance root
// (https://gitlab.com or https://<host>); a nil client defaults to
// http.DefaultClient; token is a personal/project/group access token sent as
// PRIVATE-TOKEN. In CreatePullRequest, repo is the full project path
// ("group/project", subgroups allowed), URL-encoded into the projects endpoint.
func NewGitLabProvider(baseURL string, client *http.Client, token string) *GitLabProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &GitLabProvider{baseURL: baseURL, client: client, token: token}
}

// gitlabMRRequest is the GitLab "create merge request" payload. GitLab calls a
// PR a merge request, but the interface's semantics are identical.
type gitlabMRRequest struct {
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Title        string `json:"title"`
	Description  string `json:"description"`
}

// gitlabMRResponse carries the fields we consume from the created MR.
type gitlabMRResponse struct {
	WebURL string `json:"web_url"`
}

// CreatePullRequest opens a merge request via the GitLab REST API and returns
// its web_url.
func (p *GitLabProvider) CreatePullRequest(ctx context.Context, repo string, branch string, baseBranch string, title string, body string) (string, error) {
	payload := gitlabMRRequest{
		SourceBranch: branch,
		TargetBranch: baseBranch,
		Title:        title,
		Description:  body,
	}

	headers := map[string]string{}
	if p.token != "" {
		headers["PRIVATE-TOKEN"] = p.token
	}
	// The project path is a single, URL-encoded path segment: slashes become
	// %2F so a subgroup path like "group/sub/project" addresses one project.
	apiURL := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests", p.baseURL, url.QueryEscape(repo))

	var mrResp gitlabMRResponse
	if err := postPRJSON(ctx, p.client, apiURL, headers, payload, &mrResp); err != nil {
		return "", err
	}
	if mrResp.WebURL == "" {
		return "", errors.New("git: merge request response missing web_url")
	}
	return mrResp.WebURL, nil
}
