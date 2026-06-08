---
name: cdk-go-recipe
description: Use when authoring or deploying ANY AWS infrastructure as code in this account. Provisions exclusively via AWS CDK in Go (aws-cdk-go latest), verifying each construct against live AWS docs before use, then preparing (not executing) the deploy for out-of-band human execution. Covers app structure, the diff→synth→deploy flow, version freshness, jsii idioms, and the verify-before-act cache.
---

# CDK-Go recipe — verify before you act

This is for **Claude Code**. The IAM allowlist is the security boundary; you NEVER run `cdk deploy` — you
prepare it and hand it to the human (out-of-band). You reason and GENERATE; the human EXECUTES mutations.

## The 4 phases (every infra change)

**F1 — VERIFY RULES.**
- Live version: `go list -m -versions github.com/aws/aws-cdk-go/awscdk/v2` confirms the latest. NOTE: the
  governance hook denies `go` (it is an execution engine), so you cannot run that through Bash here — read the
  version from go.mod and, if you cannot confirm it is current, SAY SO explicitly rather than assume. NEVER
  hardcode a version (e.g. v2.258.0).
- Constructs: read `docs/cdk-verified.json`. An entry is VALID iff `cdk_version` == the go.mod aws-cdk-go
  version AND `verified_at` is within 7 days. Anti-poison: on read, if `cdk_version` != the live go.mod
  version, IGNORE the cache (a forged stale entry must not be trusted). If any construct is missing/stale,
  dispatch the `cdk-verifier` agent (via the Task tool) with the construct list + target version. This dispatch
  is BEST-EFFORT — you must choose to do it; it is not mechanically enforced (only the IAM boundary is). Write
  the returned verdict back into `docs/cdk-verified.json`. `--force-verify` invalidates the whole cache.

**F2 — READ STATE.** `aws sts get-caller-identity` (confirm account 367707589526). `cdk diff` (read-only delta).

**F3 — ACT (prepare, do not execute).** Generate the stack code + the exact `cdk deploy` command + the diff.
HAND IT TO THE HUMAN. The human runs it out-of-band with a named profile the agent never selects. Do NOT run
`cdk deploy` yourself (the hook denies it; the IAM read-only cred cannot mutate anyway).

**F4 — VERIFY OUTPUT.** After the human's deploy, read CloudFormation events/outputs. A known error → back to F1.

## Go / CDK-Go idioms (compose with modern-go-guidelines, do not duplicate)
- `cdk.json`: `"app": "go mod download && go run ."`. `main.go` instantiates the `App`; one stack per file.
- jsii: pointers via `jsii.String(...)`, `jsii.Number(...)`, `jsii.Bool(...)` — never raw literals into props.
- Cross-stack: SP-1 exposes the hosted zone; SP-2 consumes it via a cross-stack reference. `cdk diff` ALWAYS
  before handing off a deploy.

## Cache schema (docs/cdk-verified.json)
```json
{ "cdk_version": "v2.258.0",
  "constructs": {
    "awsroute53.NewHostedZone": { "exists": true, "doc_url": "https://...",
      "signature_hash": "sha256:...", "verified_at": "2026-06-08T12:00:00Z" } } }
```

## --dry-run
When invoked with `--dry-run`, run F1–F3 (verify, read, GENERATE the code/commands) but do NOT ask the human to
execute — just show what you WOULD do. Used by the eval harness.
