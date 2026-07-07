# Weave IDP (Internal Developer Portal) — Architecture

> **Reading guide:** this document describes both the *target* architecture and
> the *current* state, and is explicit about which is which. Anything marked
> **[TARGET]** is not built yet. As of 2026-07 the system is **v1 of the Weave
> IDP**: the GitOps engine core, t-shirt-size choice modeling, orchestration,
> HTTP API (`/health`, `/api/catalog`, `/api/workspace`, `/api/scaffold`), the embedded web
> wizard, a zero-config local demo mode (`weaved -demo`), and the `cmd/weaved`
> server binary — all complete, tested, and proven by an end-to-end test with
> zero mocks in the production path (`internal/demo`).

## Project history: the CLI origins

Weave was initially built (Phases A–F) as a zero-global Cobra CLI
(`weave init`, `weave add`). That phase established a strict
"Pure Core → Domain → I/O" concentric layering, which kept the core engine
(`internal/hcl`, `internal/domain`) completely ignorant of its interface. The
payoff: we discarded the CLI shell entirely and are wrapping the exact same
engine in a web portal. **The CLI code is deleted** (not parked — `archive/`
holds only superseded markdown), and no document should describe `weave init`,
`weave add`, or `cmd/weave` as existing features.

## Core philosophy

Weave is a minimal self-service scaffolding portal that abstracts
infrastructure complexity away from application developers by offering "Golden
Paths" for cloud provisioning. Developers do not write or see Terraform; they
interact with a business-logic web UI, and Weave translates their choices into
strict, platform-approved GitOps pull requests.

**The business-choice layer is built (Phase 2, complete).** A `type: choice`
input is *virtual*: it never emits a value of its own — the selected option's
`expandsTo` map (defined by the platform team in the spec, never in Weave
code) supplies concrete values for other declared inputs, coerced against
each target's declared type inside `validate.Inputs`. Rulings enforced by
tests: a direct caller value colliding with an expansion is rejected
(`ErrChoiceConflict`, 422); an unknown option value is `ErrUnknownChoice`
(422); a spec-authoring bug (expansion targeting an undeclared input, or an
uncoercible value) is `ErrSpecInvalid` (500) and **never** wraps a
caller-fault sentinel — the developer is never blamed for the platform's
mistakes. The catalog DTO flags expansion-target inputs (`managedByChoice`)
so the wizard hides them; the `expandsTo` mapping itself never reaches the
browser.

## Invariants (enforced today in the engine)

1. **Weave never runs Terraform and never holds apply credentials.** Its only
   write channel to the world is a pushed branch plus a pull request. A
   compromised or buggy Weave can at worst open a bad PR that a human rejects.
2. **Fail-before-mutate.** `domain.AddResource` resolves the module spec and
   validates *all* inputs (accumulated via `errors.Join`, unknown inputs
   rejected) before any file is written. Generated `main.tf` blocks are only
   module blocks pinned to `spec.Source?ref=spec.Version`; hand-rolled
   resources are structurally impossible. Module insertion is idempotent
   (guarded before mutation), so re-running a request produces no changes and
   no empty commit.
3. **Clone-to-temp is the git safety boundary.** All workspace mutation happens
   in a disposable temporary clone. `fs.WriteAtomic` is atomic per *file*, not
   per changeset — a failure mid-changeset can leave a partially written
   *local* workspace. That is harmless precisely because the workspace is a
   temp clone: on any failure before `push`, the directory is discarded and
   the remote never sees anything. This makes clone-to-temp load-bearing, not
   polish. Implemented as the package-level `git.Clone(ctx, url, branch,
   token, dir)` — deliberately *not* a `Committer` method, because cloning
   creates a repository rather than operating on one.
4. **No global variables.** All dependencies (registry, git, providers, config)
   are injected; assembly happens only in `cmd/weaved`'s `main`/`run`.

## System boundaries & tech stack

1. **Frontend (embedded SPA) — complete for v1**
   - A single-file vanilla-JS wizard (`web/index.html`, no build step)
     embedded via Go `embed` and served by `internal/server`:
     (1) choose service → (2) configure — choice inputs render as
     business-language option cards, `managedByChoice` inputs are hidden →
     (3) review & submit → success screen with the PR link. The UI shows
     "request submitted, under review" plus the PR link — never HCL or diffs;
     422 errors render as an actionable list; the 502 pushed-but-no-PR seam
     is explained to the user with the branch name.
   - **[TARGET]** A richer Vue.js build (project/use-case selection, RBAC-
     filtered catalogs) once PostgreSQL lands. The API contract is already
     shaped for it.

2. **Backend API (Go, `internal/server`) — complete, tested**
   - `GET /health` liveness probe + static file serving. Stateless,
     dependency-injected via `New(static, registry.ModuleRegistry, Scaffolder,
     WorkspaceInitializer)`; `Scaffolder` (Day 2) and `WorkspaceInitializer`
     (Day 1) are one-method consumer-side interfaces both satisfied by
     `*orchestrate.Orchestrator` (compile-time asserted in the tests).
   - `GET /api/catalog` → 200 JSON DTO array (name, displayName, description,
     version, inputs name/type/required/description, and for choice inputs
     `options` [value/label/description] plus the `managedByChoice` flag on
     expansion-target inputs). A deliberate subset of `ModuleSpec`: the module
     git `Source` and option `expandsTo` maps never reach the client (tested).
     Registry failure → 500.
   - `POST /api/scaffold {moduleType, instanceName, inputs}` — the only data a
     client may supply; repo URL, base branch, environment, and token come from
     server config, never from the request (tested: config-shaped body fields
     never reach the Scaffolder). Status mapping, by `errors.Is` classification
     only (no validation logic in the server): `201 {prUrl, branch}` on
     success; `200 {changed: false}` idempotent no-op; `422 {errors: [...]}`
     when the error wraps `registry.ErrModuleNotFound` or a caller-fault
     `validate.Err*` sentinel (incl. `ErrUnknownChoice`, `ErrChoiceConflict`;
     `ErrSpecInvalid` is deliberately excluded — a platform spec bug is a
     500, never a 422), with the `errors.Join` flattened to one entry per
     failure;
     `400` malformed JSON or missing moduleType/instanceName (Scaffolder not
     invoked); `502 {error, branch}` for the push-succeeded/PR-failed seam
     (the pushed branch is surfaced, not lost); `500 {error}` otherwise.
   - `POST /api/workspace {projectId, statePrefix?}` — the Day 1 counterpart:
     it bootstraps `terraform/env/<env>/` (main.tf, `<env>.tfvars` with the
     injected `project_id`, pipeline.yaml) in the target repo via the same
     fail-before-mutate PR loop, driving `orchestrate.InitWorkspace`. Identical
     status contract and classification rules as `/api/scaffold` (a missing
     `projectId` is a caller fault → `validate.ErrMissingRequired` → 422).
     `statePrefix` is Terraform backend plumbing: omitted by the wizard and
     derived server-side as `weave/<env>`, so the developer never supplies it
     (invariant: the developer never sees Terraform). Idempotent — re-init of
     a workspace whose base branch already matches is a `200 {changed: false}`
     no-op that opens no branch.

3. **Module registry (`internal/registry`)**
   - *Current:* `spec.yaml` manifest parser (`ParseManifest`, apiVersion
     `weave.dev/v1`), the `ModuleRegistry` interface (`Resolve`/`List`), and an
     in-memory `FakeRegistry` for tests.
   - *Current:* `FileSource` — reads a `spec.yaml` from disk, re-reading on
     every call, so a platform engineer editing the spec is reflected in the
     catalog **without redeploy**.
   - **[TARGET]** Git/HTTP-backed source fetching specs from the `iac-modules`
     repository (same interface, different source).

4. **GitOps engine core (`internal/hcl`, `internal/domain`, `internal/fs`,
   `internal/validate`, `internal/pipeline`, `internal/git`)**
   - *Current:* complete and tested as units. Comment-preserving HCL AST
     editing, strict validation, `ChangeSet` production, atomic file writes,
     `pipeline.yaml` merging, `git.Committer`
     (`Stage`/`Commit`/`CheckoutBranch`/`Push`), the package-level `git.Clone`
     (clone-to-temp), and four tested `PullRequestProvider` implementations —
     Bitbucket Cloud (`HTTPProvider`), GitHub (`GitHubProvider`), GitLab
     (`GitLabProvider`), and Bitbucket Server/DC
     (`BitbucketServerProvider`) — all sharing one `postPRJSON` HTTP spine and
     selected by config, never by the request.
   - Note: `internal/pipeline` is a YAML-step merger used by `domain.Scaffold`,
     **not** an orchestrator, despite the name.

5. **Orchestration (`internal/orchestrate`) — complete, tested**
   - `Orchestrator` (built via `New(registry.ModuleRegistry, git.PullRequestProvider,
     Config)`) composes the proven units in fail-before-mutate order:
     1. Pre-flight: `registry.Resolve` + `validate.Inputs` (no I/O side effects) —
        an invalid `Request` returns before `git.Clone` is ever called.
     2. `git.Clone` into `os.MkdirTemp` (always removed via `defer`); workspace
        via `afero.NewBasePathFs`.
     3. `CheckoutBranch("weave/add-<InstanceName>")`.
     4. `domain.AddResource`; if the `ChangeSet` is unchanged, `Run` returns
        `Result{Changed: false}` and stops — no stage/commit/push/PR.
     5. `Stage` → `Commit` → `Push` → `CreatePullRequest` (the shared
        `publish` tail).
   - `InitWorkspace` is the Day 1 sibling of `Run`: same clone-to-temp,
     branch (`weave/init-<Env>`), and `publish` tail, but it calls
     `domain.Scaffold` instead of `AddResource`. Its only pre-flight is a
     non-blank `projectId` (a caller fault → `validate.ErrMissingRequired`);
     an omitted `statePrefix` is derived as `weave/<Env>` so the developer
     never supplies Terraform plumbing. Covered by `init_test.go` (fresh →
     PR, already-scaffolded → no-op, missing projectId → zero remote
     mutation) and end-to-end by `demo.TestEndToEnd_WorkspaceInit`.
   - Known risky seam, handled: if `CreatePullRequest` fails after a successful
     `Push`, both `Run` and `InitWorkspace` return a non-nil error **and**
     `Result{Changed: true, Branch: <pushed branch>}` — the caller can recover
     the pushed branch instead of losing track of it. Covered by
     `TestRun_PRCreationFails_ReportsPushedBranch`.
   - `Config` carries `RepoURL` (clone URL), `RepoSlug` (workspace/repo slug for
     the PR provider), `BaseBranch`, `Token`, `Env` — always server-owned,
     never populated from the request (see rule 3 below).

6. **Database, sessions & RBAC (`internal/store`) — foundation complete;
   enforcement [TARGET]**
   - The persistence + RBAC foundation for multi-tenant operation exists in
     `internal/store` (full spec in `DESIGN.md`): `pgx/v5` `PostgresStore`
     behind a `Store` interface, embedded `golang-migrate` migrations
     (`Migrate`), the entities (`User`, `UseCase`, `Membership`, `GroupGrant`,
     `Session`), the pure hybrid `EffectiveRole` decision (highest of direct
     membership and matching Entra-group grant), and a `CredentialStore` seam
     (`StaticCredentialStore` today; encrypted-DB / secret-manager backends
     later). Session tokens are stored only as a SHA-256 hash.
   - Tested two ways: pure unit tests (no DB) for `EffectiveRole`, role
     ordering, and token hashing; and testcontainers integration tests for
     `PostgresStore` behind `//go:build integration`
     (`go test -tags=integration ./internal/store/`, Docker required).
   - **Identity + sessions (`internal/auth`) — done (increment 2).** A
     pluggable `Authenticator` turns a request into a `store.Principal`:
     `HeaderAuthenticator` (trusted proxy headers — the Entra/oauth2-proxy
     path) or `StaticAuthenticator` (solo-dev). `auth.Service` issues/verifies
     PostgreSQL sessions (`/api/session` login/whoami/logout) and exposes a
     `Middleware` that injects the principal into request context (session
     cookie first, then the Authenticator). Attached opt-in via
     `(*Server).WithSessions`; wired in `cmd/weaved` behind `WEAVE_AUTH_MODE` +
     `WEAVE_DATABASE_URL` (migrations on boot). Off ⇒ the v1 / demo paths are
     byte-for-byte unchanged.
   - **[TARGET] — enforcement (increment 3).** The business endpoints are not
     yet use-case-scoped: identity is established but not used to gate
     scaffolding. Remaining: the orchestrator resolves per-use-case repo config
     + credentials from the `Store`/`CredentialStore` under an RBAC check
     (`EffectiveRole`), admin management endpoints, and a bootstrap-admin. The
     request will select a use-case *key*; its config and credentials come from
     the DB (invariant 4 / "config never from the request" preserved).

7. **Entrypoint (`cmd/weaved`) — complete, tested**
   - First runnable binary. Config via flags/env with precedence
     flag > env > default, resolved by a pure, test-driven
     `loadConfig(args, getenv)` (env lookup injected — tests never touch the
     process environment; missing required settings accumulated into one
     error, `errors.Join` style). Required: `WEAVE_SPECS`, `WEAVE_REPO_URL`,
     `WEAVE_GIT_TOKEN`, `WEAVE_PR_REPO` (legacy `WEAVE_BITBUCKET_REPO`),
     `WEAVE_ENV`. Optional: `WEAVE_LISTEN` (default `:8080`),
     `WEAVE_BASE_BRANCH` (default `main`), `WEAVE_PR_PROVIDER` (default
     `bitbucket-cloud`), `WEAVE_PR_API` (legacy `WEAVE_BITBUCKET_API`).
   - `WEAVE_PR_PROVIDER` selects one of `bitbucket-cloud`, `github`, `gitlab`,
     `bitbucket-server`; validated at config time (unknown → startup error).
     `WEAVE_PR_API` defaults per provider (the `knownPRProviders` map: Cloud
     `api.bitbucket.org`, GitHub `api.github.com`, GitLab `gitlab.com`),
     override for a self-hosted instance or an `httptest` stub (trailing
     slashes normalized). `bitbucket-server` has no public host, so it
     *requires* an explicit `WEAVE_PR_API`. `WEAVE_PR_REPO` is interpreted in
     the provider's terms (`workspace/repo`, `owner/repo`, `group/project`,
     `PROJECTKEY/repo`). The provider is chosen by `newPRProvider` in `main`;
     the request never influences it (invariant 7).
   - All DI assembly in `main`/`run` (rule 3): `loadConfig` →
     `registry.NewFileSource` → `newPRProvider` → `orchestrate.New` →
     `server.New(web.Assets, …)` → `http.ListenAndServe`. `main` is
     smoke-tested (binary + `/health`), not unit-tested; `loadConfig` is.

8. **Demo mode (`internal/demo` + `weaved -demo`) — complete, tested**
   - `demo.Setup(dir)` builds a self-contained local environment: a bare
     "workspace" repo seeded on `main` with the Day 1 scaffold (produced by
     the real `domain.Scaffold`), an example `spec.yaml` showcasing choice
     inputs on two modules, and an in-process fake Bitbucket that implements
     the Cloud PR-creation endpoint and serves a page per created PR.
   - `weaved -demo` synthesizes all required config from `demo.Setup` — the
     30-second quickstart in the README. Never imported by the production
     path; `cmd/weaved` only calls it behind the explicit flag.
   - `internal/demo`'s end-to-end test is the v1 capstone: it assembles the
     production graph exactly as `run()` does and proves, through the real
     HTTP API, both fail-before-mutate (422 ⇒ zero new remote branches) and
     the happy path (choice expansion visible in the pushed branch's tfvars,
     PR URL serving a page, no `size` leakage).

## Authentication to git

A single platform service-account token (Bitbucket Cloud PAT over HTTP basic
auth, username `x-token-auth`) authors every commit and PR. Acceptable for the
skeleton; per-request attribution ("requested by …") and token rotation are
open questions before production.
