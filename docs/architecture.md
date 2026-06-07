# erickaldama.com — Email System Architecture

A serverless email system on AWS — receive and send for the `erickaldama.com` domain —
provisioned entirely with **AWS CDK (Go)**, consumed by a terminal-native Go client.

> Region: `us-east-1` · Account: ErickSA (367707589526) · All resources provisioned via AWS CDK Go.

```mermaid
flowchart TB
    sender([📧 External sender])
    user([👤 Operator · nvim + tmux])

    subgraph dns["🌐 Amazon Route 53 — hosted zone erickaldama.com"]
        direction LR
        mx["MX → inbound-smtp"]
        dkim["DKIM ×3 CNAME"]
        mailfrom["MAIL FROM · MX + SPF"]
        dmarc["DMARC TXT · p=none→reject"]
    end

    subgraph receive["📥 RECEIVE path · us-east-1"]
        direction TB
        ses_in["✉️ Amazon SES<br/>receipt rule set"]
        s3["🪣 Amazon S3<br/>raw MIME · SSE-S3"]
        lambda["⚡ AWS Lambda<br/>parse · async/Event"]
        ddb["🗄️ Amazon DynamoDB<br/>mail-index"]
        dlq["📨 Amazon SQS<br/>DLQ"]
    end

    subgraph send["📤 SEND path"]
        direction TB
        ses_out["✉️ Amazon SES v2<br/>SendEmail · SigV4"]
    end

    subgraph client["🖥️ Go CLI / TUI client"]
        direction TB
        tui["Go TUI<br/>aws-sdk-go-v2/sesv2"]
    end

    subgraph obs["📊 Observability"]
        cw["📈 CloudWatch alarms<br/>bounce 2% · complaint 0.05% · DLQ"]
        sns["🔔 Amazon SNS → operator"]
    end

    sender --> mx
    mx --> ses_in
    ses_in -->|"① store"| s3
    ses_in -->|"② notify"| lambda
    lambda -->|read body| s3
    lambda -->|"PutItem (idempotent, Message-ID)"| ddb
    lambda -.->|on-failure| dlq

    tui -->|build MIME| ses_out
    ses_out --> sender
    tui -->|"Query"| ddb
    tui -->|"GetObject on open"| s3
    user --> tui

    ses_out -.->|bounce/complaint events| cw
    dlq -.-> cw
    cw --> sns

    dkim -.-|authenticates| ses_out
    mailfrom -.-|SPF alignment| ses_out
    dmarc -.-|policy| ses_out

    classDef aws fill:#1a2332,stroke:#ff9900,stroke-width:1px,color:#fff;
    classDef ext fill:#161b22,stroke:#58a6ff,stroke-width:1px,color:#fff;
    class ses_in,s3,lambda,ddb,dlq,ses_out,cw,sns,mx,dkim,mailfrom,dmarc aws;
    class sender,user,tui ext;
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
