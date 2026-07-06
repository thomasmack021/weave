# Testing Weave Locally

## The 30-second way: demo mode

```sh
go run ./cmd/weaved -demo
```

This synthesizes a complete local environment (see `internal/demo`): a bare
git "workspace" repo seeded with the Day 1 scaffold, an example module
catalog with t-shirt-size choices, and an in-process fake Bitbucket. Open
<http://localhost:8080> and run the whole wizard → PR loop for real — the
branch, commit, and Terraform it produces are genuine; only the Bitbucket API
is faked.

Inspect what a submission actually produced (paths are printed at startup):

```sh
git clone /path/printed/at/startup/workspace.git /tmp/inspect
git -C /tmp/inspect log --all --stat
git -C /tmp/inspect show weave/add-<your-instance>:terraform/env/dev/dev.tfvars
```

## The test suite

```sh
go build ./... && go vet ./... && go test ./...
```

Highlights:

- `internal/demo` — the end-to-end capstone: assembles the production
  dependency graph exactly as `cmd/weaved` does and drives it through the
  real HTTP API. Proves fail-before-mutate (a rejected request leaves the
  remote with zero new branches) and the happy path (choice expansion visible
  in the pushed branch).
- `internal/server` — API contract tests (status mapping, DTO leak
  prevention).
- `internal/hcl`, `internal/domain`, `internal/fs`, `internal/validate`,
  `internal/pipeline`, `internal/registry`, `internal/git`,
  `internal/orchestrate` — the engine.

## Running against a real Bitbucket repo

See the configuration table in [`README.md`](./README.md). Point
`WEAVE_REPO_URL` at a real clone URL, `WEAVE_GIT_TOKEN` at a service-account
token, and `WEAVE_SPECS` at your module catalog. For a dry run against a
local "remote", any bare repository path works as `WEAVE_REPO_URL`, and
`WEAVE_BITBUCKET_API` can point at any stub implementing the Bitbucket Cloud
`POST /2.0/repositories/<slug>/pullrequests` endpoint (see
`internal/demo/demo.go:startFakeBitbucket` for a reference implementation).
