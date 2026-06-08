---
name: ses-domain-recipe
description: Use when provisioning or operating Amazon SES for a domain — verifying a domain identity, DKIM, custom MAIL FROM, DMARC, configuration sets, requesting production access (sandbox exit), or setting up inbound receiving. Orchestrates the 8-step SES setup in dependency order, verifying each API against live AWS docs, and includes the post-provisioning reputation runbook.
---

# SES domain recipe — 8 steps, in dependency order

For Claude Code. Provisioning steps generate CDK-Go constructs (delegating "how to write Go" to cdk-go-recipe);
account-operations (steps 6, 8) are NOT CDK — they are commands/docs handed to the human. Each provisioning step
runs the 4 verify-before-act phases (same cache/agent mechanism as cdk-go-recipe).

## The 8 steps
1. **Domain identity (DKIM-based)** — infra (CDK-Go). Creates the 3 DKIM CNAMEs.
2. **DKIM verification** — depends on 1; ≤72h to Verified.
3. **Custom MAIL FROM** (`mail.erickaldama.com`) — infra; MX + SPF; for SPF→DMARC alignment.
4. **DMARC** (`_dmarc`, `p=none` → `quarantine` → `reject`) — infra; depends on 3.
5. **Configuration set + event destination** — infra; BEFORE sending (bounce/complaint capture).
6. **Sandbox exit (production access)** — ACCOUNT OPERATION → hand `aws sesv2 put-account-details` to the human;
   there is no CloudFormation construct for this.
7. **Inbound receiving** (receipt rule → S3 → Lambda) — infra; uses the **v1 `ses` API, NOT sesv2** (trap #6).
8. **Post-provisioning runbook** — see below; presented to the human, NOT executed.

## 6 traps (guardrails — verify against live docs, never assume)
1. Domain verification IS DKIM-based: steps 1+2 are ONE DNS transaction, not two.
2. DKIM suffix is region-dependent → derive from `SigningHostedZone`, NEVER hardcode `dkim.amazonses.com`.
3. Sandbox exit ≠ quota jump → after production access, read the live quota and request an increase separately.
4. `put-account-details` returns 409 after a denial → fall back to the Service Quotas API.
5. SPF has a 10 DNS-lookup limit (RFC 7208) → count lookups before writing the record.
6. Receiving uses the v1 `ses` API, NOT `sesv2`.

## Step 8 — reputation runbook (the skill GENERATES the alarm constructs; SP-3 deploys them)
Critical thresholds (AWS pauses sending account-wide): bounce > 5%, complaint > 0.5%. If crossed: STOP sending
immediately. CloudWatch alarms (conservative warning, well below the review line) — the skill EMITS this CDK-Go
as a recipe artifact (SP-3 owns/deploys the stack; SP-0 does not deploy):
```go
cloudwatch.NewAlarm(stack, jsii.String("SESBounceRateAlarm"), &cloudwatch.AlarmProps{
    Metric:             ses.MetricBounceRate(),
    Threshold:          jsii.Number(2),
    ComparisonOperator: cloudwatch.ComparisonOperator_GREATER_THAN_THRESHOLD,
    EvaluationPeriods:  jsii.Number(2),
})
// + SESBounceRateCritical (5%), SESComplaintRateAlarm (0.05%), SESComplaintRateCritical (0.5%) → SNS
```
"Reputation in the red" runbook: 1) pause sending; 2) identify cause (suppression list? content? dirty list?);
3) clear suppression list (`aws sesv2 put-suppressed-destination` — handed to the human); 4) request quota
increase (only if bounce is throttling, not quality); 5) resume gradually (1% → 10% → 100% while monitoring).

## --dry-run
Run verify/read/GENERATE the constructs + commands, but do NOT ask the human to execute. Used by the eval harness.
