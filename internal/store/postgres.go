package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a looked-up row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrSessionInvalid is returned when a session token is unknown or expired.
var ErrSessionInvalid = errors.New("store: session invalid")

// Store is the persistence seam the rest of Weave depends on. PostgresStore
// implements it; the interface keeps callers testable and honours "no globals,
// DI everywhere". See DESIGN.md for the model.
type Store interface {
	// UpsertUser returns the user with the given subject, creating it (or
	// refreshing its email) on first sight.
	UpsertUser(ctx context.Context, subject, email string) (User, error)

	// CreateUseCase inserts a new use case and returns it with its generated id.
	CreateUseCase(ctx context.Context, uc UseCase) (UseCase, error)
	// GetUseCaseByKey returns the use case with the given key, or ErrNotFound.
	GetUseCaseByKey(ctx context.Context, key string) (UseCase, error)
	// ListUseCasesForPrincipal returns every use case the principal may act on
	// (direct membership or matching group grant), ordered by key.
	ListUseCasesForPrincipal(ctx context.Context, p Principal) ([]UseCase, error)

	// AddMembership grants a role directly to a user on a use case (idempotent).
	AddMembership(ctx context.Context, m Membership) error
	// AddGroupGrant grants a role to an IdP group on a use case (idempotent).
	AddGroupGrant(ctx context.Context, g GroupGrant) error
	// EffectiveRole resolves the principal's role on the use case, applying the
	// hybrid rule. ok is false when the principal has no access.
	EffectiveRole(ctx context.Context, useCaseID string, p Principal) (role Role, ok bool, err error)

	// CreateSession mints a session for userID valid for ttl, snapshotting the
	// principal's groups, and returns the raw token once (only its hash is
	// stored).
	CreateSession(ctx context.Context, userID string, groups []string, ttl time.Duration) (rawToken string, s Session, err error)
	// ResolvePrincipal reconstructs the full principal (subject + snapshotted
	// groups) for a raw session token, or ErrSessionInvalid if it is unknown or
	// expired.
	ResolvePrincipal(ctx context.Context, rawToken string) (Principal, error)
	// DeleteSession revokes a session by its raw token (idempotent).
	DeleteSession(ctx context.Context, rawToken string) error

	// Close releases the underlying connection pool.
	Close()
}

// PostgresStore is the pgx-backed Store.
type PostgresStore struct {
	pool *pgxpool.Pool
}

var _ Store = (*PostgresStore)(nil)

// NewPostgresStore opens a connection pool to dsn and verifies connectivity.
// Callers own migration (see Migrate) and Close.
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: opening pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: pinging database: %w", err)
	}
	return &PostgresStore{pool: pool}, nil
}

// Close releases the pool.
func (s *PostgresStore) Close() { s.pool.Close() }

const useCaseColumns = `id, key, display_name, repo_url, repo_slug, pr_provider, base_branch, env, credential_ref, created_at`

func scanUseCase(row pgx.Row) (UseCase, error) {
	var uc UseCase
	err := row.Scan(&uc.ID, &uc.Key, &uc.DisplayName, &uc.RepoURL, &uc.RepoSlug,
		&uc.PRProvider, &uc.BaseBranch, &uc.Env, &uc.CredentialRef, &uc.Created)
	return uc, err
}

func (s *PostgresStore) UpsertUser(ctx context.Context, subject, email string) (User, error) {
	const q = `
		INSERT INTO users (subject, email) VALUES ($1, $2)
		ON CONFLICT (subject) DO UPDATE SET email = EXCLUDED.email
		RETURNING id, subject, email, created_at`
	var u User
	if err := s.pool.QueryRow(ctx, q, subject, email).Scan(&u.ID, &u.Subject, &u.Email, &u.Created); err != nil {
		return User{}, fmt.Errorf("store: upserting user %q: %w", subject, err)
	}
	return u, nil
}

func (s *PostgresStore) CreateUseCase(ctx context.Context, uc UseCase) (UseCase, error) {
	const q = `
		INSERT INTO use_cases (key, display_name, repo_url, repo_slug, pr_provider, base_branch, env, credential_ref)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + useCaseColumns
	provider := uc.PRProvider
	if provider == "" {
		provider = "bitbucket-cloud"
	}
	base := uc.BaseBranch
	if base == "" {
		base = "main"
	}
	created, err := scanUseCase(s.pool.QueryRow(ctx, q,
		uc.Key, uc.DisplayName, uc.RepoURL, uc.RepoSlug, provider, base, uc.Env, uc.CredentialRef))
	if err != nil {
		return UseCase{}, fmt.Errorf("store: creating use case %q: %w", uc.Key, err)
	}
	return created, nil
}

func (s *PostgresStore) GetUseCaseByKey(ctx context.Context, key string) (UseCase, error) {
	const q = `SELECT ` + useCaseColumns + ` FROM use_cases WHERE key = $1`
	uc, err := scanUseCase(s.pool.QueryRow(ctx, q, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return UseCase{}, fmt.Errorf("store: use case %q: %w", key, ErrNotFound)
	}
	if err != nil {
		return UseCase{}, fmt.Errorf("store: getting use case %q: %w", key, err)
	}
	return uc, nil
}

func (s *PostgresStore) ListUseCasesForPrincipal(ctx context.Context, p Principal) ([]UseCase, error) {
	const q = `
		SELECT ` + useCaseColumns + ` FROM use_cases uc
		WHERE EXISTS (
			SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
			WHERE m.use_case_id = uc.id AND u.subject = $1
		) OR EXISTS (
			SELECT 1 FROM group_grants g
			WHERE g.use_case_id = uc.id AND g.group_name = ANY($2)
		)
		ORDER BY uc.key`
	rows, err := s.pool.Query(ctx, q, p.Subject, p.Groups)
	if err != nil {
		return nil, fmt.Errorf("store: listing use cases for %q: %w", p.Subject, err)
	}
	defer rows.Close()

	var out []UseCase
	for rows.Next() {
		uc, err := scanUseCase(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scanning use case: %w", err)
		}
		out = append(out, uc)
	}
	return out, rows.Err()
}

func (s *PostgresStore) AddMembership(ctx context.Context, m Membership) error {
	if !m.Role.Valid() {
		return fmt.Errorf("store: invalid role %q", m.Role)
	}
	const q = `
		INSERT INTO memberships (user_id, use_case_id, role) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, use_case_id) DO UPDATE SET role = EXCLUDED.role`
	if _, err := s.pool.Exec(ctx, q, m.UserID, m.UseCaseID, string(m.Role)); err != nil {
		return fmt.Errorf("store: adding membership: %w", err)
	}
	return nil
}

func (s *PostgresStore) AddGroupGrant(ctx context.Context, g GroupGrant) error {
	if !g.Role.Valid() {
		return fmt.Errorf("store: invalid role %q", g.Role)
	}
	const q = `
		INSERT INTO group_grants (use_case_id, group_name, role) VALUES ($1, $2, $3)
		ON CONFLICT (use_case_id, group_name) DO UPDATE SET role = EXCLUDED.role`
	if _, err := s.pool.Exec(ctx, q, g.UseCaseID, g.GroupName, string(g.Role)); err != nil {
		return fmt.Errorf("store: adding group grant: %w", err)
	}
	return nil
}

func (s *PostgresStore) EffectiveRole(ctx context.Context, useCaseID string, p Principal) (Role, bool, error) {
	// Direct memberships for this subject on this use case.
	const directQ = `
		SELECT m.role FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.use_case_id = $1 AND u.subject = $2`
	directRows, err := s.pool.Query(ctx, directQ, useCaseID, p.Subject)
	if err != nil {
		return "", false, fmt.Errorf("store: querying memberships: %w", err)
	}
	var direct []Role
	for directRows.Next() {
		var r string
		if err := directRows.Scan(&r); err != nil {
			directRows.Close()
			return "", false, fmt.Errorf("store: scanning membership role: %w", err)
		}
		direct = append(direct, Role(r))
	}
	directRows.Close()
	if err := directRows.Err(); err != nil {
		return "", false, err
	}

	// All group grants on this use case (the pure decision filters by group).
	const grantQ = `SELECT group_name, role FROM group_grants WHERE use_case_id = $1`
	grantRows, err := s.pool.Query(ctx, grantQ, useCaseID)
	if err != nil {
		return "", false, fmt.Errorf("store: querying group grants: %w", err)
	}
	var grants []GroupGrant
	for grantRows.Next() {
		var g GroupGrant
		var r string
		if err := grantRows.Scan(&g.GroupName, &r); err != nil {
			grantRows.Close()
			return "", false, fmt.Errorf("store: scanning group grant: %w", err)
		}
		g.Role = Role(r)
		grants = append(grants, g)
	}
	grantRows.Close()
	if err := grantRows.Err(); err != nil {
		return "", false, err
	}

	role, ok := EffectiveRole(direct, grants, p.Groups)
	return role, ok, nil
}

func (s *PostgresStore) CreateSession(ctx context.Context, userID string, groups []string, ttl time.Duration) (string, Session, error) {
	raw, err := newToken()
	if err != nil {
		return "", Session{}, err
	}
	if groups == nil {
		groups = []string{}
	}
	expires := time.Now().Add(ttl)
	const q = `
		INSERT INTO sessions (token_hash, user_id, groups, expires_at) VALUES ($1, $2, $3, $4)
		RETURNING user_id, groups, expires_at, created_at`
	var sess Session
	if err := s.pool.QueryRow(ctx, q, HashSessionToken(raw), userID, groups, expires).
		Scan(&sess.UserID, &sess.Groups, &sess.Expires, &sess.Created); err != nil {
		return "", Session{}, fmt.Errorf("store: creating session: %w", err)
	}
	return raw, sess, nil
}

func (s *PostgresStore) ResolvePrincipal(ctx context.Context, rawToken string) (Principal, error) {
	const q = `
		SELECT u.subject, s.groups, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1`
	var (
		subject string
		groups  []string
		expires time.Time
	)
	err := s.pool.QueryRow(ctx, q, HashSessionToken(rawToken)).Scan(&subject, &groups, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrSessionInvalid
	}
	if err != nil {
		return Principal{}, fmt.Errorf("store: resolving session principal: %w", err)
	}
	if !expires.After(time.Now()) {
		return Principal{}, ErrSessionInvalid
	}
	return Principal{Subject: subject, Groups: groups}, nil
}

func (s *PostgresStore) DeleteSession(ctx context.Context, rawToken string) error {
	const q = `DELETE FROM sessions WHERE token_hash = $1`
	if _, err := s.pool.Exec(ctx, q, HashSessionToken(rawToken)); err != nil {
		return fmt.Errorf("store: deleting session: %w", err)
	}
	return nil
}

// newToken returns a 256-bit random token, hex-encoded.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("store: generating token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
