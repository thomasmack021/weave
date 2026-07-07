package git

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// BitbucketServerProvider opens pull requests through the Bitbucket Server /
// Data Center REST API (1.0), whose shape differs from Bitbucket Cloud's: a
// projects/{key}/repos/{slug} path and fromRef/toRef branch coordinates rather
// than the Cloud source/destination shape.
type BitbucketServerProvider struct {
	baseURL string
	client  *http.Client
	token   string
}

var _ PullRequestProvider = (*BitbucketServerProvider)(nil)

// NewBitbucketServerProvider builds a BitbucketServerProvider. baseURL is the
// instance root (https://<host>, or with a context path such as
// https://<host>/bitbucket); a nil client defaults to http.DefaultClient;
// token is an HTTP access token sent as a bearer. In CreatePullRequest, repo is
// "<projectKey>/<repoSlug>".
func NewBitbucketServerProvider(baseURL string, client *http.Client, token string) *BitbucketServerProvider {
	if client == nil {
		client = http.DefaultClient
	}
	return &BitbucketServerProvider{baseURL: baseURL, client: client, token: token}
}

// serverRef is a Bitbucket Server fromRef/toRef: a fully-qualified branch ref
// plus the repository it lives in (Data Center requires the coordinates on
// both refs, even for a same-repo PR).
type serverRef struct {
	ID         string           `json:"id"`
	Repository serverRepository `json:"repository"`
}

type serverRepository struct {
	Slug    string        `json:"slug"`
	Project serverProject `json:"project"`
}

type serverProject struct {
	Key string `json:"key"`
}

// bitbucketServerPRRequest is the Bitbucket Server "create pull request" body.
type bitbucketServerPRRequest struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	FromRef     serverRef `json:"fromRef"`
	ToRef       serverRef `json:"toRef"`
}

// bitbucketServerPRResponse carries the created PR's self link (the first
// links.self entry is the web URL).
type bitbucketServerPRResponse struct {
	Links struct {
		Self []struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
}

// CreatePullRequest opens a PR via the Bitbucket Server REST API and returns
// the first links.self href.
func (p *BitbucketServerProvider) CreatePullRequest(ctx context.Context, repo string, branch string, baseBranch string, title string, body string) (string, error) {
	projectKey, repoSlug, err := splitServerRepo(repo)
	if err != nil {
		return "", err
	}

	ref := func(branch string) serverRef {
		r := serverRef{ID: "refs/heads/" + branch}
		r.Repository.Slug = repoSlug
		r.Repository.Project.Key = projectKey
		return r
	}
	payload := bitbucketServerPRRequest{
		Title:       title,
		Description: body,
		FromRef:     ref(branch),
		ToRef:       ref(baseBranch),
	}

	headers := map[string]string{}
	if p.token != "" {
		headers["Authorization"] = "Bearer " + p.token
	}
	url := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests", p.baseURL, projectKey, repoSlug)

	var prResp bitbucketServerPRResponse
	if err := postPRJSON(ctx, p.client, url, headers, payload, &prResp); err != nil {
		return "", err
	}
	if len(prResp.Links.Self) == 0 || prResp.Links.Self[0].Href == "" {
		return "", errors.New("git: pull request response missing links.self href")
	}
	return prResp.Links.Self[0].Href, nil
}

// splitServerRepo parses the "<projectKey>/<repoSlug>" identifier Bitbucket
// Server addresses a repo by. Exactly one slash is required — project keys and
// repo slugs never contain one.
func splitServerRepo(repo string) (projectKey, repoSlug string, err error) {
	key, slug, ok := strings.Cut(repo, "/")
	if !ok || key == "" || slug == "" || strings.Contains(slug, "/") {
		return "", "", fmt.Errorf("git: bitbucket server repo %q must be \"<projectKey>/<repoSlug>\"", repo)
	}
	return key, slug, nil
}
