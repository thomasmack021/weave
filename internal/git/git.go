// Package git is the version-control boundary for the GitOps flow. The CLI
// drives it after the domain Service returns a ChangeSet: it stages the changed
// files and records a commit. domain.Service never imports this package.
package git

import (
	"context"
	"errors"
	"fmt"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// Default identity used when git config has no user set (e.g. headless CI), so
// the tool never panics on a missing signature.
const (
	defaultAuthorName  = "Weave CLI"
	defaultAuthorEmail = "weave@localhost"
)

// ErrNothingToCommit is returned by Commit when nothing is staged. The caller
// may treat it as a benign no-op, consistent with an unchanged ChangeSet.
var ErrNothingToCommit = errors.New("git: nothing to commit")

// Committer drives the GitOps write path: it stages files, records a commit,
// switches to a feature branch, and pushes that branch to a remote. The HTTP
// handlers depend on this interface and mock it in tests. Obtaining a
// Committer in the first place is a construction concern and lives outside
// the interface: see Open, OpenWithAuthor, and Clone.
type Committer interface {
	// Stage adds the given repo-relative paths to the index.
	Stage(paths ...string) error
	// Commit records the staged index under message and returns the new commit's
	// hash. It returns ErrNothingToCommit if nothing is staged.
	Commit(message string) (hash string, err error)
	// CheckoutBranch switches the worktree to branch, creating it from the
	// current HEAD when it does not yet exist.
	CheckoutBranch(branch string) error
	// Push publishes the current branch to the named remote, authenticating with
	// the supplied service-account token.
	Push(ctx context.Context, remoteName string, token string) error
}

// Author is the identity recorded on a commit.
type Author struct {
	Name  string
	Email string
}

// Repository is a Committer backed by a real on-disk git repository.
type Repository struct {
	repo   *gogit.Repository
	author Author
}

// Compile-time assertion that *Repository implements Committer.
var _ Committer = (*Repository)(nil)

// Open opens the git repository containing path and resolves the commit Author
// from the repository's git config (user.name / user.email), falling back to a
// safe default identity when the config is missing.
func Open(path string) (*Repository, error) {
	repo, err := gogit.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("git: opening repository at %s: %w", path, err)
	}
	return &Repository{repo: repo, author: resolveAuthor(repo)}, nil
}

// OpenWithAuthor opens the git repository at path with an explicit identity, for
// CI environments without git config and for deterministic tests.
func OpenWithAuthor(path string, author Author) (*Repository, error) {
	repo, err := gogit.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("git: opening repository at %s: %w", path, err)
	}
	return &Repository{repo: repo, author: author}, nil
}

// Clone clones the repository at url into dir with branch checked out and
// returns a Repository rooted there, authenticating with the supplied
// service-account token (empty token = unauthenticated, e.g. local/file
// remotes). dir is the caller-owned disposable directory that bounds all
// subsequent mutation — the git safety boundary of the GitOps flow: discard
// dir before Push and the remote has seen nothing.
func Clone(ctx context.Context, url, branch, token, dir string) (*Repository, error) {
	opts := &gogit.CloneOptions{
		URL:           url,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
	}
	if token != "" {
		opts.Auth = &githttp.BasicAuth{Username: "x-token-auth", Password: token}
	}

	repo, err := gogit.PlainCloneContext(ctx, dir, false, opts)
	if err != nil {
		return nil, fmt.Errorf("git: cloning %s into %s: %w", url, dir, err)
	}
	return &Repository{repo: repo, author: resolveAuthor(repo)}, nil
}

// resolveAuthor reads user.name / user.email from the merged git config,
// defaulting any missing field so a commit always has a valid signature.
func resolveAuthor(repo *gogit.Repository) Author {
	author := Author{Name: defaultAuthorName, Email: defaultAuthorEmail}
	cfg, err := repo.ConfigScoped(config.LocalScope)
	if err != nil {
		return author
	}
	if cfg.User.Name != "" {
		author.Name = cfg.User.Name
	}
	if cfg.User.Email != "" {
		author.Email = cfg.User.Email
	}
	return author
}

// Stage adds the given repo-relative paths to the index. Adding a non-existent
// path returns an error from the underlying go-git API.
func (r *Repository) Stage(paths ...string) error {
	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("git: opening worktree: %w", err)
	}
	for _, p := range paths {
		if _, err := wt.Add(p); err != nil {
			return fmt.Errorf("git: staging %s: %w", p, err)
		}
	}
	return nil
}

// Commit records the staged index under message and returns the commit hash. It
// returns ErrNothingToCommit when nothing is staged, preventing empty commits.
func (r *Repository) Commit(message string) (string, error) {
	wt, err := r.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("git: opening worktree: %w", err)
	}

	status, err := wt.Status()
	if err != nil {
		return "", fmt.Errorf("git: reading status: %w", err)
	}
	if !hasStagedChanges(status) {
		return "", ErrNothingToCommit
	}

	hash, err := wt.Commit(message, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  r.author.Name,
			Email: r.author.Email,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("git: committing: %w", err)
	}
	return hash.String(), nil
}

// CheckoutBranch switches the worktree to branch, creating it from the current
// HEAD when it does not yet exist.
func (r *Repository) CheckoutBranch(branch string) error {
	wt, err := r.repo.Worktree()
	if err != nil {
		return fmt.Errorf("git: opening worktree: %w", err)
	}

	ref := plumbing.NewBranchReferenceName(branch)

	// Create the branch from the current HEAD only when it does not yet exist;
	// otherwise switch to the existing branch.
	_, err = r.repo.Reference(ref, false)
	create := errors.Is(err, plumbing.ErrReferenceNotFound)
	if err != nil && !create {
		return fmt.Errorf("git: resolving branch %s: %w", branch, err)
	}

	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: ref, Create: create}); err != nil {
		return fmt.Errorf("git: checking out branch %s: %w", branch, err)
	}
	return nil
}

// Push publishes the current branch to the named remote under a like-named
// branch, authenticating with the supplied service-account token. An empty
// token leaves the request unauthenticated (e.g. for local/file remotes).
func (r *Repository) Push(ctx context.Context, remoteName string, token string) error {
	head, err := r.repo.Head()
	if err != nil {
		return fmt.Errorf("git: reading HEAD: %w", err)
	}

	// Push the current branch ref to an identically named ref on the remote.
	refspec := config.RefSpec(fmt.Sprintf("%[1]s:%[1]s", head.Name()))
	opts := &gogit.PushOptions{
		RemoteName: remoteName,
		RefSpecs:   []config.RefSpec{refspec},
	}
	// Bitbucket Cloud accepts an access token as the password over HTTP Basic
	// auth, with a fixed sentinel username.
	if token != "" {
		opts.Auth = &githttp.BasicAuth{Username: "x-token-auth", Password: token}
	}

	if err := r.repo.PushContext(ctx, opts); err != nil {
		if errors.Is(err, gogit.NoErrAlreadyUpToDate) {
			return nil
		}
		return fmt.Errorf("git: pushing to %s: %w", remoteName, err)
	}
	return nil
}

// hasStagedChanges reports whether any file is staged in the index (a Staging
// code other than Unmodified/Untracked).
func hasStagedChanges(status gogit.Status) bool {
	for _, s := range status {
		switch s.Staging {
		case gogit.Unmodified, gogit.Untracked:
			// not staged
		default:
			return true
		}
	}
	return false
}
