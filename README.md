# Weave

**Self-service cloud infrastructure for application teams — without teaching
them Terraform.**

Weave is a lightweight Internal Developer Portal for organizations with a
dedicated platform team. Application developers pick services and t-shirt
sizes in a web wizard; Weave translates their choices into your platform
team's **golden Terraform modules** and opens a **pull request** for human
review. Nobody writes HCL. Nobody bypasses review. Nothing applies without
your existing CI.

```
developer (wizard) ──▶ Weave ──▶ golden-module PR ──▶ platform review ──▶ CI plan/apply
```

## Why

In large organizations, two teams pull in opposite directions:

- **App teams** want to ship. They don't want to learn Terraform, state
  backends, IAM, or naming conventions — so they copy-paste a neighbor's
  config (subtly wrong), file a ticket (days of waiting), or stall.
- **Platform teams** own security, cost, and compliance. They can't review
  hand-rolled Terraform from a hundred teams, and they can't hand-hold every
  request either.

Weave dissolves the tension: the app developer's "hard problem" becomes a
form; the platform team's "control problem" becomes a code review they
already do. Time-to-correct-PR drops from days of tickets to seconds.

## Try it in 30 seconds

No cloud account, no git server, no configuration — `-demo` runs the entire
production loop locally against a throwaway git repository and an in-process
fake Bitbucket:

```sh
go run ./cmd/weaved -demo
```

Open <http://localhost:8080>, pick **Cloud Run Service**, choose
**Small — prototypes & internal tools**, and submit. You get a real branch
with a real commit containing real Terraform — and a demo PR page showing
exactly what your platform team would review.

## How it works

**1. The platform team publishes a module catalog** (`spec.yaml`): golden
modules with typed inputs, validation rules, and **business-language choices**
that expand to concrete values. The mapping lives in the spec, owned by the
platform team — never in Weave code:

```yaml
- name: size
  type: choice
  required: true
  description: "How big should this service be?"
  options:
    - value: small
      label: "Small — prototypes & internal tools"
      description: "1 vCPU · 512Mi memory"
      expandsTo:
        cpu: 1
        memory: "512Mi"
```

Spec edits are live immediately — the catalog is re-read per request, no
redeploy.

**2. Developers use the embedded wizard** (a single static page served from
the Go binary): choose a service → configure business options → review →
submit. The wizard never shows HCL, branch names, or diffs — and option
expansions (`expandsTo`) never even reach the browser.

**3. Weave generates and proposes — never applies.** On submit, Weave
validates everything **before touching anything**, then clones the target
workspace repo to a throwaway temp dir, checks out `weave/add-<name>`,
injects a module block pinned to the golden source + version, updates
`<env>.tfvars`, commits, pushes, and opens a Bitbucket PR. Your CI runs
`terraform plan`/`apply` after a human merges. Weave holds no apply
credentials — a worst-case compromise can only open a bad PR that a human
rejects.

## Guarantees

- **Fail-before-mutate.** Invalid requests are rejected with structured 422
  errors before any file, branch, or remote is touched — proven end-to-end by
  a test asserting zero new branches on the remote after a rejected request.
- **Only golden modules.** Generated `main.tf` contains nothing but module
  blocks pinned to `source?ref=version` from the spec. Hand-rolled resources
  are structurally impossible.
- **Idempotent.** Resubmitting an identical request returns "already up to
  date" — no empty commits, no duplicate PRs.
- **Comment-preserving.** Weave edits Terraform by AST manipulation
  (`hclwrite`); hand-written comments and formatting survive.
- **Honest fault attribution.** A developer mistake is a 422 with actionable
  messages; a platform spec bug is a 500. Weave never blames the developer
  for the platform's mistakes (pinned by tests).

## Running for real

```sh
go build -o weaved ./cmd/weaved

WEAVE_SPECS=/etc/weave/spec.yaml \
WEAVE_REPO_URL=https://github.com/acme/workspace-prod.git \
WEAVE_PR_PROVIDER=github \
WEAVE_PR_REPO=acme/workspace-prod \
WEAVE_GIT_TOKEN=$SERVICE_ACCOUNT_TOKEN \
WEAVE_ENV=dev \
./weaved
```

| Setting | Flag / env | Default |
|---|---|---|
| Listen address | `-listen` / `WEAVE_LISTEN` | `:8080` |
| Module catalog path | `-specs` / `WEAVE_SPECS` | *(required)* |
| Workspace repo clone URL | `-repo-url` / `WEAVE_REPO_URL` | *(required)* |
| Repo identifier for PRs | `-pr-repo` / `WEAVE_PR_REPO` | *(required)* |
| Service-account token | `-git-token` / `WEAVE_GIT_TOKEN` | *(required)* |
| Target environment | `-env` / `WEAVE_ENV` | *(required)* |
| Base branch | `-base-branch` / `WEAVE_BASE_BRANCH` | `main` |
| PR provider | `-pr-provider` / `WEAVE_PR_PROVIDER` | `bitbucket-cloud` |
| PR API base | `-pr-api` / `WEAVE_PR_API` | *(per provider — see below)* |
| Demo mode | `-demo` | off |

The server is stateless — run as many replicas as you like. The legacy
`WEAVE_BITBUCKET_REPO` / `WEAVE_BITBUCKET_API` env vars are still accepted as
fallbacks for `WEAVE_PR_REPO` / `WEAVE_PR_API`.

**PR providers.** `WEAVE_PR_PROVIDER` selects how the reviewed PR is opened;
`WEAVE_PR_REPO` is interpreted in that provider's terms, and `WEAVE_PR_API`
defaults to the provider's public host (override for self-hosted):

| Provider | `WEAVE_PR_REPO` | `WEAVE_PR_API` default |
|---|---|---|
| `bitbucket-cloud` | `workspace/repo` | `https://api.bitbucket.org` |
| `github` | `owner/repo` | `https://api.github.com` |
| `gitlab` | `group/project` (subgroups ok) | `https://gitlab.com` |
| `bitbucket-server` | `PROJECTKEY/repo` | *(required — no public host)* |

**Day 1 → Day 2:** a brand-new target repo needs its workspace bootstrapped
once — `POST /api/workspace` (or the wizard's "Set up the workspace" link)
opens a PR that lays down `terraform/env/<env>/`. After that PR merges,
developers add services with `POST /api/scaffold`. Both are the same
fail-before-mutate, reviewed-PR loop.

**Deployment model.** The target repo is your CD-pipeline / GitOps repo — the
merged PR is applied by whatever watches it (e.g. Argo CD). Weave never runs
Terraform or holds apply credentials; its only write channel is a pushed
branch + PR.

## API

| Endpoint | Purpose |
|---|---|
| `GET /health` | liveness probe |
| `GET /api/catalog` | module catalog DTO: inputs, choices, labels — never git sources or option expansions |
| `POST /api/workspace` | Day 1 bootstrap: `{projectId}` → lays down `terraform/env/<env>/` in the target repo as a reviewed PR. Same status contract as `/api/scaffold`. `statePrefix` is derived server-side (`weave/<env>`) — the developer never supplies Terraform plumbing. |
| `POST /api/scaffold` | Day 2 resource add: `{moduleType, instanceName, inputs}` → `201 {prUrl, branch}` · `200 {changed:false}` (idempotent no-op) · `422 {errors:[…]}` · `502 {error, branch}` (pushed but PR creation failed — the branch is surfaced, not lost) · `500` |

Repo URL, base branch, environment, and token come from **server config** —
never from the request.

## Development

```sh
go build ./... && go vet ./... && go test ./...
```

The suite includes a full end-to-end test (`internal/demo`) that assembles
the production dependency graph exactly as `cmd/weaved` does and drives it
through the real HTTP API against a local bare repo and fake Bitbucket.

Further reading:

- [`ARCHITECTURE.md`](./ARCHITECTURE.md) — structure, invariants, current state
- [`AGENT_HANDOVER.md`](./AGENT_HANDOVER.md) — contributor manual
- [`PHASE0_AUDIT.md`](./PHASE0_AUDIT.md) — the product & workflow audit behind the design
- `.claude/skills/` — onboarding skills for AI coding agents working on Weave

## Roadmap

- Multi-tenant use-case RBAC + PostgreSQL sessions — **foundation landed**
  (`internal/store`; see [DESIGN.md](DESIGN.md)); identity middleware and
  endpoint enforcement next
- Authentication / SSO (trusted proxy header → Entra ID; solo-dev mode)
- Git/HTTP-backed dynamic module registry (specs fetched from the platform repo)
- Per-request attribution in commits and PR bodies

Shipped since v1.0.0: **Day-1 workspace scaffolding via the API**
(`POST /api/workspace`); **GitHub, GitLab, and Bitbucket Server/DC PR
providers** (`WEAVE_PR_PROVIDER`) alongside the original Bitbucket Cloud; the
**multi-tenant RBAC persistence foundation** (`internal/store`).
