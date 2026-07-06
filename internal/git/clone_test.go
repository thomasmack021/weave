package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

// newBareRemoteWithCommit builds a local bare repository seeded with one commit
// (containing main.tf) on branch "master", standing in for the remote workspace
// repo. It returns the bare repo's path and the seeded commit's hash. Fully
// offline, mirroring the fixture style of TestRepository_Push.
func newBareRemoteWithCommit(t *testing.T) (remoteDir string, headHash string) {
	t.Helper()

	remoteDir = t.TempDir()
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
		t.Fatalf("Push (seeding remote): %v", err)
	}

	head, err := r.repo.Head()
	if err != nil {
		t.Fatalf("reading fixture HEAD: %v", err)
	}
	return remoteDir, head.Hash().String()
}

// Clone-to-temp is the git safety boundary of the whole GitOps flow — all
// mutation happens in a disposable clone, so any failure before Push leaves
// the remote untouched by construction.
func TestClone_LocalBareRemote(t *testing.T) {
	remoteDir, wantHash := newBareRemoteWithCommit(t)
	destDir := t.TempDir()

	cloned, err := Clone(context.Background(), remoteDir, "master", "", destDir)
	if err != nil {
		t.Fatalf("Clone returned unexpected error: %v", err)
	}

	// The clone's HEAD must sit on the requested branch, at the seeded commit.
	head, err := cloned.repo.Head()
	if err != nil {
		t.Fatalf("reading cloned HEAD: %v", err)
	}
	if got, want := head.Name().Short(), "master"; got != want {
		t.Errorf("cloned HEAD on branch %q, want %q", got, want)
	}
	if got := head.Hash().String(); got != wantHash {
		t.Errorf("cloned HEAD hash = %s, want %s", got, wantHash)
	}

	// The working tree must be materialized in dir so the domain layer can
	// edit files there.
	if _, err := os.Stat(filepath.Join(destDir, "main.tf")); err != nil {
		t.Errorf("cloned working tree missing main.tf: %v", err)
	}

	// The returned Repository must be operational as a Committer (a follow-up
	// branch checkout is the orchestrator's very next step).
	if err := cloned.CheckoutBranch("weave/add-cloud-run-abc123"); err != nil {
		t.Errorf("CheckoutBranch on cloned repo returned unexpected error: %v", err)
	}
}
