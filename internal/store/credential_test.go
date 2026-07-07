package store

import (
	"context"
	"errors"
	"testing"
)

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
