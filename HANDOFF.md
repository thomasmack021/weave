# HANDOFF — living turn-by-turn state

> **Protocol (for every agent session):** This file is the single source of
> truth for "where were we." It is rewritten at the **end of every turn**.
> A fresh session should: (1) read this file, (2) read
> `.claude/skills/weave-onboard` and `AGENT_HANDOVER.md` for the standing
> rules, (3) verify the *Verified state* below by running the commands in
> `.claude/skills/weave-verify`, (4) execute only the *Next step* — respecting
> its gate — and (5) rewrite this file before ending the turn. Do not trust
> any other doc over this one for turn-level state.

## Verified state (2026-07-06, independently re-verified by a fresh session)

- **Weave v1 is SHIPPED**, and a fresh session re-ran the full
  `weave-verify` playbook from scratch: `go build ./...`, `go vet ./...`,
  `gofmt -l internal cmd web` all clean; `go test ./... -count=1`: **all 11
  test-bearing packages ok, 79 test functions** (domain, fs, git, hcl,
  orchestrate, pipeline, registry, server, validate, demo, cmd/weaved;
  `web` has no tests) — exactly matching the state recorded at ship time.
- The `internal/demo` **end-to-end capstone** passes: production graph
  assembled exactly as `cmd/weaved` does, driven through the real HTTP API —
  fail-before-mutate proven (422 ⇒ zero new remote branches) and the happy
  path proven (choice expansion in the pushed branch's tfvars, PR URL serves
  a page, no virtual-key leakage).
- Live smoke of the actual binary (`weaved -demo`, rebuilt fresh) verified:
  /health 200, wizard served (`<title>Weave` present), catalog contains
  `cloud-run`, 422 on unknown size `galactic`, 201 with
  `{branch: weave/add-smoke-test, prUrl}` whose PR page itself serves 200.
- Git state: branch `main`, working tree **clean**, single root commit
  `4aeb719` tagged **v1.0.0**. No remote is configured — pushing to
  GitHub/Bitbucket is the user's call.

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

1. **Publish to GitHub** — done in this session: module renamed to
   `github.com/thomasmack021/weave` (matches the GitHub account so
   `go install .../cmd/weaved@latest` works), public repo, `main` +
   `v1.0.0` tag pushed.
2. **Day-1 workspace scaffolding via the API** — bootstrap
   `terraform/env/<env>/` in the target repo through the same
   fail-before-mutate PR loop.
3. **Additional PR providers** — GitHub, GitLab, Bitbucket Server/DC behind
   `git.PullRequestProvider`; selection via server config, never the request.
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
