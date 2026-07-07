---
name: weave-verify
description: Verify the Weave codebase end-to-end - build, vet, format, full test suite, and the live demo-mode smoke test. Use before claiming anything works, after any change, when resuming a session to confirm HANDOFF.md's recorded state, or when the user asks "does it still work?".
---

# Verifying Weave

## 1. Static + unit suite (always)

```sh
go build ./... && go vet ./...
gofmt -l internal cmd web        # must print nothing
go test ./... -count=1
```

Expected: every package `ok` (12 Go packages with tests + `web` with none):
domain, fs, git, hcl, orchestrate, pipeline, registry, server, store,
validate, demo, cmd/weaved. `internal/store`'s default run is its pure unit
tests (RBAC decision, role ordering, token hashing).

## 1b. Store integration suite (when `internal/store` changed; Docker required)

```sh
go test -tags=integration ./internal/store/    # spins up Postgres via testcontainers
```

Expected: `ok` with 3 passing tests (user/use-case lifecycle, hybrid RBAC
filtering, session lifecycle). These are gated behind `//go:build integration`
so the default suite stays fast and Docker-free; run them after any change to
the schema, migrations, or `PostgresStore`.
The suite includes `internal/demo`'s end-to-end capstone, which drives the
real production graph (FileSource → orchestrator → go-git → Bitbucket HTTP
provider) through the real HTTP API against a local bare repo — so a fully
green suite already proves the loop. `-count=1` matters: never accept cached
results as verification.

## 2. Live binary smoke (when the server, wizard, config, or demo changed)

```sh
go build -o /tmp/weaved ./cmd/weaved
/tmp/weaved -demo -listen 127.0.0.1:18089 &
sleep 1.5
curl -s http://127.0.0.1:18089/health                     # {"status":"ok"}
curl -s http://127.0.0.1:18089/ | grep -c '<title>Weave'  # 1 (wizard served)
curl -s http://127.0.0.1:18089/api/catalog | grep -c cloud-run   # ≥1
# fail-before-mutate: unknown t-shirt size → 422
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://127.0.0.1:18089/api/scaffold \
  -H 'Content-Type: application/json' \
  -d '{"moduleType":"cloud-run","instanceName":"x","inputs":{"service_name":"x","image":"i","size":"galactic"}}'
# happy path → 201 with prUrl + branch
curl -s -X POST http://127.0.0.1:18089/api/scaffold \
  -H 'Content-Type: application/json' \
  -d '{"moduleType":"cloud-run","instanceName":"smoke-test","inputs":{"service_name":"smoke-test","image":"gcr.io/x/y:1","size":"small"}}'
# Day 1 init: re-init of the demo repo with its seeded project → 200 no-op
curl -s -X POST http://127.0.0.1:18089/api/workspace \
  -H 'Content-Type: application/json' -d '{"projectId":"acme-demo-project"}'   # {"changed":false}
# Day 1 init: a different project → 201 with prUrl + branch weave/init-dev
curl -s -X POST http://127.0.0.1:18089/api/workspace \
  -H 'Content-Type: application/json' -d '{"projectId":"acme-new-project"}'
# Day 1 init: missing projectId → 422 (caller fault, never 500)
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://127.0.0.1:18089/api/workspace \
  -H 'Content-Type: application/json' -d '{}'
kill %1
```

The 201 response's `prUrl` must itself serve HTTP 200 (the fake Bitbucket PR
page). Note the Day 1 no-op vs. 201 distinction: re-init is a no-op only when
the request matches what the base branch already carries (same `projectId`);
a different `projectId` is a legitimate change, not a no-op.

## 3. What "green" must include (do not rationalize away)

- The e2e negative test (`TestEndToEnd_DemoLoop`): a 422 must leave the demo
  remote with **zero** new branches. If this fails, fail-before-mutate is
  broken somewhere between the API and git — treat as release-blocking.
- The Day 1 e2e test (`TestEndToEnd_WorkspaceInit`): a fresh repo → 201 with
  the scaffold pushed (injected `project_id` present) and the PR page serving
  200; the already-scaffolded demo repo → 200 no-op that opens no branch.
  Same fail-before-mutate boundary as Day 2, release-blocking if broken.
- The leak tests: catalog responses must contain no `Source`, no
  `expandsTo`, no expansion values; generated files must not contain a
  choice input's name.
- The sentinel-firewall test (`TestInputs_ChoiceSpecBugNeverMapsToCallerFault`
  and `TestScaffoldSpecBugReturns500Not422`): spec bugs are 500, never 422.

## 4. Reporting

Report counts honestly (packages ok, test functions, live-smoke statuses).
If anything fails, report the failing output verbatim — never summarize a
failure as "mostly passing". Update `HANDOFF.md`'s Verified state section
with what you actually observed.
