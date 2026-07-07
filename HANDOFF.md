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
  `github.com/thomasmack021/weave` (public), and two post-v1 gates are done
  (publish + Day-1 workspace scaffolding). Full `weave-verify` playbook green:
  `go build ./...`, `go vet ./...`, `gofmt -l internal cmd web` all clean;
  `go test ./... -count=1`: **all 11 test-bearing packages ok, 91 test
  functions** (was 79 at ship; +12 for Day-1 init: 4 orchestrate + 7 server +
  1 demo e2e).
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
3. **Additional PR providers** — GitHub, GitLab, Bitbucket Server/DC behind
   `git.PullRequestProvider`; selection via server config, never the request.
   **← NEXT.**
4. **PostgreSQL sessions + use-case RBAC** — `pgx`, `golang-migrate`,
   testcontainers (Docker verified available); developers see only their
   use cases.

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
