package store

import (
	"context"
	"errors"
	"testing"
)

func TestSharedCredentialStore(t *testing.T) {
	cs := NewSharedCredentialStore("platform-bot-token")
	// Any ref resolves to the one shared token — the "single service account
	// for all target repos" model.
	for _, ref := range []string{"", "ref-a", "sm://anything"} {
		got, err := cs.Resolve(context.Background(), ref)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v, want nil", ref, err)
		}
		if got != "platform-bot-token" {
			t.Errorf("Resolve(%q) = %q, want the shared token", ref, got)
		}
	}
}

func TestStaticCredentialStore(t *testing.T) {
	cs := NewStaticCredentialStore(map[string]string{"ref-a": "token-a"})

	got, err := cs.Resolve(context.Background(), "ref-a")
	if err != nil {
		t.Fatalf("Resolve(ref-a) error = %v, want nil", err)
	}
	if got != "token-a" {
		t.Errorf("Resolve(ref-a) = %q, want %q", got, "token-a")
	}

	if _, err := cs.Resolve(context.Background(), "missing"); !errors.Is(err, ErrCredentialNotFound) {
		t.Errorf("Resolve(missing) error = %v, want ErrCredentialNotFound", err)
	}
}
