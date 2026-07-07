# HANDOFF — living turn-by-turn state

> **Protocol (for every agent session):** This file is the single source of
> truth for "where were we." It is rewritten at the **end of every turn**.
> A fresh session should: (1) read this file, (2) read
> `.claude/skills/weave-onboard` and `AGENT_HANDOVER.md` for the standing
> rules, (3) verify the *Verified state* below by running the commands in
> `.claude/skills/weave-verify`, (4) execute only the *Next step* — respecting
> its gate — and (5) rewrite this file before ending the turn. Do not trust
> any other doc over this one for turn-level state.

## Verified state (2026-07-07, post-v1 session in progress)

- **Weave v1 is SHIPPED and PUBLISHED**, now on
  `github.com/thomasmack021/weave` (public). Three post-v1 gates are fully
  done (publish + Day-1 workspace scaffolding + multi-provider PRs) and the
  fourth (Postgres/RBAC) has its **foundation** done. Full `weave-verify`
  playbook green: `go build ./...`, `go vet ./...`, `gofmt -l internal cmd
  web` all clean; `go test ./... -count=1`: **all 12 test-bearing packages
  ok, 107 test functions** (79 at ship; +12 Day-1 init; +11 PR providers;
  +5 store unit). Plus **3 testcontainers integration tests** green under
  `go test -tags=integration ./internal/store/` (Docker; real Postgres 16).
- The `internal/demo` **end-to-end capstones** pass: `TestEndToEnd_DemoLoop`
  (Day 2) and the new `TestEndToEnd_WorkspaceInit` (Day 1) both drive the real
  production graph through the real HTTP API — fail-before-mutate proven
  (rejected request ⇒ zero new remote branches) and happy paths proven
  (expanded/injected values in the pushed branch, PR page serves 200, no
  leakage).
- Live smoke of the actual binary (`weaved -demo`) verified via curl AND the
  real browser wizard: /health 200, wizard served, catalog contains
  `cloud-run`; `/api/scaffold` 422 on unknown size + 201 happy path;
  `/api/workspace` 200 no-op (seeded project) + 201 (new project, branch
  `weave/init-dev`, PR page 200) + 422 (missing projectId). The wizard's
  "Set up the workspace" link drives the Day-1 201 and no-op screens end to
  end.
- Git state: branch `main`, remote `origin` =
  `github.com/thomasmack021/weave`, `v1.0.0` tag + GitHub release pushed.
  **Uncommitted:** the Day-1 workspace-scaffolding change (this turn) — being
  committed now.

## What shipped in v1 (the ship session, 2026-07-06)

1. **Phase 2 completed** (steps 3+4 after the earlier 1+2):
   - Step 3 — domain e2e choice test
     (`TestService_AddResource_ChoiceExpandsEndToEnd`): passed immediately as
     predicted (virtual-choice design needs zero domain changes); the test
     pins expansion-in-files + no-leak.
   - Step 4 — server: catalog DTO `options` (value/label/description only)
     + `managedByChoice` flag (so the wizard hides expansion targets — the
     mapping itself never reaches the browser); `ErrUnknownChoice`/
     `ErrChoiceConflict` → 422; `ErrSpecInvalid` explicitly excluded → 500
     (both directions pinned by tests). All red-first.
2. **Embedded web wizard** (`web/index.html`): single-file vanilla JS, no
   build step. Catalog → configure (choice option cards; managed inputs
   hidden; required checks) → review (business labels) → submit; renders 422
   lists, the 502 pushed-but-no-PR seam, idempotent no-op, and the success
   screen with PR link.
3. **Demo mode**: `internal/demo` (`Setup(dir)`) — bare workspace repo seeded
   on `main` by the real `domain.Scaffold`, example choice-bearing
   `spec.yaml` (cloud-run sizes + bucket tiers), in-process fake Bitbucket
   (Cloud PR endpoint + a page per PR); `weaved -demo` flag (config
   requireds skipped, tested). Never imported by production code.
4. **Docs sync**: README rewritten as the product's public face (pitch,
   30-second demo quickstart, guarantees, config table, API table, roadmap);
   ARCHITECTURE updated (choice layer complete, wizard, demo, DTO/status
   mapping); AGENT_HANDOVER updated to v1 state; LOCAL_TESTING rewritten
   around demo mode.
5. **Succession skills** in `.claude/skills/`: `weave-onboard` (north star,
   architecture map, invariants, working agreement, session protocol),
   `weave-verify` (verification playbook incl. live smoke + what green must
   include), `weave-extend-catalog` (spec/choice authoring guide + when Go
   changes are actually needed).
6. **Repository initialized** (`git init -b main`, `.gitignore`, everything
   committed as v1.0.0).

Note on process: the user explicitly collapsed the per-step approval gates
for this session ("ship version 1 … end2end is the main goal"); red→green
TDD was still followed within each step (reds shown for server DTO/status
work; the domain test's immediate pass was itself the specified proof).

## Next steps — approved gates (user decision, 2026-07-07 session)

The user explicitly approved these four work items (in the agent's chosen
execution order; each still gets red-first TDD and honest verification):

1. ✅ **DONE — Publish to GitHub**: module renamed to
   `github.com/thomasmack021/weave` (matches the GitHub account so
   `go install .../cmd/weaved@latest` works), public repo, `main` +
   `v1.0.0` tag + a GitHub release pushed.
2. ✅ **DONE — Day-1 workspace scaffolding via the API**:
   `orchestrate.InitWorkspace` (clone → branch `weave/init-<env>` →
   `domain.Scaffold` → shared `publish` tail) behind `POST /api/workspace`
   (`{projectId, statePrefix?}`; statePrefix derived `weave/<env>` when
   omitted so the developer never sees Terraform plumbing). Wizard gained a
   "Set up the workspace" link. All red-first; proven e2e by
   `demo.TestEndToEnd_WorkspaceInit` and the live browser wizard.
3. ✅ **DONE — Additional PR providers**: `GitHubProvider`, `GitLabProvider`,
   `BitbucketServerProvider` join the Bitbucket Cloud `HTTPProvider`, all over
   a shared `postPRJSON` spine. Selection via `WEAVE_PR_PROVIDER` (validated
   at config time; `newPRProvider` factory in `main`), never the request.
   Provider-aware `WEAVE_PR_API` defaults; legacy `WEAVE_BITBUCKET_*` env vars
   still accepted. All red-first (httptest fakes per provider).
4. 🚧 **PostgreSQL sessions + multi-tenant use-case RBAC** — user-approved,
   design agreed and recorded in `DESIGN.md` (identity = trusted proxy header
   → Entra + solo dev; **hybrid** authz = DB members OR Entra group grants;
   per-use-case repo config + credentials behind a `CredentialStore`).
   **Foundation DONE this session** (`internal/store`): pgx `PostgresStore`
   behind a `Store` interface, embedded `golang-migrate` migrations, pure
   `EffectiveRole`, `CredentialStore`/`StaticCredentialStore`, unit +
   testcontainers integration tests — all red-first, all green. **NOT wired to
   any endpoint yet** (v1 single-tenant config path untouched).
   **← NEXT increments:** (a) `Authenticator` middleware (proxy-header +
   static) + PostgreSQL session issue/verify on the HTTP boundary; (b)
   use-case-scoped `/api/*` + orchestrator resolving per-use-case repo config &
   credentials from the `Store`/`CredentialStore` + RBAC checks + admin
   management endpoints. Both are their own steps; keep the foundation's
   reversibility until (b) deliberately replaces the global config path.

**Product context from the user (shapes the RBAC/registry data model):**
admins onboard the source repos that contain the IaC modules; developers
use Weave to scaffold those modules into their CD-pipeline repo; a GitOps
operator (ArgoCD at the user's company) watches that repo and applies after
merge. So "CI applies after review" generalizes to GitOps pull-based
deployment, and the future data model is: use case ↔ target repo
(ArgoCD-watched), admin-onboarded module source repos, developers scoped to
their use cases.

Still open, not approved: Authentication/SSO; Git/HTTP-backed dynamic
registry; per-request attribution + token rotation.

## Standing working agreement (unchanged)

- Strict TDD, red → green, show the red; stop at gates the user sets — a
  resume prompt alone is never approval (sessions 12–14 precedent).
- Fail-before-mutate, DI/no-globals, docs honest in the same turn.
- The 422/500 fault-attribution firewall and the DTO no-leak rules are
  invariants; their tests must never be weakened.
- End every turn by rewriting this file and giving the user a resume prompt.

## Resume prompt (for the next session)

All four tasks the user approved on 2026-07-07 are done and pushed to
`github.com/thomasmack021/weave` (through commit `1e054db`): publish, Day-1
workspace scaffolding (`POST /api/workspace`), the GitHub/GitLab/Bitbucket
Server PR providers, and the **Gate 1 RBAC/sessions foundation**
(`internal/store` + `DESIGN.md`). Working tree clean; nothing half-done.

The Gate 1 foundation is deliberately **not wired to endpoints**. The next two
increments are queued (see the task list / roadmap items 4a and 4b above), and
their design is already locked in `DESIGN.md` §8 — so they do **not** need
re-approval, only execution:

1. **Increment 2 — identity**: the pluggable `Authenticator`
   (proxy-header + static dev backend) → `store.Principal`, plus PostgreSQL
   session issue/verify, injected into `server.New`. No endpoint enforcement
   yet.
2. **Increment 3 — enforcement**: use-case-scoped `/api/*`; the orchestrator
   resolves per-use-case repo config + credentials from
   `store.Store`/`CredentialStore` instead of global config; every action
   RBAC-checked; admin endpoints for use cases / memberships / group grants;
   wizard use-case selector.

Start with `weave-onboard`, verify with `weave-verify` (now includes
`go test -tags=integration ./internal/store/`, Docker required), then pick up
increment 2 red-first.
