# Architecture

This document records the structural decisions behind `weave` and the invariants
that must be preserved. Read it before changing any package boundary.

## Layering: Pure Core → Domain → I/O

The codebase is organized into three concentric rings. Dependencies point
**inward only**: I/O depends on Domain, Domain depends on Pure Core; never the
reverse.

```
            ┌──────────────────────────────────────────────┐
            │  I/O boundary (interfaces + adapters)         │
            │  registry · fs · git · cli · cmd/weave        │
            │   ┌──────────────────────────────────────┐   │
            │   │  Domain (orchestration)              │   │
            │   │  domain.Service → ChangeSet          │   │
            │   │   ┌──────────────────────────────┐   │   │
            │   │   │  Pure Core (functions)       │   │   │
            │   │   │  validate · hcl · pipeline   │   │   │
            │   │   └──────────────────────────────┘   │   │
            │   └──────────────────────────────────────┘   │
            └──────────────────────────────────────────────┘
```

### Pure Core — `internal/validate`, `internal/hcl`, `internal/pipeline`

Deterministic, side-effect-free functions. No filesystem, no network, no git.

- `validate` — typed validation of developer inputs against a module's `InputSpec`
  list (required/optional, types, patterns). Returns `map[string]cty.Value`.
- `hcl` — format-preserving manipulation of `main.tf` via `hclwrite` (AST), e.g.
  `InsertModule`, `UpsertVariable`. Idempotent: inserting an existing module is a
  no-op at the byte level.
- `pipeline` — comment-preserving edits to `pipeline.yaml` via `yaml.Node`
  (`EnsureStep`), injecting the project ID and state prefix.

Because these are pure, they are tested directly with table tests and no fakes.

### Domain — `internal/domain`

`domain.Service` orchestrates the pure-core functions into a **`ChangeSet`** — an
in-memory description of file changes (`Created`/`Updated`/`Unchanged`). It is the
only place that knows *what* a Day 1 / Day 2 operation means.

- `Scaffold(ctx, ScaffoldRequest) (ChangeSet, error)` — Day 1.
- `AddResource(ctx, AddResourceRequest) (ChangeSet, error)` — Day 2.

**The Domain is deliberately ignorant of version control.** `git.Committer` is
**not** injected into `NewService`. This is a Single-Responsibility decision: the
domain manipulates workspace state; committing that state is a separate concern
owned by the CLI layer. The domain depends on `fs.ReadWriter` (to read/write
workspace files) and `registry.ModuleRegistry` (to resolve module specs) — both
interfaces, both at the boundary.

### I/O boundary — interfaces + adapters

All side effects live behind small interfaces so the domain and CLI stay testable:

| Interface | Production adapter | Test double |
|---|---|---|
| `registry.ModuleRegistry` | `FakeRegistry` *(placeholder — see handover)* | `FakeRegistry`, `spyRegistry` |
| `fs.ReadWriter` | `*fs.Workspace` over `afero.Fs` | `afero.NewMemMapFs()` |
| `git.Committer` | `*git.Repository` (go-git) | `recordingCommitter` |
| `cli.Prompter` | `*ttyPrompter` | `scriptedPrompter` |

`internal/cli` wires these together into Cobra commands; `cmd/weave/main.go` is a
thin shell that builds production deps and executes the root command.

## The CLI dependency-injection pattern: no globals

Cobra code commonly leans on `init()` and package-level variables. **We do not.**
Both are banned because they make commands un-testable and order-dependent.

Instead:

- **Dependencies** live in a `Deps` struct (`internal/cli/app.go`): `Registry`,
  `FS`, `NewCommitter`, `Prompter`, and `In`/`Out`/`Err` streams.
- **Flag values** live in function-local option structs (`rootOptions`,
  `initOptions`, `addOptions`) created inside the command constructor and
  **captured by the `RunE` closure** — never global.
- `NewRootCommand(deps)` builds a fresh command tree per call. Tests construct a
  root with fakes and `SilenceUsage`/`SilenceErrors`, then drive it via
  `cmd.SetArgs(...)` + `Execute()`.
- Production wiring is isolated in `DefaultDeps()`: `afero.NewOsFs()`, a
  `git.Open`-backed committer factory, os streams, and a TTY-aware prompter
  (`term.IsTerminal` resolved once into an injected `isTTY` bool).

The shared commit helper, `commitChangeSet(committer, ws, cs, message)`, is
intentionally decoupled from `Deps` — it speaks only `git.Committer` and the
workspace, so the GitOps logic is reusable and unit-testable in isolation.

## The "read → compute → reconcile" loop

Every file mutation follows the same three-phase shape inside the domain:

1. **Read** existing content (if any). `readExisting` treats "not found" as
   `existed=false` rather than an error, so create and update share one path.
2. **Compute** the desired next content using pure-core functions (`hcl`,
   `pipeline`, baseline templates). No writes happen here.
3. **Reconcile** desired vs. existing via `reconcile(name, existing, existed, next)`:
   - not existed → write, `ActionCreated`
   - `bytes.Equal(existing, next)` → **no write**, `ActionUnchanged`
   - otherwise → write, `ActionUpdated`

This is what makes every command **idempotent**: a no-op run computes identical
bytes, reconciles to `Unchanged`, writes nothing, and (because nothing was
staged) produces no commit.

## Filesystem guarantees

- **Atomic writes.** `fs.Workspace` writes via a temp file + rename, so a crash
  mid-write can never leave a half-written `main.tf`/`tfvars`/`pipeline.yaml`.
- **Idempotent writes.** Byte-comparison in `reconcile` (above) means unchanged
  content is never rewritten and timestamps don't churn.
- **Env-scoped paths.** `Workspace.Path(name)` resolves to
  `terraform/env/<env>/<name>`, the single source of truth for where files live;
  the commit helper stages exactly these resolved paths.

## Git guarantees

- **Stage only what changed.** The commit helper filters the `ChangeSet` to
  `Created`/`Updated` entries and stages exactly those paths. We never `git add .`.
- **No empty commits.** `git.Repository.Commit` checks the worktree status and
  returns the `ErrNothingToCommit` sentinel when nothing is staged; the CLI
  swallows it as a benign no-op (exit 0).
- **Safe author fallback.** `git.Open` reads `user.name`/`user.email` from local
  config, falling back to `Weave CLI <weave@localhost>` so the tool never panics
  in a headless CI repo with no identity configured.

## The "fail-before-mutate" invariant

A command must perform **all** validation and resolution that can fail **before**
it writes anything. `AddResource` resolves the module and validates inputs up
front; the `add` command resolves the spec and prompts for/validates required
inputs before calling into the domain. Combined with atomic writes, a failed
command (unknown module, invalid input, missing required input in a headless
session) leaves the workspace **byte-for-byte unchanged**. This is asserted by
tests that snapshot the workspace and compare before/after.
