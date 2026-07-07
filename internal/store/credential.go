package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrCredentialNotFound is returned when a credential reference resolves to
// nothing.
var ErrCredentialNotFound = errors.New("store: credential not found")

// CredentialStore resolves a use case's opaque credential reference to the git
// token used to push and open PRs. The database stores only the reference; the
// token lives behind this seam so it can come from an encrypted column, a cloud
// secret manager, or (for tests and single-tenant runs) an in-memory map,
// without any schema change.
type CredentialStore interface {
	Resolve(ctx context.Context, ref string) (token string, err error)
}

// SharedCredentialStore returns one token for every use case — the model
// where a central Weave uses a single platform service-account across all
// target repos. Per-use-case secret resolution (encrypted column, cloud secret
// manager) replaces this behind the same interface later.
type SharedCredentialStore struct {
	token string
}

// NewSharedCredentialStore builds a SharedCredentialStore around one token.
func NewSharedCredentialStore(token string) *SharedCredentialStore {
	return &SharedCredentialStore{token: token}
}

// Resolve returns the shared token for any reference.
func (s *SharedCredentialStore) Resolve(context.Context, string) (string, error) {
	return s.token, nil
}

// StaticCredentialStore is an in-memory CredentialStore for tests and
// single-tenant deployments where credentials are supplied at boot.
type StaticCredentialStore struct {
	byRef map[string]string
}

// NewStaticCredentialStore builds a StaticCredentialStore from a ref→token map.
// The map is copied, so later mutation of the argument does not affect it.
func NewStaticCredentialStore(byRef map[string]string) *StaticCredentialStore {
	cp := make(map[string]string, len(byRef))
	for k, v := range byRef {
		cp[k] = v
	}
	return &StaticCredentialStore{byRef: cp}
}

// Resolve returns the token for ref, or ErrCredentialNotFound.
func (s *StaticCredentialStore) Resolve(_ context.Context, ref string) (string, error) {
	token, ok := s.byRef[ref]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrCredentialNotFound, ref)
	}
	return token, nil
}
