# DESIGN — Gate 1: PostgreSQL sessions + multi-tenant use-case RBAC

Status: **foundation in progress** (2026-07-07). This document is the agreed
architecture for the gate; the code lands incrementally (see §8). It is the
source of truth for *why* the data model looks the way it does — turn-level
state still lives in `HANDOFF.md`.

## 1. Context & goal

v1 is single-tenant: one `weaved` process serves exactly one target repo,
configured globally (`WEAVE_REPO_URL`, `WEAVE_PR_REPO`, …). The product vision
is a **central Weave instance in a shared layer that reaches many customer
environments** — so one process must serve many target repos, and must know
*who* is asking and *which* use cases they may touch.

This gate introduces:

- **Identity** — who is calling (from an SSO proxy; Entra ID for corporates, a
  static identity for a solo developer).
- **Use cases** — the tenant unit: a target CD-pipeline repo plus its provider
  config and credentials, owned in the database.
- **RBAC** — which principals may act on which use cases, and in what role.
- **Sessions** — PostgreSQL-backed, so identity survives across requests.

It does **not** weaken any v1 invariant. In particular: the request may *select*
a use case, but that use case's repo config and credentials come from the
database (server side), and access is gated by the authenticated principal —
so "config never comes from the HTTP request body" (invariant 7) still holds.

## 2. Identity & the `Authenticator` seam  *(next increment — described here)*

Identity is resolved by a pluggable `Authenticator` that turns an inbound
request into a `Principal{Subject, Groups}`:

- **Proxy-header backend (production).** An auth proxy / SSO gateway
  (oauth2-proxy, Azure App Proxy, an ingress) terminates OIDC against **Entra
  ID** and forwards the verified identity and group claims as headers
  (configurable names, e.g. `X-Forwarded-Email`, `X-Forwarded-Groups`). Weave
  trusts these and never handles credentials. The header names are config, so
  the same code serves any OIDC proxy.
- **Static backend (solo developer / dev mode).** A single configured identity
  (e.g. `WEAVE_DEV_SUBJECT`) treated as the caller, so one person can run the
  container with no IdP.

The proxy is trusted by network placement (Weave binds only where the proxy can
reach it). Sessions (a hashed opaque token in Postgres) let the browser wizard
avoid re-presenting identity on every call.

## 3. Data model

Entities (see `internal/store/model.go`; schema in
`internal/store/migrations/`):

- **user** — an authenticated principal, keyed by `subject` (unique IdP
  identity, typically email). Rows are created lazily on first sight
  (`UpsertUser`).
- **use_case** — a tenant. Carries its own target repo (`repo_url`,
  `repo_slug`, `pr_provider`, `base_branch`, `env`) and an opaque
  `credential_ref` resolved by the `CredentialStore` (§5). Identified by a
  stable `key`.
- **membership** — `(user, use_case, role)`: a *direct* grant to one user.
- **group_grant** — `(use_case, group_name, role)`: a grant to *any* principal
  whose forwarded groups include `group_name` (the Entra path).
- **session** — `(token_hash, user, expires_at)`. Only the SHA-256 of the token
  is stored; the raw token is returned once at creation and never persisted.

`role` is `developer` or `admin`. Admins onboard module source repos and use
cases; developers scaffold into the use cases they can reach.

## 4. Authorization model — **hybrid**

A principal's effective role on a use case is the **highest** of:

1. any **direct membership** for that user on that use case, and
2. any **group_grant** on that use case whose `group_name` is in the
   principal's forwarded groups.

`admin > developer`. No membership and no matching grant ⇒ **no access** (the
use case is invisible and un-actionable). The pure decision —
`EffectiveRole(direct []Role, grants []GroupGrant, principalGroups []string)` —
lives in `model.go` and is unit-tested without a database; the database simply
supplies the inputs (`Store.EffectiveRole`, `Store.ListUseCasesForPrincipal`).

This satisfies both personas: a solo developer is a direct member of their use
cases; a corporate deployment grants use cases to Entra groups and never
manages individual users in Weave.

## 5. Per-use-case config & the `CredentialStore` seam

Because the central instance pushes to many repos, each use case needs its own
git/PR credentials. The database stores only an opaque `credential_ref`; a
`CredentialStore` resolves it to a token at use time:

```go
type CredentialStore interface {
    Resolve(ctx context.Context, ref string) (token string, err error)
}
```

Foundation ships `StaticCredentialStore` (an in-memory map, for tests and
single-tenant runs). Future backends — an encrypted-column store keyed by an
app KEK, or GCP Secret Manager via workload identity — implement the same
interface with no schema change. Raw tokens never sit in application logs or in
the `use_case` row.

## 6. Persistence

- **Driver:** `pgx/v5` (`pgxpool`).
- **Migrations:** `golang-migrate/v4` with an `iofs` source embedding
  `internal/store/migrations/*.sql` (`embed.FS`), applied by `Migrate(ctx,
  dsn)` at startup and in tests. `gen_random_uuid()` is built in on Postgres
  13+.
- **`Store` interface:** the repository seam the rest of the app depends on
  (`internal/store`). `PostgresStore` implements it; the interface keeps
  handlers testable and honours "no globals, DI everywhere".

## 7. Testing

- **Unit (always run):** the pure `EffectiveRole` decision, role ordering, and
  session-token hashing — no database.
- **Integration (`-tags=integration`):** `PostgresStore` against a real
  Postgres via **testcontainers-go**, migrations applied, exercising users,
  use cases, memberships, group grants, the RBAC-filtered listing, and session
  lifecycle (create → get → expiry → delete). Behind a build tag so the default
  `go test ./...` stays fast and Docker-free; run explicitly with
  `go test -tags=integration ./internal/store/` (Docker required).

## 8. Rollout increments

1. **This session — foundation (reversible, not wired to endpoints):**
   §3 model + `EffectiveRole`, migrations, `Store` interface + `PostgresStore`,
   `CredentialStore` + `StaticCredentialStore`, unit + integration tests.
2. **Next — identity:** the `Authenticator` middleware (proxy-header + static
   backends) and PostgreSQL session issue/verify on the HTTP boundary.
3. **Next — enforcement:** `/api/catalog`, `/api/workspace`, `/api/scaffold`
   become use-case-scoped; the orchestrator resolves per-use-case repo config
   and credentials from the `Store` + `CredentialStore` instead of global
   config; every action is RBAC-checked. Admin endpoints to manage use cases,
   memberships, and group grants.

Until increment 3 lands, the live server keeps its v1 single-tenant config path
untouched — the foundation compiles and is fully tested but changes no request
behaviour.

## 9. Invariants preserved

- **Fail-before-mutate / no apply creds:** unchanged; RBAC is an additional
  gate *before* the existing validate→clone→PR path.
- **Config not from the request:** the request selects a use case *key*; its
  repo config and credentials are read from the database, and access is denied
  unless the principal is authorized. The client still cannot inject a repo URL
  or token.
- **Developer never sees Terraform; only golden modules; choice inputs virtual:**
  untouched — this gate is orthogonal to generation.
