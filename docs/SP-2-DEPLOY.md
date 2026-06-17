# SP-2 — SES identity + send: deploy runbook & evidence

> Live deploy executed 2026-06-16/17, account `367707589526` (ErickSA), `us-east-1`.
> This is the reproducible runbook plus the **real commands and outputs** from the
> actual deploy — including two failure modes that the pre-deploy audit did not anticipate
> and how they were resolved. The agent works read-only as `mail-readonly`; the human runs
> every mutating step out-of-band with SSO `AdministratorAccess-367707589526`.

## What SP-2 provisions (`SendingStack`)

| Resource | Purpose |
|---|---|
| `AWS::SES::EmailIdentity` (`erickaldama.com`) | Domain identity, Easy DKIM RSA-2048, custom MAIL FROM `mail.erickaldama.com` |
| 6× `AWS::Route53::RecordSet` | 3 DKIM CNAME + MAIL FROM MX + MAIL FROM SPF TXT (all auto-published by the construct) + 1 manual DMARC TXT |
| `AWS::SES::ConfigurationSet` (`mail-config`) | Reputation metrics on; feedback forwarding off |
| `AWS::SES::ConfigurationSetEventDestination` | Routes BOUNCE + COMPLAINT to the EventBridge default bus |
| `AWS::Events::Rule` (`mail-ses-bounce-complaint`) | Pattern `{source:[aws.ses], detail-type:[Email Bounce, Email Complaint]}` → SNS |
| `AWS::SNS::Topic` (`mail-bounce-complaint`) + `TopicPolicy` | Fan-out point for bounce/complaint events (subscriber added in SP-3) |
| 2× `AWS::CloudWatch::Alarm` | `Reputation.BounceRate ≥ 0.02`, `Reputation.ComplaintRate ≥ 0.0005`, `treatMissingData: ignore` |
| `AWS::IAM::ManagedPolicy` (`mail-send`) | `ses:SendEmail`/`SendRawEmail` on the identity ARN, `Condition ses:FromAddress = erick@erickaldama.com` |
| `AWS::IAM::Role` (`mail-sender-role`) | Assumable via the account principal; carries `mail-send` |

DMARC is `v=DMARC1; p=none;` **without `rua`** — Gmail does not publish cross-domain report
authorization (RFC 7489 §7.1), so a `rua` to a Gmail address would be non-functional. The `rua`
is added in SP-3 pointing at a same-domain mailbox. Production access (`put-account-details`) is a
deferred human step, not part of SP-2.

## Prerequisites

- `cdk` CLI ≥ the cloud-assembly schema of the live `aws-cdk-go/awscdk/v2` (SP-1 finding: a stale CLI
  fails with a deceptive "not bootstrapped"). Verify `cdk --version` before deploying.
- The environment was bootstrapped in SP-1 with `--custom-permissions-boundary erickaldama-boundary`
  and `--cloudformation-execution-policies …/erickaldama-deploy-exec`. SP-2 only redeploys; no re-bootstrap.

## The deploy, step by step (with real outputs)

### ① Human — widen the exec-policy with `events:*`

The EventBridge rule needs `events:PutRule`/`PutTargets` from the CFN exec-role. SP-1's exec-policy
had no `events:*`, so this is applied first (out-of-band).

```bash
aws iam create-policy-version \
  --policy-arn arn:aws:iam::367707589526:policy/erickaldama-deploy-exec \
  --policy-document file://iam/deploy-exec-policy.json \
  --set-as-default \
  --profile AdministratorAccess-367707589526
```

Output:
```json
{ "PolicyVersion": { "VersionId": "v3", "IsDefaultVersion": true, "CreateDate": "2026-06-17T00:06:48+00:00" } }
```

### ② Human — first deploy attempt → FAILED (Finding #6)

```bash
cdk deploy SendingStack --profile AdministratorAccess-367707589526
```

It progressed to 5/18 and then:
```
CREATE_FAILED | AWS::Events::Rule | SesEventRule
Resource handler returned message: "User: arn:aws:sts::367707589526:assumed-role/
cdk-hnb659fds-cfn-exec-role-367707589526-us-east-1/AWSCloudFormation is not authorized to
perform: events:DescribeRule on resource: arn:aws:events:us-east-1:367707589526:rule/
mail-ses-bounce-complaint because NO PERMISSIONS BOUNDARY allows the events:DescribeRule action
(Service: EventBridge, Status Code: 400 …) (HandlerErrorCode: AccessDenied)"
ROLLBACK_IN_PROGRESS … Rollback requested by user.
```

**Root cause — Finding #6 (boundary intersects the exec-policy).** The key phrase is
*"no permissions boundary allows"* — not the exec-policy. The permissions boundary INTERSECTS the
exec-policy (effective permission = exec-policy ∩ boundary). Widening the exec-policy with `events:*`
is necessary but not sufficient: the boundary needs the same permission. This is the SP-1 lesson
(`feedback_cdk_permissions_boundary_intersects`, which surfaced on `ssm`) reappearing on a second
service (`events`). The rollback was clean — no orphaned resources.

### Fix — add `events:*` to the boundary, then re-apply (human)

The agent edited `iam/erickaldama-boundary.json` (added `"events:*"` to the Allow array). Then:

```bash
aws iam create-policy-version \
  --policy-arn arn:aws:iam::367707589526:policy/erickaldama-boundary \
  --policy-document file://iam/erickaldama-boundary.json \
  --set-as-default \
  --profile AdministratorAccess-367707589526
```

Output:
```json
{ "PolicyVersion": { "VersionId": "v3", "IsDefaultVersion": true, "CreateDate": "2026-06-17T01:12:49+00:00" } }
```

Waited for the stack to reach `ROLLBACK_COMPLETE` before redeploying.

### ② bis Human — redeploy → SUCCESS

```bash
cdk deploy SendingStack --profile AdministratorAccess-367707589526
```

```
✅ SendingStack
Deployment time: 169.82s
Stack ARN: arn:aws:cloudformation:us-east-1:367707589526:stack/SendingStack/bdc644a0-69e9-11f1-a342-123133e18a75
```

The deploy returns `CREATE_COMPLETE` **with DKIM still PENDING** — DKIM verification is asynchronous
and decoupled from the deploy. A green deploy is NOT proof the identity can send.

### ③ Agent — wait for DKIM (read-only gate)

```bash
./iam/ses-dkim-wait.sh
```

```
=== waiting for DKIM=SUCCESS on erickaldama.com (profile mail-readonly) ===
attempt 1/30: DKIM=SUCCESS VerifiedForSending=True
PASS: DKIM verified, identity ready for sending
```

Route53 same-account propagation was minutes (verified at attempt 1; worst case is 72h). Snapshot:

```bash
aws sesv2 get-email-identity --email-identity erickaldama.com --profile mail-readonly --region us-east-1 \
  --query '{DKIM:DkimAttributes.Status,VerifiedForSending:VerifiedForSendingStatus,
            MailFrom:MailFromAttributes.MailFromDomain,MailFromStatus:MailFromAttributes.MailFromDomainStatus,
            FeedbackForwarding:FeedbackForwardingStatus,ConfigSet:ConfigurationSetName}'
```
```json
{ "DKIM": "SUCCESS", "VerifiedForSending": true, "MailFrom": "mail.erickaldama.com",
  "MailFromStatus": "SUCCESS", "FeedbackForwarding": false, "ConfigSet": "mail-config" }
```

### ④ Human — smoke via the Mailbox Simulator

The simulator costs no quota, needs no recipient verification, and exercises the bounce/complaint
event path. From must be exactly `erick@erickaldama.com` (the `ses:FromAddress` condition).

```bash
aws ses send-email --from erick@erickaldama.com \
  --destination "ToAddresses=success@simulator.amazonses.com" \
  --message "Subject={Data=SP-2 smoke success},Body={Text={Data=hello from erickaldama.com}}" \
  --configuration-set-name mail-config --region us-east-1 --profile AdministratorAccess-367707589526
# repeat with bounce@ and complaint@simulator.amazonses.com
```

| Path | MessageId |
|---|---|
| success | `0100019ed32b820a-4a04c715-2ad4-4a56-835e-30769025d91b-000000` |
| bounce  | `0100019ed32cc507-7718728c-4e0a-4675-8d3b-cb5bd061094c-000000` |
| complaint | `0100019ed32d8fcd-94fc8592-70d8-453a-be02-c883161d1319-000000` |

> `aws ses send-email` (SES v1 CLI shape) consumes the same `ses:SendEmail` IAM action the `mail-send`
> policy authorizes — the v1/v2 CLI choice is cosmetic; the IAM action is identical.

### Fix mid-deploy — Finding #7 (read-only could not verify the event path)

When the agent tried to verify the SNS topic and EventBridge rule, `mail-readonly` got `AccessDenied`
on `sns:ListTopics` and `events:DescribeRule` — the allowlist is pure (same class as SP-1's
`ListHostedZonesByName`). The agent was blind to the observability of what it had just deployed.
Governance decision: widen `mail-readonly` (FoundationStack) with read-only SNS + EventBridge actions,
scoped to `us-east-1`, hard-deny untouched. Redeployed `FoundationStack` (`UPDATE_COMPLETE`, 25.56s).
Pinned with a template assertion so a refactor cannot silently re-blind the verifier.
**Lesson: the agent's read-only must be able to READ everything the agent deploys — observability is
part of the boundary, not an extra.**

### ⑤ Agent — verify the event path end-to-end (read-only, after the read widening)

```bash
aws events describe-rule --name mail-ses-bounce-complaint --profile mail-readonly --region us-east-1 \
  --query '{State:State,EventPattern:EventPattern}'
```
```json
{ "State": "ENABLED",
  "EventPattern": "{\"detail-type\":[\"Email Bounce\",\"Email Complaint\"],\"source\":[\"aws.ses\"]}" }
```

```bash
aws events list-targets-by-rule --rule mail-ses-bounce-complaint --profile mail-readonly --region us-east-1 \
  --query 'Targets[].Arn'
# ["arn:aws:sns:us-east-1:367707589526:mail-bounce-complaint"]
```

The SES metrics lit up after the smoke (proof the events were generated):

```bash
aws cloudwatch list-metrics --namespace AWS/SES --profile mail-readonly --region us-east-1 \
  --query 'Metrics[].MetricName'
# Send, Delivery, Bounce, Complaint, Reputation.BounceRate, Reputation.ComplaintRate
#   (the Reputation.* metrics carry dimension ses:configuration-set = mail-config)
```

```bash
aws cloudwatch describe-alarms --profile mail-readonly --region us-east-1 \
  --query "MetricAlarms[?contains(MetricName,'Reputation')].{Name:AlarmName,State:StateValue,Threshold:Threshold,TreatMissing:TreatMissingData}"
```
```json
[ {"Name":"mail-bounce-rate","State":"INSUFFICIENT_DATA","Threshold":0.02,"TreatMissing":"ignore"},
  {"Name":"mail-complaint-rate","State":"INSUFFICIENT_DATA","Threshold":0.0005,"TreatMissing":"ignore"} ]
```

`INSUFFICIENT_DATA` is the **correct idle state**, not a failure: `Reputation.*Rate` is aggregated in
windows, and with only three messages there are no datapoints yet. `treatMissingData: ignore` is
exactly why the alarms don't fire false positives without sustained traffic.

Final inventory of the deployed stack:

```bash
aws cloudformation list-stack-resources --stack-name SendingStack --profile mail-readonly --region us-east-1 \
  --query 'StackResourceSummaries[].ResourceType' --output text | tr '\t' '\n' | sort | uniq -c
```
```
   1 AWS::CDK::Metadata
   2 AWS::CloudWatch::Alarm
   1 AWS::Events::Rule
   1 AWS::IAM::ManagedPolicy
   1 AWS::IAM::Role
   6 AWS::Route53::RecordSet          # 6, NOT 7 — the SPF TXT was not duplicated (audit canary)
   1 AWS::SES::ConfigurationSet
   1 AWS::SES::ConfigurationSetEventDestination
   1 AWS::SES::EmailIdentity
   1 AWS::SNS::Topic
   1 AWS::SNS::TopicPolicy
```

## The two findings, distilled

1. **Boundary must mirror the exec-policy.** When you widen the CFN exec-policy with a new service,
   widen the permissions boundary with the same permission in the same change — the boundary
   intersects, it does not union. The deploy fails with *"no permissions boundary allows …"*, which
   reads like an exec-policy problem but is not.
2. **The agent's read-only must cover what the agent deploys.** A pure allowlist that can deploy SNS +
   EventBridge but cannot read them leaves the verifier blind. Observability reads belong inside the
   read-only boundary.

Both are now encoded: `events:*` lives in both `iam/deploy-exec-policy.json` and
`iam/erickaldama-boundary.json`; the SNS/EventBridge reads live in the FoundationStack read-only
policy with a template assertion guarding them.
