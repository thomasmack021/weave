---
name: weave-onboard
description: Read FIRST when starting any work on the Weave codebase. Explains the product's north star, the architecture map, the non-negotiable invariants, the TDD working agreement, and the session protocol (HANDOFF.md). Use when onboarding to this repo, before planning any feature, refactor, or fix.
---

# Weave onboarding (for AI agents and humans)

## The north star — judge every decision against this sentence

> Make it as easy as possible for application teams in large organizations to
> ship their apps to the cloud — without learning Terraform, IaC, or the
> platform's internal plumbing — while the dedicated platform team keeps full
> control of what "good" infrastructure looks like.

If a change makes the app developer's path easier AND keeps the platform team
in control → probably right. If it leaks IaC concepts to the developer
(HCL, branch names, raw variables) or takes control from the platform team →
probably wrong.

## Session protocol

1. Read `HANDOFF.md` — the single source of truth for "where were we". It is
   rewritten at the end of every working session; do that too.
2. Read `AGENT_HANDOVER.md` for the precise current state and roadmap.
3. **Verify before trusting**: run `go build ./... && go vet ./... &&
   go test ./... -count=1` and compare against HANDOFF's recorded state
   (see the `weave-verify` skill). If docs and code disagree, trust the code
   and fix the docs.
4. Respect gates: if HANDOFF says a step is GATED, do not proceed without the
   user's explicit approval. A resume prompt alone is never approval
   (precedent: sessions 12–14 held three turns rather than assume).

## Architecture map (what lives where)

| Package | Role | Caution level |
|---|---|---|
| `internal/hcl` | Comment-preserving Terraform AST editing; idempotency guarded before mutation | **Load-bearing core — change only with overwhelming justification** |
| `internal/domain` | `Scaffold` (Day 1) / `AddResource` (Day 2) → `ChangeSet`; resolve-and-validate before any write | **Load-bearing core** |
| `internal/fs` | afero workspace under `terraform/env/<env>/`; `WriteAtomic` (per file) | **Load-bearing core** |
| `internal/validate` | Strict input validation + the virtual-choice expansion layer (see below) | **Core; the 422/500 sentinel taxonomy lives here** |
| `internal/pipeline` | `pipeline.yaml` step merger used by Scaffold — NOT an orchestrator despite the name | Core |
| `internal/registry` | `spec.yaml` parsing, `ModuleRegistry` interface, `FileSource` (re-reads per call → spec edits live without redeploy), `FakeRegistry` for tests | Core |
| `internal/git` | `Committer` (Stage/Commit/CheckoutBranch/Push), package-level `Clone` (the safety boundary), four `PullRequestProvider`s (Bitbucket Cloud/Server, GitHub, GitLab) over one `postPRJSON` spine | Core |
| `internal/orchestrate` | Fail-before-mutate composition: resolve+validate → clone temp → branch → AddResource → stage/commit/push → PR | The write path |
| `internal/server` | HTTP API + embedded wizard serving; classification-only error mapping (`errors.Is`), no validation logic | API boundary |
| `internal/demo` | Self-contained local demo env + THE e2e capstone test; never imported by production code | Test/demo only |
| `cmd/weaved` | Assembly-only main + pure `loadConfig`; `-demo` flag | Entrypoint |
| `web/` | Single-file vanilla-JS wizard, embedded via `embed.FS`, no build step | Frontend |

## Non-negotiable invariants (all pinned by tests — keep them pinned)

1. **Weave never runs Terraform and never holds apply credentials.** Its only
   write channel is a pushed branch + PR. CI applies after human review.
2. **Fail-before-mutate.** Validation completes before clone/branch/write.
   Clone-to-temp is the git safety boundary: any failure before Push leaves
   the remote untouched by construction. The e2e test asserts a rejected
   request creates zero remote branches — never weaken that test.
3. **Only golden modules.** Generated main.tf contains only module blocks
   pinned to `spec.Source?ref=spec.Version`. Hand-rolled resources must stay
   structurally impossible.
4. **The developer never sees Terraform.** The wizard shows business language
   only. The catalog DTO must never leak `Source` or option `expandsTo`
   (tests enforce this — extend them when extending the DTO).
5. **Choice inputs are virtual.** A `type: choice` input never emits a value;
   its selected option's `expandsTo` (spec-owned, platform-defined) supplies
   values for other declared inputs. Conflicting direct values are rejected
   (`ErrChoiceConflict`).
6. **Fault attribution is sacred.** Caller faults (`ErrUnknownChoice`,
   `ErrChoiceConflict`, `ErrMissingRequired`, …) → 422. Platform spec bugs
   (`ErrSpecInvalid`) → 500, and the error chain must NEVER wrap a
   caller-fault sentinel (use `%v` not `%w` when wrapping a coercion error
   inside a spec-bug error — there is a test pinning this).
7. **No globals; DI everywhere.** Assembly only in `cmd/weaved`'s
   `main`/`run`. Server config (repo URL, token, env, base branch) never
   comes from the HTTP request.

## Working agreement (from the user, standing)

- **Strict TDD**: red first, show the failing output, then green. A test that
  passes immediately can itself be the deliverable when it pins an expected
  property (precedent: the domain choice e2e test).
- Stop at gates the user sets; do not run ahead.
- Keep docs honest **in the same change** as the code (doc drift once cost a
  full audit cycle).
- Match the existing test style: table-driven, sentinel matching via
  `errors.Is`, fixtures named `xxxSpec()`, helpers with `t.Helper()`.

## Related skills

- `weave-verify` — the full verification playbook with expected counts.
- `weave-extend-catalog` — how to add modules, inputs, and t-shirt choices.
