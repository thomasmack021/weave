# Agent Handover

**Read this in full before writing a single line of code in this repository.**

This project was built under a strict, deliberate methodology. The patterns are
not incidental — they are the product, as much as the binary is. Violating them
will produce code that gets reverted. This document tells you how to contribute
without breaking them.

---

## 1. Strict TDD: Red → Green, always

**No production code is written without a failing test that demands it.** Every
single one of the 51 tests in this repo was written before its implementation.

The cycle, per unit of work ("step"):

1. **Red phase.** Write the interface/stub and the test(s). The stub returns a
   "not implemented" error (or is a trivial placeholder). Run the suite and
   **confirm the new tests fail** for the *right reason*. Then **stop.**
2. **Green phase.** Implement the minimum to make the failing tests pass. Run the
   full suite. Report results. Then **stop.**

Do not skip the red phase. Do not implement two steps at once. Do not write
speculative code for a test that doesn't exist yet.

### Disclose "trivially-passing" reds — honesty over green checkmarks

If a test passes during the red phase (e.g. it only asserts `err != nil` and your
stub already returns an error), **say so explicitly** and explain why the test
isn't yet a real guard. We caught and flagged several of these during the build
(`Stage_FileNotFound`, the `--set` malformed-input guards, and a `ttyPrompter`
stub that was being bypassed by `--yes`). Surfacing them — and then writing the
test that genuinely drives the implementation — is *required*, not optional. A
test you cannot see fail is not protecting anything.

---

## 2. The "fail-before-mutate" rule

A command must do **all** fallible work (resolution, validation, prompting)
**before** it writes anything to the workspace. A failed command must leave the
workspace **byte-for-byte unchanged**.

- Validate inputs and resolve modules up front.
- Only after everything that can fail has succeeded do you compute and write.
- Writes are atomic (temp file + rename) so partial writes are impossible.
- New behavior that can fail mid-operation **must** add a test that snapshots the
  workspace and asserts it is unchanged on the failure path.

See `ARCHITECTURE.md` for the read → compute → reconcile loop that enforces this.

---

## 3. Non-negotiable patterns

- **The domain does not know about git.** Never inject `git.Committer` into
  `domain.NewService`. The domain produces a `ChangeSet`; the CLI layer commits it
  via `commitChangeSet`. (Single Responsibility.)
- **No package-level globals and no `init()` in the CLI.** Dependencies go in
  `Deps`; flag values go in function-local option structs captured by the `RunE`
  closure. If you need new wiring, add a field to `Deps` and set it in
  `DefaultDeps()`.
- **All side effects sit behind a boundary interface** (`ModuleRegistry`,
  `fs.ReadWriter`, `git.Committer`, `Prompter`). Pure-core packages (`validate`,
  `hcl`, `pipeline`) must stay pure — no I/O.
- **Format/comment preservation.** Edit `main.tf` through `hcl` (hclwrite AST) and
  `pipeline.yaml` through `pipeline` (yaml.Node). Never template these as raw
  strings; never reformat the whole file.
- **Idempotency is a feature.** Any new mutation must reconcile by byte-comparison
  and be a no-op (no write, no commit) when nothing changed. Add an idempotency
  test for it.
- **Determinism.** Sort anything enumerated (e.g. registry `List`) so tests don't
  flake.
- **The CLI never runs Terraform.** It edits files and commits. Do not add a
  `terraform` exec path.
- **Test doubles live in `internal/cli/fakes_test.go`** (`_test.go`, so excluded
  from the production build): `recordingCommitter`, `spyRegistry`,
  `scriptedPrompter`. Reuse them.

---

## 4. Current project state

**Phases A–F are complete. 51 tests pass. `go build ./...` and `go vet ./...`
are clean.** The binary builds and runs.

```
?   cmd/weave            (thin main shell)
ok  internal/cli         13 tests   commands, parsing, prompter, E2E
ok  internal/domain       6         Scaffold, AddResource, error propagation
ok  internal/fs           3         atomic/env-scoped workspace
ok  internal/git          3         stage/commit, ErrNothingToCommit, author fallback
ok  internal/hcl          7         InsertModule, UpsertVariable (format-preserving)
ok  internal/pipeline     3         EnsureStep (comment-preserving)
ok  internal/registry    14         resolve/list, fakes
ok  internal/validate     2         typed input validation
                         ── 51 total
```

### What exists

- **Commands:** `weave init` (Day 1), `weave add` (Day 2), `weave list`, with
  persistent `--env`/`--repo`.
- **Engine:** domain orchestration → `ChangeSet`; atomic idempotent workspace;
  go-git committer; TTY-aware prompter with headless hard-fail.
- **Entrypoint:** `cmd/weave/main.go` + `cli.DefaultDeps()`.
- **One on-disk E2E** (`TestE2E_InitAndAdd_RealDisk`): real temp `git init`, real
  `OsFs`, runs `init → add`, asserts files on disk and exactly two real commits.

### How to run it

```sh
go build -o weave ./cmd/weave
go test ./...        # full suite
go vet ./...
```

---

## 5. The ONE deliberate stub — and the next major step

`DefaultDeps()` (in `internal/cli/app.go`) currently wires a **`FakeRegistry`**
pre-loaded with placeholder specs (`cloud-run`, `bucket`) via `defaultSpecs()`.
This is the **only** intentional placeholder in the codebase. It exists so the
binary is fully functional today.

**The next major step is to implement the real registry and swap it in:**

- Build a real `ModuleRegistry` that fetches the module manifest via an
  **authenticated HTTP GET against the Git provider's API** (not a `go-git`
  clone). Decisions already locked from the design phase:
  - HTTP GET against the Git API with an auth token.
  - Prompts are interactive on a TTY and hard-fail (exit 1) in headless/CI — this
    is already implemented in `ttyPrompter`; the registry must not reintroduce
    interactive behavior.
- It drops in **behind the existing `registry.ModuleRegistry` interface**. No
  changes to `domain` or `cli` should be required — only `DefaultDeps()` changes
  to construct the real registry instead of `FakeRegistry`.
- Caching (to avoid the one extra resolve that `weave add` performs — it resolves
  once to discover required inputs, and `AddResource` resolves again) belongs
  **in the registry implementation**, not by threading a pre-resolved spec
  through the domain.

Do this the same way everything else was built: **red first.** Write the registry
interface tests (happy path, auth failure, module-not-found, malformed manifest)
against a stub/`httptest` server, watch them fail, then implement.

---

## 6. Definition of done for any change

- New behavior was driven by a test that you watched fail first.
- Trivially-passing reds were disclosed.
- Fail-before-mutate holds (with a workspace-unchanged test on failure paths).
- Idempotency holds (with a test).
- `go build ./...`, `go vet ./...`, and `go test ./...` are all clean.
- The boundary/no-globals/domain-ignorant-of-git patterns are intact.

Welcome aboard. Keep it disciplined.
