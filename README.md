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
| SP-3 | Receive pipeline — receipt rule → S3 → Lambda → DynamoDB + DLQ (CDK-Go) | pending |
| SP-4 | Go TUI client — reads DynamoDB+S3, sends via SES v2 (nvim/tmux) | pending |
| SP-5 | iOS Swift client (future) — same backend | pending |

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

Two CDK-Go stacks are live in `367707589526` / `us-east-1`:

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
            SNS topic mail-bounce-complaint  ──▶  (subscriber added in SP-3)
```

Sending is gated by the `mail-send` managed policy (`ses:SendEmail` on the identity ARN with a
`ses:FromAddress = erick@erickaldama.com` condition) carried by `mail-sender-role`. The full deploy
runbook — real commands, real outputs, and the two failure modes the audit didn't anticipate (the
permissions boundary needing the same `events:*` the exec-policy gained; the read-only role needing to
read the SNS/EventBridge it deploys) — is in **[`docs/SP-2-DEPLOY.md`](docs/SP-2-DEPLOY.md)**. The
human-executed bootstrap/deploy steps per subproject are in **[`docs/BOOTSTRAP.md`](docs/BOOTSTRAP.md)**.

### Why this is the interesting part

Every mutation is human-executed out-of-band with SSO Admin; the agent works only as `mail-readonly`
and a `PreToolUse` hook (SP-0) mechanically blocks any non-CDK-Go write. Two of the three deploys
failed first — and the failures are documented as findings, not hidden. The discipline on display:
adversarial audit before implementing, real-output verification before declaring done (a green deploy
is not a verified one — DKIM is asynchronous), and productivizing every real-deploy lesson.
