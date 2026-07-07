# Agent Handover: Weave IDP — v1 shipped

*Last verified against the code 2026-07-07: `go build
./...`, `go vet ./...`, `gofmt` clean; **148 test functions across 14 test-bearing
packages** (`go test ./...`), all passing, including the `internal/demo`
end-to-end capstones; plus **3 testcontainers integration tests** under
`-tags=integration` (`internal/store`, Docker required).
If this document and the code disagree, trust the code and fix this document.
For the running turn-by-turn state, see `HANDOFF.md`. AI agents: start with
`.claude/skills/weave-onboard`.*

## Current state — precise

The project is **Weave v1**: a runnable Web IDP (`go build ./cmd/weaved`)
with the full wizard → golden module → pull request loop working end-to-end,
demoable in 30 seconds via `weaved -demo`. The legacy CLI (`weave init` /
`weave add`, `internal/cli`, `cmd/weave`) has been **deleted**; `archive/`
contains only superseded markdown.

What exists and is proven (do not modify without strong reason — this is the
engine):

- `internal/hcl` — comment-preserving Terraform AST editing; idempotency
  guarded *before* mutation; only module blocks pinned to
  `spec.Source?ref=spec.Version` are ever generated.
- `internal/validate` — strict input validation: unknown inputs rejected, all
  failures accumulated via `errors.Join`, typed sentinel errors. Includes the
  Phase 2 choice layer: `type: choice` inputs are *virtual* — the selected
  option's `expandsTo` supplies values for other declared inputs (coerced
  against each target's declared type, order-independent). Caller faults:
  `ErrUnknownChoice`, `ErrChoiceConflict` (422). Spec-authoring bugs:
  `ErrSpecInvalid` (500) — never wraps a caller-fault sentinel (pinned by
  test).
- `internal/domain` — `AddResource` / `Scaffold`: resolve-and-validate before
  any write; produces a `ChangeSet`.
- `internal/fs` — afero-backed workspace, `WriteAtomic` (atomic per file).
- `internal/pipeline` — `pipeline.yaml` step merging used by `domain.Scaffold`.
  **Not an orchestrator**, despite the name.
- `internal/registry` — `spec.yaml` parser (`ParseManifest`), `ModuleRegistry`
  interface, in-memory `FakeRegistry`, and `FileSource` (file-backed source,
  re-read on every call, so spec edits are visible without redeploy).
- `internal/git` — `Committer` interface (`Stage`, `Commit`, `CheckoutBranch`,
  `Push`) implemented by `Repository`; the package-level `git.Clone(ctx, url,
  branch, token, dir)` (clone-to-temp — our git safety boundary: all mutation
  happens in a disposable temp clone, so any failure before `push` leaves the
  remote untouched by construction; deliberately **not** a `Committer` method,
  since cloning creates a repository rather than operating on one); plus four
  tested `PullRequestProvider` implementations sharing one `postPRJSON` HTTP
  spine — `HTTPProvider` (Bitbucket Cloud), `GitHubProvider`,
  `GitLabProvider`, `BitbucketServerProvider` (Server/DC).
- `internal/server` — `GET /health` + the embedded wizard (`web/index.html`),
  plus the JSON API: `GET /api/catalog` (DTO subset of `ModuleSpec` — module
  `Source` and option `expandsTo` never leak; choice inputs carry
  `options` [value/label/description] and expansion targets carry
  `managedByChoice: true` so the wizard hides them); `POST /api/scaffold`
  (Day 2, `{moduleType, instanceName, inputs}`); and `POST /api/workspace`
  (Day 1, `{projectId, statePrefix?}`). Both mutation endpoints map 201 / 200
  no-op / 422 / 400 / 502 / 500 purely via `errors.Is` — caller-fault
  sentinels incl. `ErrUnknownChoice`/`ErrChoiceConflict`/`ErrMissingRequired`
  → 422, `ErrSpecInvalid` deliberately excluded → 500, `errors.Join`
  flattened to one 422 entry per failure. Depends on the one-method
  `Scaffolder` and `WorkspaceInitializer` interfaces, both satisfied by
  `*orchestrate.Orchestrator` (compile-time asserted); assembly happens in
  `cmd/weaved`.
- `web/` — the single-file vanilla-JS wizard (no build step): catalog →
  configure (choice option cards; managed inputs hidden) → review → submit →
  PR link; renders 422 error lists and explains the 502 pushed-but-no-PR
  seam. Also a "Set up the workspace" link on the catalog screen driving the
  Day 1 `/api/workspace` flow (asks only for the cloud project ID — never
  the Terraform state prefix).
- `internal/store` — **Gate 1 foundation (2026-07-07).** The multi-tenant
  RBAC + sessions persistence layer per `DESIGN.md`: entities (`User`,
  `UseCase`, `Membership`, `GroupGrant`, `Session`), the pure hybrid
  `EffectiveRole` decision, the `Store` interface + `PostgresStore`
  (`pgx/v5`), embedded `golang-migrate` migrations (`Migrate`, incl. `0002`
  adding session group-snapshots), `ResolvePrincipal`, and a `CredentialStore`
  seam (`StaticCredentialStore` today). Unit-tested without a DB;
  `PostgresStore` covered by testcontainers integration tests behind
  `//go:build integration` (`go test -tags=integration ./internal/store/`).
- `internal/auth` — **Gate 1 increment 2 (2026-07-07).** Identity + sessions
  on the HTTP boundary: the pluggable `Authenticator` (`HeaderAuthenticator`
  for the proxy/Entra path, `StaticAuthenticator` for solo dev) → a
  `store.Principal`; `auth.Service` issuing/verifying PostgreSQL sessions
  (`/api/session` login/whoami/logout) with a principal-injecting
  `Middleware`. Attached to the server via opt-in
  `(*server.Server).WithSessions`; wired in `cmd/weaved` behind
  `WEAVE_AUTH_MODE` + `WEAVE_DATABASE_URL` (migrations on boot).
- `internal/usecase` — **Gate 1 increment 3 (2026-07-07).** The multi-tenant
  dispatcher: `usecase.Service` resolves a use case from the store, enforces
  the caller's role (`store.EffectiveRole`, global-admin bypass) BEFORE any
  orchestrator is built, then dispatches via a `RunnerFactory`
  (`OrchestratorFactory` resolves the credential + `git.NewProvider` and
  binds an `orchestrate.Orchestrator` to the use case's repo config).
  Sentinels `ErrUseCaseNotFound` (→404) / `ErrForbidden` (→403). Server
  endpoints (`GET /api/usecases`, `POST /api/usecases[/{key}/scaffold|
  workspace|members|groups]`) attach opt-in via `server.WithUseCases`; wired
  in `cmd/weaved` with `WEAVE_BOOTSTRAP_ADMINS`. Proven end-to-end vs real
  Postgres. **Remaining:** the wizard use-case selector.
- `internal/demo` — zero-config local environment (bare workspace repo seeded
  by the real `domain.Scaffold`, example choice-bearing `spec.yaml`, fake
  in-process Bitbucket serving PR pages) behind `weaved -demo`; its e2e tests
  are the v1 capstones (fail-before-mutate proven through the real HTTP API:
  422 ⇒ zero new remote branches; happy path ⇒ expanded values in the pushed
  branch; plus `TestEndToEnd_WorkspaceInit` for Day 1). Never imported by the
  production path.
- `internal/orchestrate` — `Orchestrator` (via `New(registry.ModuleRegistry,
  git.PullRequestProvider, Config)`): fail-before-mutate composition of the
  above. `Run` is Day 2 (resolve+validate → clone → branch → `AddResource` →
  `publish`); `InitWorkspace` is Day 1 (validate projectId → clone → branch
  `weave/init-<Env>` → `domain.Scaffold` → `publish`), deriving `statePrefix`
  as `weave/<Env>` when omitted. `publish` is the shared stage/commit/push/PR
  tail, so both endpoints handle the push-succeeds/PR-fails seam identically
  (pushed branch name reported alongside the error).

- `cmd/weaved` — the runnable server binary: pure, test-driven
  `loadConfig(args, getenv)` (precedence flag > env > default; required
  settings accumulated into one `errors.Join`-style error; env lookup
  injected) and an assembly-only `main`/`run`. `WEAVE_PR_PROVIDER` (default
  `bitbucket-cloud`) selects the provider; `newPRProvider` builds it in
  `main`. `WEAVE_PR_API` defaults per provider (`knownPRProviders`) and is
  required for `bitbucket-server`; `WEAVE_PR_REPO` is the provider-specific
  repo id. Legacy `WEAVE_BITBUCKET_API`/`WEAVE_BITBUCKET_REPO` still accepted
  as fallbacks.

What does **not** exist yet (the post-v1 roadmap; items marked ✅ APPROVED
were green-lit by the user on 2026-07-07 — see HANDOFF.md):

- ✅ MOSTLY DONE — PostgreSQL sessions + multi-tenant use-case RBAC.
  **Increments 1 (foundation, `internal/store`), 2 (identity + sessions,
  `internal/auth`), and 3 (enforcement, `internal/usecase` + server) are
  DONE** (see `DESIGN.md`), all proven end-to-end vs real Postgres. Decisions
  locked in `DESIGN.md`: proxy-header identity (Entra), hybrid authz (DB
  members OR Entra group grants), per-use-case config + credentials.
  **Remaining:** the wizard use-case selector (frontend); a real per-use-case
  `CredentialStore` backend (encrypted column / secret manager) replacing the
  current `SharedCredentialStore`; an optional per-use-case module registry.
- Full SSO/session UX polish (the proxy-header `Authenticator` + PostgreSQL
  sessions are in; a login page / richer whoami UI could follow).
- Git/HTTP-backed dynamic module registry (same `ModuleRegistry` interface,
  remote source).
- ✅ DONE (2026-07-07) — Bitbucket Server/DC, GitHub, GitLab PR providers
  (new implementations of `git.PullRequestProvider`, selected via
  `WEAVE_PR_PROVIDER`).
- ✅ DONE (2026-07-07) — Day-1 workspace scaffolding via the API
  (`POST /api/workspace` → `orchestrate.InitWorkspace`, plus the wizard's
  "Set up the workspace" link). The target repo no longer needs a
  pre-existing `terraform/env/<env>/`; Weave bootstraps it as a reviewed PR.
- Per-request attribution in commits/PR bodies + token rotation.

**Deployment model context (from the user, 2026-07-07):** the target repo is
a CD-pipeline repo watched by a GitOps operator (ArgoCD at the user's
company) — merge triggers apply. Admins onboard the module source repos;
developers are scoped to their use cases. Design RBAC and the future dynamic
registry around that: use case ↔ target repo, admin-managed module sources.

## The plan (approved; see PHASE0_AUDIT.md §4 for rationale)

**Phase 1 — walking skeleton**, thinnest slice proving the whole loop with
zero mocks in the production path: one module type → validate → generate →
branch → commit → push → PR created. Proven locally (bare repo as push remote +
`httptest` Bitbucket stub).

Strict TDD order (each step red-first):

1. ✅ **Done.** `internal/registry/filesource.go` — `FileSource` reusing
   `ParseManifest`, re-reading the file on every `Resolve`/`List`.
2. ✅ **Done.** `internal/git` — package-level `Clone(ctx, url, branch, token,
   dir) (*Repository, error)`, tested against a local bare-repo fixture.
3. ✅ **Done.** `internal/orchestrate` — fail-before-mutate composition:
   pre-flight `registry.Resolve` + `validate.Inputs` → `git.Clone` into
   `os.MkdirTemp` → workspace via `afero.NewBasePathFs` → `CheckoutBranch` →
   `domain.AddResource` → if changed: `Stage`/`Commit`/`Push` →
   `CreatePullRequest`; else "no changes". Temp dir always removed. Negative
   test proves an invalid input leaves the remote with **zero** new branches
   and zero PR-provider calls; a third test proves a `CreatePullRequest`
   failure after a successful `Push` still reports the pushed branch name.
4. ✅ **Done.** `internal/server` API: `GET /api/catalog` (DTO, no `Source`
   leak), `POST /api/scaffold` → `201 {prUrl, branch}` / `200 {changed:false}`
   no-op / `422` accumulated validation errors / `400` / `502 {error, branch}`
   PR-failed-after-push / `500`. Repo URL, base branch, env, token from server
   config, never the request. Injected `Scaffolder` interface; classification
   via `errors.Is` only.
5. ✅ **Done.** `cmd/weaved` — first runnable binary; test-driven
   `loadConfig` (7 tests, red shown first), all DI assembly in `main`/`run`,
   config via `WEAVE_*` env/flags (incl. `WEAVE_BITBUCKET_API`);
   `go build ./cmd/weaved` + `/health`, `/api/catalog`, `/` smoke all 200.

**Phase 2 — T-shirt-size spec modeling: ✅ complete.** Delivered red-first in
four steps: (1) registry `options` schema (`OptionSpec{Value, Label,
Description, ExpandsTo}`); (2) `validate` choice expansion + the three
sentinels; (3) domain end-to-end proof (no production change needed — the
virtual-choice design meant `AddResource` consumed the expansion unchanged;
the test pins it); (4) server DTO `options`/`managedByChoice` + 422/500
mapping.

**v1 ship (2026-07-06): ✅ complete.** Embedded wizard (`web/index.html`),
`internal/demo` + `weaved -demo`, the e2e capstone test, docs sync, and
`.claude/skills/` for agent succession. v1 assumption: the target repo is
already scaffolded (`terraform/env/<env>/` exists); Day-1 init via API is on
the roadmap.

## Rules of engagement

1. **Strict TDD.** Every new endpoint, git operation, and registry source is
   driven by a failing test first. Red → Green, and show the red.
2. **Fail-before-mutate.** Fully validate against the module spec before
   cloning, branching, or writing. A rejected request touches nothing; any
   failure before `Push` leaves the remote untouched by construction
   (clone-to-temp).
3. **No global variables.** Registry, git credentials, providers, and logger
   are passed explicitly via dependency injection; assembly only in `main`.
4. **Do not modify the engine core** (`internal/hcl`, `internal/domain`,
   `internal/fs`, `internal/validate`) unless a task explicitly requires it.
5. **Keep the docs honest.** README/ARCHITECTURE/this file must describe what
   *is*, clearly marking targets as targets. Doc drift already cost an audit
   cycle once.
