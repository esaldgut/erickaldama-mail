# erickaldama.com — Email System Architecture

A serverless email system on AWS — receive and send for the `erickaldama.com` domain —
provisioned entirely with **AWS CDK (Go)**, consumed by a terminal-native Go client.

> Region: `us-east-1` · Account: ErickSA (367707589526) · All resources provisioned via AWS CDK Go.
>
> **Status (2026-06-24):** the SEND path (SP-2), the RECEIVE path (SP-3), and the Go terminal client
> (SP-4) are **all live and verified** — send→receive→read exercised end-to-end against live AWS.
> The full loop is closed.

```mermaid
flowchart TB
    sender([📧 External sender])
    user([👤 Operator · nvim + tmux])

    subgraph dns["🌐 Amazon Route 53 — hosted zone erickaldama.com"]
        direction LR
        mx["MX → inbound-smtp.us-east-1"]
        dkim["DKIM ×3 CNAME"]
        mailfrom["MAIL FROM mail.· MX + SPF"]
        dmarc["DMARC TXT · p=none<br/>rua → dmarc-reports@ (dogfooded)"]
    end

    subgraph receive["📥 RECEIVE path · us-east-1 · LIVE"]
        direction TB
        ses_in["✉️ Amazon SES v1<br/>rule set erickaldama-inbound<br/>catch-all · TLS Require · scan"]
        s3["🪣 Amazon S3<br/>erickaldama-mail-raw<br/>SSE-S3 · Block-All-Public"]
        lambda["⚡ AWS Lambda mail-receive<br/>Go · provided.al2023 · arm64<br/>async/Event"]
        ddb["🗄️ Amazon DynamoDB<br/>mail-index · on-demand<br/>PK mailbox# · SK ts#"]
        dlq["📨 Amazon SQS<br/>mail-receive-dlq · SSE"]
    end

    subgraph send["📤 SEND path · LIVE"]
        direction TB
        ses_out["✉️ Amazon SES v2<br/>SendEmail · SigV4<br/>mail-config · DKIM signed"]
    end

    subgraph client["🖥️ Go CLI / TUI client · SP-4 · LIVE · 2026-06-24"]
        direction TB
        tui["cmd/mail (CLI Cobra)<br/>cmd/mail-tui (TUI Bubble Tea · Vim-motions)"]
        read_iam["🔑 mail-client-read<br/>Query mail-index · GetObject erickaldama-mail-raw"]
        send_iam["🔑 mail-sender<br/>SendRawEmail scoped"]
        ai["🤖 AI dual-backend<br/>Ollama local qwen3:32b (default, on-device)<br/>Claude API claude-opus-4-8 (opt-in, explicit warning)"]
    end

    subgraph obs["📊 Observability"]
        cw["📈 CloudWatch alarms<br/>bounce 2% · complaint 0.05% · DLQ"]
        sns["🔔 Amazon SNS → operator"]
    end

    sender --> mx
    mx --> ses_in
    ses_in -->|"① store (S3 action first)"| s3
    ses_in -->|"② invoke (Lambda action, async)"| lambda
    lambda -->|confirm object| s3
    lambda -->|"PutItem per Receipt.Recipient (idempotent by RFC5322 Message-ID)"| ddb
    lambda -.->|on-failure destination| dlq

    tui --- read_iam
    tui --- send_iam
    tui --- ai
    read_iam -->|"Query (mail-index)"| ddb
    read_iam -->|"GetObject on open (erickaldama-mail-raw)"| s3
    send_iam -->|"SendRawEmail · SigV4 · build MIME"| ses_out
    ses_out --> sender
    user --> tui

    ses_out -.->|bounce/complaint events| cw
    dlq -.-> cw
    cw --> sns

    dkim -.-|authenticates| ses_out
    mailfrom -.-|SPF alignment| ses_out
    dmarc -.-|policy| ses_out

    classDef aws fill:#1a2332,stroke:#ff9900,stroke-width:1px,color:#fff;
    classDef ext fill:#161b22,stroke:#58a6ff,stroke-width:1px,color:#fff;
    classDef iam fill:#1a2332,stroke:#dd344c,stroke-width:1px,color:#fff;
    classDef ai fill:#1a2332,stroke:#7b68ee,stroke-width:1px,color:#fff;
    class ses_in,s3,lambda,ddb,dlq,ses_out,cw,sns,mx,dkim,mailfrom,dmarc aws;
    class sender,user,tui ext;
    class read_iam,send_iam iam;
    class ai ai;
```

## Provisioning & governance

Every resource above — the Route 53 hosted zone, SES identity + DKIM, the S3 bucket, the Lambda,
the DynamoDB table, the SQS DLQ, IAM policies, and the CloudWatch alarms — is provisioned by a single
**AWS CDK app written in Go** (`github.com/aws/aws-cdk-go/awscdk/v2`, latest version, verified live).

This is enforced, not just intended: a `PreToolUse` hook blocks any AWS write that does not come
through the CDK-Go stack, and a skill-recipe verifies live AWS docs before each step.

## Key decisions (audited against Well-Architected)

| Decision | Rationale |
|---|---|
| Region `us-east-1` | SES not available in `mx-central-1`; us-east-1 supports send + receive + SMTP |
| S3 **SSE-S3**, not SES message-encryption | SES message-encryption is client-side (Java/Ruby only) — a Go client could not decrypt its own mail |
| Custom MAIL FROM (`mail.erickaldama.com`) | Required for SPF→DMARC alignment; without it, mail lands in spam |
| Send via SES v2 API + SigV4, **not SMTP** | Eliminates the only long-lived mail secret |
| DynamoDB index (not S3-list) | Makes the TUI listing instant; ~$0 at personal volume |
| DLQ + idempotent PutItem | The one real reliability gap; everything else is right-sized, not over-engineered |
| AI default = Ollama local (`qwen3:32b`), not Claude API | Mail corpus never crosses the network without explicit opt-in; `qwen3:32b` is Ollama's reference model for tool-calling |
| Two disjoint IAM users (`mail-client-read` / `mail-sender`) | Least-privilege split: read path cannot send; send path cannot read the inbox |
| `ErrSandboxRecipient` typed sentinel (not string-match) | SES sandbox rejects non-verified recipients; typed error lets callers handle it cleanly without fragile string comparison |
