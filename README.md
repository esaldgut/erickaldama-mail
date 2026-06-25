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
| SP-4 | Go TUI/CLI/AI client — reads DynamoDB+S3, sends via SES, AI dual-backend (Ollama+Claude) | ✅ live |
| CD | GitHub Actions → OIDC → AWS, deployed 2026-06-24, PR #6 | ✅ live |
| SP-5 | iOS Swift client (future) — same backend | pending |

The send, receive, **and** client layers are live: mail to any `*@erickaldama.com` lands parsed and
indexed in DynamoDB; `erick@erickaldama.com` can send DKIM-signed, DMARC-aligned mail; and the Go
terminal client (SP-4) closes the loop end-to-end — send→receive→read verified in live AWS on
2026-06-24. See **[`docs/SP-4-DEPLOY.md`](docs/SP-4-DEPLOY.md)** and
**[`CHANGELOG.md`](CHANGELOG.md#sp-4----cliente-tuicliai-go----2026-06-24)** for the full build
history, deploy findings, and the verified end-to-end test.

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

Five CDK-Go stacks plus a Go terminal client are live in `367707589526` / `us-east-1`:

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

**Go terminal client (SP-4)** — the consumer layer that closes the loop end-to-end. Two binaries over a
shared domain core:

```
cmd/mail         CLI Cobra · ls / read / send / reply / ai / tmux (popup·status)
cmd/mail-tui     TUI Bubble Tea · list / reader / multi-field composer · Vim-motions (j/k/gg/G)

internal/config     config.toml (~/.config/erickaldama-mail, XDG) — known mailboxes, default from/profiles
internal/message    MIME parse/build (enmime/v2) · threading RFC 5322 · html-to-markdown render (glamour)
internal/mailbox    Reader  — Query DynamoDB mail-index + GetObject S3 erickaldama-mail-raw
                    Sender  — SendRawEmail via SES v2 + SigV4
internal/aiassist   LLMProvider interface + agent-loop (read-only tools, no send tool)
  /ollama             Ollama local  (qwen3:32b)   — default, mail stays on-device
  /claude             Claude API    (claude-opus-4-8, adaptive thinking) — opt-in with explicit warning
internal/redact     Deterministic NDA mask (secret-shaped tokens + third-party emails) before any
                    backend that crosses the network
internal/awsconf    Credential loader for the two scoped IAM users
internal/wire       Single instantiation point (DRY)
```

**v0.2** — `mail ls` (no `--mailbox`) lists every mailbox from `config.toml`, merged and sorted by `SK`
(ISO8601) descending. CC/BCC on `send`/`reply` and in the TUI composer, with a hard privacy invariant: the
**BCC travels only in the SES envelope, never in the MIME header** (asserted at the core and TUI-end-to-end).
Reply-all pre-fills the Cc with the original recipients minus self. See [`docs/SP-4-DEPLOY.md` §10](docs/SP-4-DEPLOY.md).

**Editor/multiplexer integration** — `mail tmux popup` opens the TUI in a floating tmux overlay; `mail tmux
status` prints the message count for the tmux `status-right`. Suggested bindings (collision-checked, copy-paste in
[`docs/SP-4-DEPLOY.md`](docs/SP-4-DEPLOY.md)): tmux `prefix+e` → popup; nvim `<leader>m{l,s,c,a}` → list/search/compose/AI.

Two IAM users provisioned via CDK with least-privilege disjoint scopes:

| User | Scope |
|---|---|
| `mail-client-read` | `dynamodb:Query` on `mail-index` + `s3:GetObject` on `erickaldama-mail-raw` |
| `mail-sender` | `ses:SendRawEmail` on the identity ARN + `mail-config` configuration set |

SES is still in sandbox (200/day). The client handles sandbox rejections via a typed
`ErrSandboxRecipient` sentinel — no string-match error silencing.

The **AI dual-backend** is the differentiating piece: Ollama local (`qwen3:32b`) is the safe default —
the mail corpus never leaves the Mac; Claude API (`claude-opus-4-8`, adaptive thinking) is opt-in with
an explicit on-screen warning. Both share the same agent-loop with read-only tools (summarize, draft,
triage) — no send tool in the agent path.

Full build and deploy history, including the 3 live-deploy incidents and the end-to-end verification, is
in **[`docs/SP-4-DEPLOY.md`](docs/SP-4-DEPLOY.md)** and **[`CHANGELOG.md`](CHANGELOG.md)**.

**`CdStack` (CD pipeline)** — the fifth stack, deployed 2026-06-24 (PR #6, merged to develop at 95ce3a3):

```
CdStack
  AWS::IAM::OIDCProvider  GithubOidc   — token.actions.githubusercontent.com (L1, 0 Lambda, 0 custom-resource)
  AWS::IAM::Role          mail-cd-diff — trust sub=…:pull_request → sts:AssumeRole on lookup-role only
  AWS::IAM::Role          mail-cd-deploy — trust sub=…:environment:production → sts:AssumeRole on 4 cdk-* roles
  Both roles carry boundary erickaldama-boundary
```

Workflow `.github/workflows/cd.yml` automates future deploys: `diff` job runs `cdk diff` on every PR and
comments the result; `deploy` job runs `cdk deploy --all` on every push to `main`, gated behind the GitHub
Environment `production` (required reviewer: esaldgut) and the OIDC trust `StringEquals` — two independent
human-approval layers. The first real run of `diff` verified the full OIDC flow end-to-end (PR #6 received
the bot comment with the CDK diff). Four deploy findings captured during bootstrap — see
**[`docs/CD-DEPLOY.md`](docs/CD-DEPLOY.md)** (§11) and **[`docs/architecture.md`](docs/architecture.md)**
(§ CD pipeline) for details.

### Why this is the interesting part

Every mutation is human-executed out-of-band with SSO Admin; the agent works only as `mail-readonly`
and a `PreToolUse` hook (SP-0) mechanically blocks any non-CDK-Go write. Across five subprojects, twelve
real-deploy findings were recorded — not hidden — including failures that the adversarial audit did not
anticipate (a permissions boundary that intersects the exec-policy, a receipt rule set with no
declarative "active" field, two Lambda field traps that `go build` accepts but `go vet` rejects, a CDK
CLI npm version scheme that is entirely separate from the Go library version, and a `cdk diff --all` flag
that does not exist in CLI 2.1xxx). The discipline on display: adversarial audit before implementing
(agents that *compile* the CDK against the live version, not just read docs), real-output verification
before declaring done (a green deploy is not a verified one — DKIM is asynchronous and the SP-3 "bug
reports" turned out to be a confirmed pipeline plus a missing click), and productivizing every
real-deploy lesson into memories, skills, and this repo.

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
