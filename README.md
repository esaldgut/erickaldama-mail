# erickaldama.com — Email System

A serverless email system on AWS — receive and send for `erickaldama.com` — provisioned entirely
with **AWS CDK (Go)**, consumed by a terminal-native Go client. Built on the `ErickSA` account in
`us-east-1`.

This is the umbrella repo. The system is decomposed into independent subprojects by domain:

| Subproject | Domain | Status |
|---|---|---|
| SP-0 | Governance — CDK-Go `PreToolUse` hook + skill-recipes (CDK, SES) | designing |
| SP-1 | Foundation — Route 53 hosted zone + base IAM (CDK-Go) | pending |
| SP-2 | SES identity + send — DKIM, custom MAIL FROM, DMARC, sandbox-exit (CDK-Go) | pending |
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
