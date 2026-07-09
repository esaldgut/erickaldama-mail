# SP-3 — Receive pipeline: deploy runbook & evidence

> Live deploy executed 2026-06-18, account `367707589526` (ErickSA), `us-east-1`.
> The reproducible runbook plus the **real commands and outputs** from the actual deploy and the
> post-deploy diagnostic — including two "symptoms that weren't bugs". The agent works read-only as
> `mail-readonly`; the human runs every mutating step out-of-band with SSO `AdministratorAccess-367707589526`.

## What SP-3 provisions (two stacks)

`MailStorageStack` (deploys first) owns the raw bucket; `ReceivingStack` consumes it as a Go prop
(Approach A — cross-stack object reference) and owns the ingestion logic.

| Resource | Purpose |
|---|---|
| `AWS::S3::Bucket` (`erickaldama-mail-raw`) | Raw inbound MIME, SSE-S3, Block-All-Public, EnforceSSL, IA@90d, RETAIN |
| `AWS::SES::ReceiptRuleSet` (`erickaldama-inbound`) | The inbound rule set — made active by a custom resource (no declarative field) |
| `AWS::SES::ReceiptRule` (`store-and-index`) | Catch-all (no Recipients), `TlsPolicy Require`, scan on; S3 action then Lambda action |
| `AWS::S3::BucketPolicy` | Lets `ses.amazonaws.com` PutObject, scoped by `SourceAccount` + the receipt-rule ARN |
| `AWS::Lambda::Function` (`mail-receive`) | Go (provided.al2023, arm64); indexes one item per domain recipient, idempotent by Message-ID |
| `AWS::DynamoDB::Table` (`mail-index`) | On-demand; PK=`mailbox#<addr>`, SK=`ts#<rfc3339>#<rfc5322-msgid>` |
| `AWS::SQS::Queue` (`mail-receive-dlq`) | SSE-SQS; Lambda async OnFailure destination + a depth>0 alarm |
| `Custom::AWS` (`ActivateRuleSet`) | Calls `SES.setActiveReceiptRuleSet` — `AWS::SES::ReceiptRuleSet` has no declarative active field |
| `AWS::Route53::RecordSet` (apex MX) | `erickaldama.com MX 10 inbound-smtp.us-east-1.amazonaws.com` |
| `AWS::SNS::Subscription` | Operator email on the SP-2 `mail-bounce-complaint` topic — closes the fan-out SP-2 left open |

`SendingStack` is also redeployed to re-point the DMARC `rua` to a same-domain mailbox
(`dmarc-reports@erickaldama.com`) — dogfooding: the pipeline receives its own DMARC reports.
`FoundationStack` is redeployed to add `mail-readonly` reads (dynamodb/lambda/sqs/logs/sns) so the
agent can verify what it deploys.

## Prerequisites

- `cdk` CLI ≥ the cloud-assembly schema of the live `aws-cdk-go/awscdk/v2` (SP-1 finding).
- The environment was bootstrapped in SP-1. SP-3 only redeploys.
- **No exec-policy / boundary change is needed** (audit finding I3): the Go Lambda is bundled via
  `awscdklambdagoalpha.NewGoFunction`, which builds locally with the host Go toolchain and uploads a
  **zip asset to the CDK S3 assets bucket** — it does NOT push to ECR. Unlike SP-2's `events:*` gap,
  nothing new must be added to either IAM layer.

## The deploy, step by step (with real outputs)

### ① Human — deploy the four stacks in order

Storage first (Approach A orders the cross-stack reference automatically, but deploying explicitly in
order is clearest):

```bash
cd .../erickaldama-mail   # the worktree at deploy time
aws sts get-caller-identity --profile AdministratorAccess-367707589526   # expect 367707589526

cdk deploy MailStorageStack --profile AdministratorAccess-367707589526
cdk deploy ReceivingStack   --profile AdministratorAccess-367707589526
cdk deploy SendingStack     --profile AdministratorAccess-367707589526
cdk deploy FoundationStack  --profile AdministratorAccess-367707589526
```

All four reached `CREATE/UPDATE_COMPLETE` (2026-06-18 ~18:22–18:30 UTC). `ReceivingStack` ran the Go
Lambda bundling (local, no Docker/ECR) and the activation custom resource fired `setActiveReceiptRuleSet`
automatically. Stack-event forensics later confirmed **19 resources in ReceivingStack, zero
`CREATE_FAILED`/rollback/retry**.

### ② Human — confirm the SNS email subscription

AWS sends a confirmation email to `esaldgut@gmail.com`; the operator clicks **"Confirm subscription"**
(check Spam/Promotions). Until clicked, the subscription is `PendingConfirmation` — which is normal, not
a failure. (CloudFormation reports the subscription `CREATE_COMPLETE` as soon as the request is dispatched.)

### ③ Human — send a REAL inbound test email

The Mailbox Simulator does NOT exercise *receiving* — it only simulates outbound recipients. To test
the receive path you must send real inbound mail. A message was sent from `esaldgut@icloud.com` to
`test@erickaldama.com` (catch-all → any local part works).

### ④ Agent — verify the pipeline end-to-end (read-only, mail-readonly)

```bash
# rule set is ours and active
aws ses describe-active-receipt-rule-set --profile mail-readonly --region us-east-1 --query 'Metadata.Name'
# → "erickaldama-inbound"   (rule store-and-index: Enabled, TlsPolicy Require, no Recipients = catch-all)

# apex MX resolves (public DNS, no profile)
dig +short MX erickaldama.com
# → 10 inbound-smtp.us-east-1.amazonaws.com.

# the raw message landed in S3 (bucket-level list; the agent cannot read bodies — hard-deny on GetObject)
aws s3api list-objects-v2 --bucket erickaldama-mail-raw --prefix inbound/ \
  --profile mail-readonly --region us-east-1 --query '{Count:KeyCount,Keys:Contents[].Key}'
# → KeyCount 2: inbound/AMAZON_SES_SETUP_NOTIFICATION + inbound/nk57geuhgslprki4e172h3bt8ahg2kkvae3haao1

# the message was indexed in DynamoDB
aws dynamodb query --table-name mail-index --profile mail-readonly --region us-east-1 \
  --key-condition-expression 'PK = :pk' \
  --expression-attribute-values '{":pk":{"S":"mailbox#test@erickaldama.com"}}' --query 'Count'
# → 1
#   item: from="Erick Aldama <esaldgut@icloud.com>", subject="Envío de correo de prueba real",
#         date="Thu, 18 Jun 2026 12:38:01 -0600",
#         messageId=nk57geuhgslprki4e172h3bt8ahg2kkvae3haao1  (the SES id = the S3 key)
#         s3Key=inbound/nk57geuhgslprki4e172h3bt8ahg2kkvae3haao1
#         SK=ts#2026-06-18T18:38:17Z#<666447AC-...@icloud.com>   (the RFC5322 Message-ID)
```

The item cross-references the S3 object by `s3Key`, proving they are the same message. The two
Message-ID semantics the audit corrected are visible live: `s3Key`/`messageId` use `Mail.MessageID`
(the SES id), the `SK` uses `CommonHeaders.MessageID` (the RFC 5322 header). `TlsPolicy: Require` did
NOT bounce the iCloud sender — it delivered over TLS as required.

### ⑤ Agent — SNS subscription confirmed

```bash
aws sns get-topic-attributes --topic-arn arn:aws:sns:us-east-1:367707589526:mail-bounce-complaint \
  --profile mail-readonly --region us-east-1 --query 'Attributes.{Confirmed:SubscriptionsConfirmed,Pending:SubscriptionsPending}'
# → {"Confirmed":"1","Pending":"0"}   (after the operator clicked the confirm link)
```

## The two "symptoms that weren't bugs"

Post-deploy, two things looked wrong; a three-agent read-only diagnostic (CloudFormation forensics +
SNS state + end-to-end receipt) established that neither was a defect:

1. **"The SNS confirmation email never arrived."** The subscription resource was created correctly with
   a real ARN; it was simply `PendingConfirmation` — AWS had sent the email; the operator had not yet
   clicked. Root cause = a pending human click (plus a possible spam-folder), not a deploy gap. A prior
   `SubscriptionsDeleted: 1` (from the redeploy's rollback of an earlier attempt) was a red herring.
2. **"The test email didn't index."** It *did* — the message traversed MX → SES → S3 → Lambda →
   DynamoDB successfully (evidence above). The perception of failure was a lack of visibility, not a
   broken pipeline. The system worked from the first message.

## Finding #8 — the agent's read-only could not see logs or subscription state

All three diagnostic agents hit the same wall: `mail-readonly` could read DynamoDB + SES describe, but
NOT Lambda CloudWatch logs (`logs:*`) nor `sns:GetSubscriptionAttributes`. They verified via the
DynamoDB data plane instead. This generalizes SP-2 finding #7: **the agent's read-only must cover the
observability of what it deploys** — including logs and subscription state, the two signals you need to
diagnose a receive pipeline. Fix: `mail-readonly` gained
`logs:DescribeLogGroups/DescribeLogStreams/FilterLogEvents/GetLogEvents` + `sns:GetSubscriptionAttributes`
(us-east-1 scoped, hard-deny on mutation/GetObject intact). Requires a `FoundationStack` redeploy to
take effect — non-blocking for the receive path, which is already verified.

## Deferred / operational (not part of SP-3)

- DMARC stays at `p=none` (monitoring). Progression to `quarantine`/`reject` is a later operational
  decision once real `rua` reports accumulate in the pipeline.
- Production access (`put-account-details`) remains a separate human step; it gates *sending* volume,
  not receiving.
- SP-4 (the Go TUI) reads `mail-index` + S3 to present mail in a terminal — the next subproject.
