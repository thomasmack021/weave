package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

// newTestRepo creates a temporary directory initialized as a git repository and
// returns its path. The directory is removed when the test finishes.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "weave-git-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if _, err := gogit.PlainInit(dir, false); err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	return dir
}

func testAuthor() Author {
	return Author{Name: "Weave Test", Email: "test@weave.dev"}
}

func TestRepository_Commit_HappyPath(t *testing.T) {
	dir := newTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "dummy.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	repo, err := OpenWithAuthor(dir, testAuthor())
	if err != nil {
		t.Fatalf("OpenWithAuthor: %v", err)
	}

	if err := repo.Stage("dummy.txt"); err != nil {
		t.Fatalf("Stage returned unexpected error: %v", err)
	}

	hash, err := repo.Commit("test commit")
	if err != nil {
		t.Fatalf("Commit returned unexpected error: %v", err)
	}
	if hash == "" {
		t.Errorf("Commit returned empty hash, want non-empty")
	}
}

func TestRepository_Commit_NothingToCommit(t *testing.T) {
	dir := newTestRepo(t)

	repo, err := OpenWithAuthor(dir, testAuthor())
	if err != nil {
		t.Fatalf("OpenWithAuthor: %v", err)
	}

	if _, err := repo.Commit("empty"); !errors.Is(err, ErrNothingToCommit) {
		t.Errorf("Commit error = %v, want it to wrap ErrNothingToCommit", err)
	}
}

func TestRepository_Stage_FileNotFound(t *testing.T) {
	dir := newTestRepo(t)

	repo, err := OpenWithAuthor(dir, testAuthor())
	if err != nil {
		t.Fatalf("OpenWithAuthor: %v", err)
	}

	if err := repo.Stage("does-not-exist.txt"); err == nil {
		t.Errorf("Stage(non-existent) returned nil error, want an error")
	}
}

// newTestRepoWithCommit returns a Repository whose HEAD is born from a single
// initial commit, so branch/push operations have a commit to act on.
func newTestRepoWithCommit(t *testing.T) (*Repository, string) {
	t.Helper()
	dir := newTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# weave\n"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}
	r, err := OpenWithAuthor(dir, testAuthor())
	if err != nil {
		t.Fatalf("OpenWithAuthor: %v", err)
	}
	if err := r.Stage("main.tf"); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := r.Commit("initial commit"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return r, dir
}

func TestRepository_CheckoutBranch(t *testing.T) {
	r, _ := newTestRepoWithCommit(t)

	const branch = "weave/add-cloud-run"
	if err := r.CheckoutBranch(branch); err != nil {
		t.Fatalf("CheckoutBranch returned unexpected error: %v", err)
	}

	head, err := r.repo.Head()
	if err != nil {
		t.Fatalf("reading HEAD: %v", err)
	}
	if got := head.Name().Short(); got != branch {
		t.Errorf("HEAD on branch %q, want %q", got, branch)
	}
}

// A second local bare repository stands in for the remote, keeping the test
// fully offline.
func TestRepository_Push(t *testing.T) {
	remoteDir, err := os.MkdirTemp("", "weave-remote-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(remoteDir) })
	if _, err := gogit.PlainInit(remoteDir, true); err != nil { // bare = "git init --bare"
		t.Fatalf("PlainInit bare: %v", err)
	}

	r, _ := newTestRepoWithCommit(t)
	if _, err := r.repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{remoteDir},
	}); err != nil {
		t.Fatalf("CreateRemote: %v", err)
	}

	if err := r.Push(context.Background(), "origin", ""); err != nil {
		t.Fatalf("Push returned unexpected error: %v", err)
	}

	// The bare remote must now contain the local HEAD commit.
	localHead, err := r.repo.Head()
	if err != nil {
		t.Fatalf("reading local HEAD: %v", err)
	}
	remote, err := gogit.PlainOpen(remoteDir)
	if err != nil {
		t.Fatalf("opening remote: %v", err)
	}
	if _, err := remote.CommitObject(localHead.Hash()); err != nil {
		t.Errorf("remote missing pushed commit %s: %v", localHead.Hash(), err)
	}
}
