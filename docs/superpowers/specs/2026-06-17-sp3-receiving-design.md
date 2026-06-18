# SP-3 — Receive pipeline: design

> Account `367707589526` (ErickSA), `us-east-1`. Provisioned via AWS CDK (Go). The agent works
> read-only as `mail-readonly`; the human deploys out-of-band with SSO Admin. This is the first time
> the project RECEIVES email — a new pattern (SES receiving lives only in the v1 `ses` API, never
> ported to `sesv2`), so a full adversarial audit (exploration agents + a CDK-compiling agent against
> the live awscdk version) precedes the plan.

## Goal

Deliver the complete inbound ingestion path: a message sent to any `*@erickaldama.com` address lands
parsed and indexed in DynamoDB (queryable by mailbox) with its raw MIME archived in S3 — ready for the
SP-4 TUI to read. SP-3 does NOT include the reader client or its read IAM (that is SP-4).

## Architecture

Two new CDK-Go stacks on top of the live SP-1 (FoundationStack) and SP-2 (SendingStack) infra. Bucket
and ingestion logic are separated into two stacks so the bucket↔receipt-rule policy cycle is broken by
construction (Approach A: cross-stack via CDK object reference).

```
INBOUND (all us-east-1)
  DNS (Route53 zone Z023932911KA6S98A6ZRW — from SP-1)
    MX   erickaldama.com → 10 inbound-smtp.us-east-1.amazonaws.com         [NEW in SP-3]
    DMARC _dmarc TXT → v=DMARC1; p=none; rua=mailto:dmarc-reports@erickaldama.com  [RE-POINTED]

  MailStorageStack (deploys 1st)
    └─ S3 bucket erickaldama-mail-raw — SSE-S3, Block Public Access ON, lifecycle → IA @90d

  ReceivingStack (deploys 2nd; receives the bucket as a Go prop — Approach A)
    ├─ SES receipt rule set "erickaldama-inbound" (ACTIVE) — v1 API
    │   └─ rule "store-and-index" (catch-all, Recipients: [] = *@erickaldama.com):
    │        Enabled, ScanEnabled, TlsPolicy Require
    │        action[0] S3Action     → s3://erickaldama-mail-raw/inbound/   (S3 FIRST)
    │        action[1] LambdaAction → mail-receive, invocationType Event   (Lambda SECOND, async)
    ├─ bucket policy (on the imported bucket): Principal ses.amazonaws.com, s3:PutObject,
    │     Condition aws:SourceAccount=367707589526 + aws:SourceArn=<receipt-rule-arn>
    ├─ Lambda Go (provided.al2023, ARM64): event has NO body → reads from S3, parses headers,
    │     idempotent PutItem per recipient
    ├─ DynamoDB mail-index (on-demand): PK=mailbox#addr, SK=ts#message-id
    ├─ SQS DLQ (Lambda async onFailure) + alarm ApproximateNumberOfMessagesVisible > 0
    └─ SNS: subscribe esaldgut@gmail.com to mail-bounce-complaint (SP-2 topic, closes the open fan-out)
              + route the DLQ alarm to the SAME topic
```

**Separation of responsibilities.** `MailStorageStack` owns only raw durable storage (rarely changes).
`ReceivingStack` owns ingestion (rule set, Lambda, index, observability). The bucket crosses via a
CDK-Go object reference (`NewReceivingStack(app, id, props, storageBucket)`), which emits the
CloudFormation `Export`/`ImportValue` and orders the deploy (storage first) automatically.

## Component: the receive Lambda (Go)

**Contract (verified trap).** SES invokes the Lambda with an event that does NOT contain the body —
only metadata + common headers + the `messageId` SES assigned. The body (raw MIME) is already in S3
(the S3 action ran first). So the Lambda:

1. Receives the SES event (`events.SimpleEmailEvent`) → extracts `messageId`, `commonHeaders`
   (From, To, Subject, Date), `destination` (recipients).
2. Builds the deterministic `s3Key` (`inbound/<messageId>`) and reads the object from S3 (v1: confirm
   existence + size; indexable headers come from the event, no need to download the full body).
3. Idempotent `PutItem` to `mail-index`, one item per domain recipient (catch-all → a message may have
   several To/Cc under the domain → N items, all pointing at the same `s3Key`).

**Idempotency.** Key = Message-ID. `PutItem` with `ConditionExpression: attribute_not_exists(SK)`. If
SES re-delivers (retries), the second PutItem fails the condition and is treated as idempotent success
(return nil). This is NOT error string-matching — detect via `errors.As` on
`*types.ConditionalCheckFailedException` (discipline: avoid-string-match-error-silencing).

**Runtime/packaging.** Go binary, `provided.al2023`, ARM64 (Graviton). Built via
`awslambdago.NewGoFunction` (automatic Go bundling). Lambda code lives at `cmd/lambda/receive/main.go`
in the same module. Async (Event) invocation, DLQ as `onFailure` destination.

**Errors/DLQ.** parse/S3-read/DynamoDB(non-conditional) failure → Lambda returns error → async retries
(2, default) → on exhaustion → SQS DLQ → alarm depth>0 → SNS → operator. A message that could not be
indexed is visible, never silently lost. `ConditionalCheckFailedException` → not an error (idempotency).

## Component: DynamoDB mail-index schema (on-demand)

| Attribute | Role | Example |
|---|---|---|
| `PK` (partition) | `mailbox#<addr>` | `mailbox#dmarc-reports@erickaldama.com` |
| `SK` (sort) | `ts#<rfc3339>#<message-id>` | `ts#2026-06-17T21:30:00Z#<abc@...>` |
| `messageId` | SES id / Message-ID | dedup + s3 lookup |
| `s3Key` | raw location | `inbound/<messageId>` |
| `from`, `subject`, `date` | indexable headers | list without fetching S3 |
| `sizeBytes` | object size | TUI UX |

`SK` timestamp-first lets SP-4 `Query` a mailbox ordered by date desc, no scan. Body never enters
DynamoDB (400KB/item limit); the TUI fetches it from S3 by `s3Key`. On-demand (PAY_PER_REQUEST):
zero capacity ops, cheapest for personal volume.

## Component: DNS + DMARC dogfooding

**Inbound MX (new).** `erickaldama.com MX 10 inbound-smtp.us-east-1.amazonaws.com`. The host is
region-specific and AWS revises those tables often — the audit verifies it against the live endpoints
table, not hardcoded blind. This is the APEX MX, distinct from SP-2's MAIL FROM MX on
`mail.erickaldama.com` (→ feedback-smtp). Two MX on two names; no collision. Published by
`ReceivingStack` via `awsroute53.NewMxRecord` on the imported zone.

**DMARC re-point (dogfooding).** SP-2 left `_dmarc` at `v=DMARC1; p=none;` (no rua — Gmail does not
authorize cross-domain). Now that SP-3 receives, re-point to `rua=mailto:dmarc-reports@erickaldama.com`
(same-domain → no cross-domain authorization needed, RFC 7489 §7.1). Reports arrive via the same
pipeline (catch-all → S3 → Lambda → DynamoDB). The system receives and archives its own health reports.
Stays at `p=none` (monitoring); progressing to quarantine/reject is a later operational decision with
real data, NOT part of SP-3.

**Ownership note (audit must resolve).** The `_dmarc` TXT already exists — `SendingStack` owns the
`Dmarc` resource. Re-pointing from `ReceivingStack` would have two stacks managing the same record.
Preferred resolution: change `DmarcValue` in `SendingStack` (update the constant + redeploy
SendingStack), NOT duplicate the record in ReceivingStack. Confirm in the audit.

## Component: the receipt rule (v1 namespace `ses`)

Rule set + rule live in the v1 API. CDK-Go: `awsses.NewReceiptRuleSet` + `ReceiptRule` with `Actions`.

- Only 1 active rule set per account/region — the audit reads real state first (a new account should
  have none; activating displaces any existing one).
- `ScanEnabled` (spam/virus) + `TlsPolicy Require` as hardening — audit confirms they don't break
  legitimate receipt.
- Action order S3[0] → Lambda[1] guarantees the object is in S3 before the Lambda is invoked.

## IAM (least-privilege)

- **Lambda exec role** (`mail-receive-lambda-role`): `s3:GetObject` on
  `arn:...:erickaldama-mail-raw/inbound/*` (read-only, prefix only) + `dynamodb:PutItem` on `mail-index`
  + `sqs:SendMessage` to the DLQ + `logs:*` scoped to the Lambda log group. Nothing else.
- **Bucket policy** (resource-based): Principal `ses.amazonaws.com`, `s3:PutObject`, Condition
  `StringEquals aws:SourceAccount=367707589526` + `ArnLike aws:SourceArn=<receipt-rule-arn>`.
- **SES→Lambda permission**: `lambda:InvokeFunction` for the SES principal with the same SourceAccount
  condition (CDK `addPermission`/`grantInvoke`).

**SP-1/SP-2 lessons applied up front (findings #6, #7).**
- exec-policy + boundary already have s3/dynamodb/lambda/sqs/sns/events/logs/iam. Verify nothing new is
  needed against the real synth; if CDK's Go Lambda bundling touches a new service (e.g. ECR for the
  build image), it goes in BOTH layers (exec-policy AND boundary) in the same change.
- `mail-readonly` (the agent's read-only) gains scoped reads so the agent can verify what it deploys:
  `dynamodb:DescribeTable/Query/GetItem`, `lambda:GetFunction/GetFunctionConfiguration`,
  `sqs:GetQueueAttributes` (S3 bucket-level reads already present). Read-only, us-east-1, hard-deny
  intact. Pinned with a FoundationStack template assertion (like SP-2's).

## Naming (new constants in naming.go)

```
RawBucketName       = "erickaldama-mail-raw"
MailIndexTableName  = "mail-index"
ReceiveFunctionName = "mail-receive"
ReceiveLambdaRole   = "mail-receive-lambda-role"
ReceiptRuleSetName  = "erickaldama-inbound"
ReceiptRuleName     = "store-and-index"
ReceiveDlqName      = "mail-receive-dlq"
InboundMxValue      = "10 inbound-smtp.us-east-1.amazonaws.com"   // audit verifies the live host
DmarcReportsAddress = "dmarc-reports@erickaldama.com"
OperatorEmail       = "esaldgut@gmail.com"                        // publishable / benign
```
`DmarcValue` (in SendingStack) updates to `"v=DMARC1; p=none; rua=mailto:dmarc-reports@erickaldama.com"`.

## Testing

**Offline (TDD, template asserts):**
- `mail_storage_stack_test.go`: bucket SSE-S3, Block Public Access, lifecycle, 0 public access.
- `receiving_stack_test.go`: 1 ReceiptRuleSet; 1 catch-all rule with 2 actions in order S3→Lambda;
  DynamoDB on-demand with the key schema; DLQ; Lambda role least-privilege; bucket policy with
  SourceAccount+SourceArn; MX record; SNS subscription.
- `foundation_stack_test.go`: assert the extended `mail-readonly` reads (dynamodb/lambda/sqs), like SP-2.

**Lambda unit tests (no AWS):** SES event → DynamoDB item; idempotency
(`ConditionalCheckFailedException` → nil); multi-recipient → N items.

**Post-deploy (human deploys, agent verifies read-only):** send a REAL message to `test@erickaldama.com`
(the Mailbox Simulator does not exercise receipt — receiving needs real inbound mail) → confirm object
in S3 (`inbound/`), item in DynamoDB (`mailbox#test@...`), DLQ empty, Lambda log OK. Dogfooding: wait
for the first DMARC report to land.

## Deploy sequence (human out-of-band, SSO Admin; agent verifies)

1. (if audit finds new services) re-apply exec-policy + boundary in lockstep.
2. `cdk deploy MailStorageStack` (bucket first).
3. `cdk deploy ReceivingStack` (rule set + Lambda + table + DLQ + SNS sub).
4. `cdk deploy SendingStack` (re-pointed DMARC rua) + `cdk deploy FoundationStack` (mail-readonly reads).
   No registrar change is needed — the NS were delegated in SP-1 and the new MX/DMARC records live in
   the same already-delegated zone.
5. Confirm the SNS email subscription (operator clicks the confirm link in the email).
6. Agent verifies the pipeline read-only; human sends a real test email to any `*@erickaldama.com`
   address; agent confirms the object in S3 + the item(s) in DynamoDB + empty DLQ.

## Out of scope (SP-3)

- The reader (SP-4 TUI) and its read IAM (dynamodb:Query/GetItem + s3:GetObject for the client).
- DMARC progression to quarantine/reject (operational, later, with data).
- Full MIME parsing into DynamoDB (body stays in S3; v1 indexes headers only).
- Multi-region inbound failover, SNS fan-out beyond the operator, dedicated IPs (dossier: over-engineering).

## Disciplines

aws-cli-pre-flight-canonical · modern-go-guidelines (Go 1.26, `any`) ·
avoid-string-match-error-silencing (idempotency via errors.As) · infra-plan-three-source-cross-check ·
adversarial-audit-before-new-pattern (first receiving) · full audit with CDK compilation (like SP-2).
