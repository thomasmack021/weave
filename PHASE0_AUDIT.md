# Phase 0 — Product & Workflow Audit

*Author: Claude Fable, acting master developer/architect. Written against the actual code
(verified 2026-07: `go build ./...`, `go vet ./...` clean; all packages' tests passing —
43 test functions), not against the docs. Where the docs and the code disagree, I say so.*

---

## 1. Is the problem real? — Yes, and the framing is sharp.

The two-persona tension (app teams want velocity without learning IaC; platform teams
cannot scale hand-holding or tolerate raw Terraform from hundreds of teams) is a real,
well-documented enterprise failure mode. The three observed coping behaviors — copy-paste
Terraform that's subtly wrong, ticket queues, or paralysis — match what actually happens
in large orgs. I have no quarrel with the problem statement.

**Is the PR the right lever?** Yes — and I want to defend this deliberately, because it's
the design decision most tempting to "improve" away. The PR boundary:

- reuses the review muscle and audit trail the org already trusts (no new approval system
  to certify);
- keeps Weave itself **out of the blast radius** — Weave never holds apply credentials,
  so a compromised or buggy Weave can at worst open a bad PR that a human rejects;
- makes every change reversible by construction (revert the merge).

**But name the friction honestly.** Two things the current design does *not* solve:

1. **The developer still waits on a human.** Weave shrinks *time-to-correct-PR* (from
   days of tickets to seconds), not *time-to-approved-infrastructure*. If platform-team
   review latency is days, the developer experience is still days. This is a product
   risk, not a code risk — see §5. The right posture: measure PR-open→merge time as the
   primary product KPI from day one, and treat auto-merge policies for low-risk changes
   (e.g. bucket with defaults) as a future platform-team-controlled dial, never a Weave
   default.
2. **Branch/PR concepts leak slightly into the developer's world.** The developer must
   understand "your change is a pull request awaiting review." I judge this acceptable
   leakage — PRs are business-adjacent vocabulary by now — but the UI must present only
   "request submitted, under review" + a link, never branch names, diffs-by-default, or
   HCL. The moment the wizard shows generated Terraform "for transparency," we've failed
   the North Star.

**Verdict: problem real, mechanism sound, two frictions named and manageable.**

---

## 2. Mechanism chain verification — link by link, against the code

The kickoff describes a four-link chain. Current state of each:

### Link 1 — "Developer picks outcomes, not variables" ❌ **weakest link, unmodeled**

The data model does not support the promised UX. `internal/registry/registry.go` defines
`InputSpec{Name, Type, Required, Default, Validation{Pattern, MaxLength}, TfvarsKey,
ModuleArg}` — free-form typed inputs with regex/length rules. There is **no enum, no
options list, no t-shirt-size mapping** anywhere in the tree. Today a developer would
supply `region=europe-west1` and `service_name=api` as raw strings — that *is* thinking
like an infra engineer, just without HCL syntax.

This is the single largest gap between the product promise and the code. What's needed
(later gate, after the skeleton): an options/choices extension to `InputSpec` where the
platform team defines business-labeled choices (`"Small — for prototypes"`) that expand
to one-or-many concrete variable values (`cpu=1, memory=512Mi`). The expansion must live
in the **spec**, owned by the platform team — never in Weave code. Until this exists,
Weave is honestly "Terraform with guardrails and less syntax," not "business language in."

### Link 2 — "Weave translates choices into platform-approved Terraform" ✅ **proven**

This is real and it is the crown jewel. `internal/hcl` does comment-preserving AST
manipulation with an idempotency guard *before* any mutation (`module.go`); generated
blocks are **only** module blocks pinned to `spec.Source?ref=spec.Version` — hand-rolled
resources are structurally impossible. `internal/validate` is strict (unknown inputs
rejected, all failures accumulated via `errors.Join`, typed sentinels). `internal/domain`
composes them with resolve-and-validate **before** any write (`resource.go`). 33 of the
43 tests live under these core packages. **Treat as load-bearing; do not modify.**

### Link 3 — "Weave opens a PR, not an apply" ⚠️ **half-proven: units exist, chain doesn't**

- `internal/git` has `Committer{Stage, Commit, CheckoutBranch, Push}` and a tested
  Bitbucket Cloud `HTTPProvider.CreatePullRequest` (`provider.go`). Green.
- **But nothing composes them.** No package outside `internal/git` imports it; same for
  `internal/domain`. The generate→branch→commit→push→PR loop exists only as disconnected
  parts.
- **Clone-to-temp does not exist.** `ARCHITECTURE.md` claims Weave "clones the target
  workspace repository to a temporary directory"; the code only has `PlainOpen` of an
  existing local path (`git.go`). This is a docs-say/code-doesn't contradiction, and
  clone-to-temp is not optional polish — it is *the* isolation mechanism that makes
  fail-before-mutate hold across the git boundary (see §3).
- **There is no `main`.** No `cmd/`, no `package main` anywhere. The system cannot be
  run. The loop is theoretical today.

### Link 4 — "State & RBAC in PostgreSQL" ⬜ **greenfield, correctly deferred**

No `pgx`, no migrations, no `internal/db`. Nothing to audit. Deferring this is right:
the skeleton is stateless and the loop can be proven without it.

### Other doc/reality drift (schedule cleanup post-gate, do not trust these docs)

- `AGENT_HANDOVER.md` claims 51 tests; 43 is the real count (kickoff has it right).
- Root `README.md` still documents the deleted CLI (`weave init/add/list`,
  `go build ./cmd/weave`) — actively misleading to a new contributor.
- `archive/` contains only old markdown; the legacy CLI *code* was deleted, not parked.
- `internal/pipeline` is not an orchestrator despite the name — it is a YAML-step merger
  used by `domain.Scaffold`. The orchestration layer does not exist yet.

---

## 3. Fail-before-mutate audit

The invariant holds where it's implemented, with two seams to watch:

1. **Within domain: good, with a per-file caveat.** `AddResource` resolves the spec and
   validates all inputs before touching any file. But the resulting ChangeSet is written
   file-by-file via `WriteAtomic` (temp+rename) — atomic *per file*, not *per changeset*.
   A failure writing file 2 leaves file 1 changed. Harmless today (in-memory/local fs);
   **solved structurally by clone-to-temp**: if the workspace is a disposable temp clone,
   a partial write is discarded with the directory and the remote never sees it. This is
   why clone-to-temp is the load-bearing piece of Link 3, not a nicety.
2. **The push→PR window is the one genuinely risky seam.** After `Push` succeeds and
   before `CreatePullRequest` succeeds, a branch exists on the remote with no PR. The
   orchestrator must (a) treat this as a *recoverable, reportable* state — return the
   pushed branch name in the error so a human or retry can finish the job — and (b) use
   collision-free branch names (`weave/add-<module>-<name>-<shortid>`) so retries never
   fight a stale branch. Remote garbage is bounded to unreferenced branches; acceptable.

Everything upstream of `Push` must be strictly ordered: **validate → clone temp →
checkout branch → apply changeset → commit** — a rejected request must die at step 1
having touched nothing, and any failure before `Push` must leave the remote untouched
by construction.

---

## 4. Recommended walking skeleton (next gate — not built yet)

The thinnest slice that proves the *whole* loop with real code and zero mocks in the
production path: **one module type → validate → generate → branch → commit → push →
PR created.** Proven locally (bare repo as push remote + `httptest` Bitbucket stub);
pointing it at real Bitbucket Cloud is then pure configuration.

Concretely:

1. **`internal/registry/filesource.go` — `FileSource`** reading a `spec.yaml` path and
   reusing the existing `ParseManifest`. Re-read on every `Resolve`/`List` call (a file
   read is cheap): this makes "platform engineer edits the spec and the catalog reflects
   it **without redeploy**" — a Definition-of-Done bullet — true from day one, and it is
   the honest stepping stone to the HTTP/Git-backed registry (same interface, different
   source).
2. **`internal/git`: add `Clone(ctx, url, branch, token, dir) (*Repository, error)`**
   beside `Open`. The `Committer` interface is untouched.
3. **New `internal/orchestrate` package** — the composition layer that finally wires the
   proven units, in fail-before-mutate order:
   pre-flight `registry.Resolve` + `validate.Inputs` (no I/O side effects) → `git.Clone`
   into `os.MkdirTemp` → workspace via `afero.NewBasePathFs` → `CheckoutBranch` →
   `domain.AddResource` → if `ChangeSet.Changed()`: `Stage`/`Commit`/`Push` →
   `CreatePullRequest`; else report "no changes" and stop. Temp dir always removed.
   All dependencies injected (registry, cloner, PR provider, config).
4. **API on `internal/server`**: `GET /api/catalog` (list specs, JSON) and
   `POST /api/scaffold {moduleType, instanceName, inputs}` → `201 {prUrl, branch}` or
   `422` with the accumulated validation errors. Repo URL, base branch, env, and token
   come from **server config**, never the request — the skeleton has one target repo.
5. **`cmd/weaved/main.go`** — the first runnable binary: flags/env vars
   (`WEAVE_LISTEN, WEAVE_SPECS, WEAVE_REPO_URL, WEAVE_BASE_BRANCH, WEAVE_GIT_TOKEN,
   WEAVE_BITBUCKET_REPO, WEAVE_ENV`), all DI assembly in `main`, no globals.
6. **Explicit skeleton assumption:** the target repo is already scaffolded
   (`terraform/env/<env>/` exists). Scaffold-on-demand (Day-1 init via API) is deferred.
7. **UI:** curl / a static HTML form is sufficient. Vue comes after the loop is proven.

**Strict TDD sequence (each step red-first):**
FileSource tests → `git.Clone` against a local bare-repo fixture → orchestrator
integration test (bare repo as remote + `httptest` Bitbucket; **the key negative test:
an invalid input must leave the remote with zero new branches**) → API handler tests
(fake orchestrator behind a small interface) → `go build ./cmd/weaved` + `/health` smoke.

Deliberately **out** of the skeleton: PostgreSQL/RBAC, auth, sessions, Vue, dynamic
HTTP registry, t-shirt-size modeling. Each gets its own gate afterward, roughly in the
kickoff's order — with one amendment: I would schedule **t-shirt-size spec modeling
immediately after the skeleton** (before Postgres), because it is the product's core
differentiator and currently pure vaporware (§2 Link 1).

---

## 5. Risks & product-market-fit concerns (beyond code)

1. **The t-shirt-size gap is a PMF risk, not a backlog item.** Until business options
   are modeled, the demo story to an app team is "a form that asks for the same
   variables, minus HCL syntax." Real, but not the North Star. Prioritize accordingly.
2. **Review latency will dominate perceived value.** If the platform team takes three
   days to approve PRs, app teams will not credit Weave for the seconds it saved them.
   Instrument PR-open→merge from the first real PR; make the platform team own that
   number as part of adopting Weave.
3. **Single service-account PAT** authors every commit and PR. Fine for the skeleton;
   before production, per-request attribution ("requested by …" in PR body/commit
   trailer) and token rotation need answers, or the audit trail the PR mechanism exists
   to provide is half-blind about *who* asked.
4. **Bitbucket Cloud concentration** is acceptable (it's the org's target) and the
   `PullRequestProvider` interface keeps the exit door open. No action needed now.
5. **Doc drift is an active hazard**: README describes a deleted CLI, ARCHITECTURE
   describes a clone flow that doesn't exist, HANDOVER has a wrong test count. Any new
   contributor (human or agent) trusting them loses a day. Cleanup is cheap — do it in
   the same PR-sized change as the skeleton kickoff.

---

## 6. Gate

Phase 0 is delivered: problem framing affirmed with frictions named, every mechanism
link verified against code, fail-before-mutate audited with its two seams identified,
and the walking skeleton specified concretely with its TDD sequence.

**Awaiting approval to proceed to the walking skeleton (§4).** Nothing has been built;
no source file was modified by this audit.
