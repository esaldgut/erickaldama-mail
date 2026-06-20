# erickaldama.com — Email System

A serverless email system on AWS — receive and send for `erickaldama.com` — provisioned entirely
with **AWS CDK (Go)**, consumed by a terminal-native Go client. Built on the `ErickSA` account in
`us-east-1`.

This is the umbrella repo. The system is decomposed into independent subprojects by domain:

| Subproject | Domain | Status |
|---|---|---|
| SP-0 | Governance — CDK-Go `PreToolUse` hook + skill-recipes (CDK, SES) | ✅ live |
| SP-1 | Foundation — Route 53 hosted zone + base IAM (CDK-Go) | ✅ live |
| SP-2 | SES identity + send — DKIM, custom MAIL FROM, DMARC, reputation (CDK-Go) | ✅ live |
| SP-3 | Receive pipeline — catch-all receipt rule → S3 → Go Lambda → DynamoDB + DLQ (CDK-Go) | ✅ live |
| SP-4 | Go TUI client — reads DynamoDB+S3, sends via SES v2 (nvim/tmux) | pending |
| SP-5 | iOS Swift client (future) — same backend | pending |

The send **and** receive halves are live: mail to any `*@erickaldama.com` lands parsed and indexed in
DynamoDB, and `erick@erickaldama.com` can send DKIM-signed, DMARC-aligned mail. Only the reader (SP-4)
remains before the loop is closed end-to-end from a terminal.

## Architecture

See [`docs/architecture.md`](docs/architecture.md) — a Mermaid diagram GitHub renders natively.
A higher-fidelity version with official AWS icons (for slides/diffusion) lives in
[`docs/diagrams/`](docs/diagrams/), generated as code via mingrammer `diagrams`:

```bash
cd docs/diagrams && .venv/bin/python architecture_icons.py   # → erickaldama_email_architecture.png
```

## Design decisions (audited)

| Decision | Why |
|---|---|
| Region `us-east-1` | SES is unavailable in `mx-central-1`; us-east-1 supports send + receive + SMTP |
| S3 SSE-S3, not SES message-encryption | SES message-encryption is client-side (Java/Ruby only); a Go client couldn't decrypt its mail |
| Custom MAIL FROM (`mail.erickaldama.com`) | Required for SPF→DMARC alignment; without it, mail lands in spam |
| Send via SES v2 API + SigV4, not SMTP | Eliminates the only long-lived mail secret |
| Reader = own Go TUI (no IMAP) | SES has no native IMAP/POP; the client reads DynamoDB+S3 directly |
| Everything via AWS CDK (Go) | Enforced by a `PreToolUse` hook (SP-0), not just intended |

The full engineering dossier (region decision, Well-Architected audit, SES recipe, plugin catalog,
decomposition) is kept in the planning notes, with the textual spec as the source of truth — the
diagram is the eagle-eye view.

## What's deployed (live)

Four CDK-Go stacks are live in `367707589526` / `us-east-1`:

**`FoundationStack` (SP-1)** — the public Route 53 hosted zone for `erickaldama.com`
(`Z023932911KA6S98A6ZRW`, CAA pinned to Amazon), plus the `mail-readonly` managed policy that scopes
the agent to a pure read-only, region-pinned allowlist with a hard-deny on mutation, credential
minting, and recon. The permissions boundary `erickaldama-boundary` is a bootstrap artifact (it must
pre-exist for `cdk bootstrap --custom-permissions-boundary`), managed out-of-band, not stack-owned.

**`SendingStack` (SP-2)** — the SES sending pipeline:

```
erick@erickaldama.com ──ses:SendEmail──▶ EmailIdentity (erickaldama.com)
                                          │  Easy DKIM RSA-2048 (3 CNAME, auto)
                                          │  custom MAIL FROM mail.erickaldama.com (MX + SPF TXT, auto)
                                          │  DMARC TXT (manual): v=DMARC1; p=none;
                                          ▼
                                    ConfigurationSet (mail-config)
                                    feedback-forwarding OFF · reputation metrics ON
                                          │
                          ┌───────────────┴───────────────┐
                          ▼                                ▼
            EventBridge event destination         CloudWatch alarms
            (BOUNCE + COMPLAINT)                   Reputation.BounceRate   ≥ 0.02
                          │                        Reputation.ComplaintRate ≥ 0.0005
            Events::Rule (source aws.ses)          treatMissingData: ignore
                          ▼
            SNS topic mail-bounce-complaint  ──▶  operator email (subscribed in SP-3)
```

Sending is gated by the `mail-send` managed policy (`ses:SendEmail` on the identity ARN with a
`ses:FromAddress = erick@erickaldama.com` condition) carried by `mail-sender-role`. The full deploy
runbook — real commands, real outputs, and the two failure modes the audit didn't anticipate (the
permissions boundary needing the same `events:*` the exec-policy gained; the read-only role needing to
read the SNS/EventBridge it deploys) — is in **[`docs/SP-2-DEPLOY.md`](docs/SP-2-DEPLOY.md)**.

**`MailStorageStack` + `ReceivingStack` (SP-3)** — the inbound pipeline. Mail to any
`*@erickaldama.com` is accepted, archived raw in S3, parsed by a Go Lambda, and indexed in DynamoDB:

```
external sender ──▶ MX erickaldama.com → inbound-smtp.us-east-1.amazonaws.com
                          │
                    SES receipt rule set "erickaldama-inbound" (ACTIVE, catch-all)
                    TlsPolicy Require · scan on · activated by a CDK custom resource
                          │
                ┌─────────┴── ① S3 action ──▶ bucket erickaldama-mail-raw
                │                              SSE-S3 · Block-All-Public · key inbound/<sesMessageId>
                │
                └─── ② Lambda action (async) ──▶ mail-receive  (Go, provided.al2023, arm64)
                                                  │  reads Receipt.Recipients (envelope, authoritative)
                                                  │  idempotent PutItem per domain recipient
                                                  ▼
                                          DynamoDB mail-index (on-demand)
                                          PK = mailbox#<addr>   SK = ts#<rfc3339>#<rfc5322-msgid>
                                                  │
                                  on failure ─────┴──▶ SQS DLQ (SSE) ──▶ alarm depth>0 ──▶ SNS
                                                                                            │
   _dmarc TXT rua=mailto:dmarc-reports@erickaldama.com (same-domain, dogfooded) ──▶ pipeline
                                                          SNS mail-bounce-complaint ──▶ operator email
```

The bucket lives in its own stack and crosses to `ReceivingStack` as a Go prop (the SES bucket policy
is created in `ReceivingStack`, not via `bucket.AddToResourcePolicy`, to avoid a real CloudFormation
dependency cycle). Verified live with a real inbound message: received, stored in S3, and indexed in
DynamoDB end-to-end. Full runbook — commands, real outputs, the post-deploy diagnostic, and the two
"symptoms that weren't bugs" — is in **[`docs/SP-3-DEPLOY.md`](docs/SP-3-DEPLOY.md)**. The
human-executed bootstrap/deploy steps per subproject are in **[`docs/BOOTSTRAP.md`](docs/BOOTSTRAP.md)**;
SP-1's first-deploy runbook is in **[`docs/SP-1-DEPLOY.md`](docs/SP-1-DEPLOY.md)**.

### Why this is the interesting part

Every mutation is human-executed out-of-band with SSO Admin; the agent works only as `mail-readonly`
and a `PreToolUse` hook (SP-0) mechanically blocks any non-CDK-Go write. Across four subprojects, eight
real-deploy findings were recorded — not hidden — including failures that the adversarial audit did not
anticipate (a permissions boundary that intersects the exec-policy, a receipt rule set with no
declarative "active" field, two Lambda field traps that `go build` accepts but `go vet` rejects). The
discipline on display: adversarial audit before implementing (agents that *compile* the CDK against the
live version, not just read docs), real-output verification before declaring done (a green deploy is not
a verified one — DKIM is asynchronous and the SP-3 "bug reports" turned out to be a confirmed pipeline
plus a missing click), and productivizing every real-deploy lesson into memories, skills, and this repo.

## Ecosystem

This repo is one node in a small AI-native engineering ecosystem. The other two are public and
demonstrate how the work here feeds back into reusable practice:

- **[ai-native-engineering-workspace](https://github.com/esaldgut/ai-native-engineering-workspace)** —
  the curated library of agent skills, hooks, and engineering practices. The governance artifacts this
  project produces (the CDK-Go `PreToolUse` hook, the SES recipe, the verify-before-act discipline) are
  meant to graduate into that workspace as generalized, reusable skills.
- **[lessongate](https://github.com/esaldgut/lessongate)** — a Go runtime agent that watches engineering
  repos, uses the Claude API to detect which lessons are *generalizable* (not project-specific), gates
  them for sensitive content, and opens draft PRs to the workspace for human review. Its multi-repo mode
  is designed to watch **this** repo too: the real-deploy findings recorded here (e.g. "a permissions
  boundary intersects the exec-policy", "the agent's read-only must read what it deploys") are exactly
  the kind of durable lesson lessongate is built to surface and publish upstream.

Together: this project generates lessons from real infrastructure work → lessongate extracts and
sanitizes the generalizable ones → they land in the workspace → the next project starts with sharper
tools. The repo is not a static snapshot; it's a feeder in a living loop.
