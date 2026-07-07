package usecase

import (
	"context"
	"testing"

	"github.com/thomasmack021/weave/internal/registry"
	"github.com/thomasmack021/weave/internal/store"
)

func TestOrchestratorFactory_For(t *testing.T) {
	creds := store.NewStaticCredentialStore(map[string]string{"ref-a": "secret-token"})
	f := NewOrchestratorFactory(registry.NewFakeRegistry(), creds)

	// A use case with a resolvable credential and a known provider builds a runner.
	uc := store.UseCase{Key: "payments", RepoURL: "u", RepoSlug: "a/b", PRProvider: "github", Env: "prod", CredentialRef: "ref-a"}
	runner, err := f.For(context.Background(), uc)
	if err != nil {
		t.Fatalf("For() error = %v, want nil", err)
	}
	if runner == nil {
		t.Fatal("For() returned nil runner")
	}

	// A missing credential surfaces an error rather than a half-built runner.
	uc.CredentialRef = "missing"
	if _, err := f.For(context.Background(), uc); err == nil {
		t.Error("For() with an unresolvable credential error = nil, want an error")
	}

	// An unknown provider is an error.
	uc.CredentialRef = "ref-a"
	uc.PRProvider = "gerrit"
	if _, err := f.For(context.Background(), uc); err == nil {
		t.Error("For() with an unknown provider error = nil, want an error")
	}
}
