---
name: weave-extend-catalog
description: How to add or change Weave golden modules, inputs, validation rules, and t-shirt-size choice options in spec.yaml - and what code/tests to touch (usually none) when doing so. Use when the platform team wants a new service in the catalog, new sizing options, or when debugging why a spec change produces 422/500 errors.
---

# Extending the Weave module catalog

## The golden rule

**Business logic lives in the spec, never in Weave code.** Adding a module,
an input, or a t-shirt size is a `spec.yaml` edit — zero Go changes, zero
redeploys (`FileSource` re-reads the file on every request). If you find
yourself editing Go code to add a catalog entry, you are doing it wrong.

## Manifest anatomy (`apiVersion: weave.dev/v1`)

```yaml
apiVersion: weave.dev/v1
kind: ModuleManifest
metadata:
  registry: "git::https://github.com/acme/iac-modules.git"
  defaultRef: "v2.4.0"
modules:
  - name: cloud-run              # catalog key; used as moduleType in the API
    displayName: "Cloud Run Service"
    description: "Shown in the wizard"
    category: compute
    source: "git::…//modules/cloud-run"   # golden module; NEVER reaches the browser
    version: "v2.4.0"                     # pinned as source?ref=version in main.tf
    stability: stable
    inputs:
      - name: service_name
        type: string              # string | number | bool | choice
        required: true
        description: "Shown as field help in the wizard"
        tfvarsKey: service_name   # variable name written to <env>.tfvars
        moduleArg: name           # argument name inside the module block
        validation:               # optional; caller gets a 422 on violation
          pattern: "^[a-z]([-a-z0-9]*[a-z0-9])?$"
          maxLength: 63
```

## T-shirt sizes (`type: choice`)

```yaml
      - name: cpu                # expansion TARGET: declared, typed, with
        type: number             # tfvarsKey/moduleArg — but the developer
        tfvarsKey: cpu           # never fills it (the wizard hides it via
        moduleArg: cpu           # the managedByChoice DTO flag)
      - name: memory
        type: string
        tfvarsKey: memory
        moduleArg: memory
      - name: size               # the CHOICE: virtual — never emits a value,
        type: choice             # never gets tfvarsKey/moduleArg
        required: true
        description: "How big should this service be?"
        options:
          - value: small                            # what the API receives
            label: "Small — prototypes & internal tools"   # what the developer sees
            description: "1 vCPU · 512Mi memory"
            expandsTo:            # server-side only; never sent to the browser
              cpu: 1              # values are coerced against each TARGET's
              memory: "512Mi"     # declared type — a mismatch is YOUR bug (500)
```

Authoring rules (violations are `ErrSpecInvalid` → HTTP 500, correctly blamed
on the platform team, not the developer):

1. Every `expandsTo` key must be a **declared, non-choice** input of the same
   module.
2. Every `expandsTo` value must coerce to the target's declared type
   (`cpu: "lots"` against `type: number` is a spec bug).
3. Give every expansion target its own `tfvarsKey`/`moduleArg` — that's how
   the values reach Terraform.
4. Don't mark expansion targets `required` — the developer can't fill them
   (the wizard hides them), so the expansion is their only source.

Developer-fault behaviors you get for free (HTTP 422, actionable messages):
unknown option value (`ErrUnknownChoice`), a direct value for an input the
selected option also expands (`ErrChoiceConflict`), missing required choice.

## Verifying a catalog change

```sh
# 1. Does it parse and list? (FileSource re-reads per call)
go run ./cmd/weaved -demo   # or point -specs at your file with real config
curl -s localhost:8080/api/catalog | python3 -m json.tool
# 2. Does the full loop accept it?  POST /api/scaffold with a real selection.
# 3. Check the wizard renders your labels the way you want.
```

For a repo-committed example, `internal/demo/demo.go`'s `specYAML` is the
canonical showcase (two modules, both with choices) — keep it working; the
e2e test depends on it.

## When Go changes ARE needed

- New input **type** (beyond string/number/bool/choice): `validate.coerceRaw`
  / `coerceAny` + tests, and the wizard's `renderConfigure`.
- New spec **fields**: `registry.InputSpec`/`OptionSpec` (+ parser tests),
  the server DTO (mind the leak tests!), and the wizard.
- Follow TDD: red test in the affected package first. See `weave-onboard`
  for the invariants you must not break (esp. the 422/500 firewall and the
  no-leak DTO rules).
