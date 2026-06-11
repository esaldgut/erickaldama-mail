# SP-0 Bootstrap (t=0) — sanctioned manual exception to the CDK-Go premise

The MCP `aws-api` needs a read-only IAM principal. Creating it IS an AWS write, but the CDK-Go governance
does not exist yet at t=0 (chicken-and-egg). This is the one-time manual exception (like Terraform's state bucket).

## Procedure (human, with ADMIN credentials the agent NEVER sees)
1. Create an IAM user/role `mail-readonly` and attach `iam/readonly-policy.json` (inline), using profile
   `AdministratorAccess-367707589526`. Do this in the Console or `aws iam ... --profile AdministratorAccess-367707589526`.
2. Configure a NAMED profile `mail-readonly` in ~/.aws/config for that principal — NEVER `[default]`.
3. Run the ACCEPTANCE GATE (`iam/bootstrap-gate.sh`) BEFORE pointing the agent at it. It must pass fully.
4. Only then set the MCP's `AWS_API_MCP_PROFILE_NAME=mail-readonly` (already in .mcp.json) live.

## Ownership boundaries (resolved)
- **IAM:** SP-0 DEFINES and VERIFIES this policy (it is SP-0's boundary). SP-1 FORMALIZES it as CDK-Go
  (base IAM is SP-1's domain) and re-runs this same gate. The debt cannot silently widen the boundary.
- **CloudWatch alarms:** SP-0's ses-domain-recipe EMITS the alarm constructs as recipe artifacts; SP-3 OWNS
  and DEPLOYS them. SP-0 does not deploy.

## The deploy credential (out-of-band, SEC2-C3)
Mutations (`cdk deploy`) run with a SEPARATE named profile `mail-deploy` that the agent NEVER selects, ideally
on another machine / CloudShell. The agent's session is pinned to `mail-readonly`. Negative test below.

## Note: sts:GetSessionToken / GetFederationToken behavior (verified 2026-06-08)
AWS lets an IAM user call `sts:GetSessionToken` on its OWN credentials regardless of an identity-policy
`Deny` — the returned temporary credentials INHERIT the user's permissions (read-only here) and cannot
escalate. Verified live: a mail-readonly session token cannot do `iam list-access-keys`, `sts assume-role`,
or any mutation. The real credential-minting risk is `sts:AssumeRole` (escalates to a DIFFERENT role),
which IS denied and IS observably blocked. The explicit `Deny sts:GetSessionToken`/`GetFederationToken`
in iam/readonly-policy.json stays as harmless defense-in-depth. The acceptance gate therefore verifies
NON-ESCALATION of the minted token rather than an (unenforceable) outright deny.

## Acceptance record (T13, 2026-06-08 — sanitized)

Principal created by the human with admin creds (out of the agent): IAM user `mail-readonly`,
ARN `arn:aws:iam::367707589526:user/mail-readonly`, inline policy `mail-readonly-boundary` =
`iam/readonly-policy.json` (4-statement, verified vs SAR). Named profile `mail-readonly` configured
(region us-east-1; never `[default]`).

**Live verification — PASSED:**
- `bootstrap-gate.sh` (profile mail-readonly) → **GATE PASS** (exit 0):
  - DENY: ses send-email, sts assume-role, s3 get-object, iam list-access-keys.
  - GetSessionToken NON-ESCALATION: minted token cannot do iam list-access-keys (read-only inherited).
  - ALLOW: ses get-account (us-east-1, regional), route53 list-hosted-zones (global, unconditioned).
  - REGION-PIN: ses get-account in eu-west-1 → denied.
- `simulate-matrix.sh` (admin profile, RO_PRINCIPAL_ARN) → **SIMULATE MATRIX PASS** (exit 0):
  - allowed: ses:GetAccount, cloudformation:DescribeStacks (regional, with region context); sts:GetCallerIdentity, route53:ListHostedZones (global).
  - explicitDeny: ses:SendEmail, sts:AssumeRole, sts:GetSessionToken, sts:GetFederationToken, s3:GetObject, cloudformation:GetTemplate, ses:GetIdentityPolicies, iam:ListAccessKeys.
  - implicitDeny: lambda:InvokeFunction (allowlist-pure confirmed).
- Pre-flight confirmed: account 367707589526, ARN .../user/mail-readonly (not admin).

**Latent-bug fix confirmed live:** `sts:GetCallerIdentity` succeeded under the mail-readonly profile (CLI v2
regional STS endpoint) — proving the move to the UNCONDITIONED statement fixed the region-pin bug that would
have broken the pre-flight in the old 2-statement policy.

**Deferred to SP-1:** the out-of-band negative test (after a real `cdk deploy` with a separate `mail-deploy`
profile, confirm the agent's next command still resolves to mail-readonly). SP-0 deploys no infra, so there is
no mutation to run out-of-band yet. SP-1 must create `mail-deploy` and run this test on its first deploy.

## SP-1 bootstrap (t=0, human, SSO Admin) — 2026-06-10

The agent NEVER runs these. The human runs them out-of-band with SSO
`AdministratorAccess-367707589526`. The agent prepares the exact commands and verifies after.
(Note: the SP-0 "Deferred to SP-1" note above mentioned a `mail-deploy` principal — that was
removed in the SP-1 design. The human deploys with SSO Admin; no long-lived deploy keys exist.)

### 1. Create the boundary + exec-policy managed policies (first time only)
The first `cdk bootstrap` references `erickaldama-boundary` by name, so it must exist first.
The FoundationStack also emits these as CDK-managed; after the first deploy, CFN owns them.
For the FIRST bootstrap, create them from the JSON mirrors:

    aws iam create-policy --policy-name erickaldama-boundary \
      --policy-document file://iam/erickaldama-boundary.json \
      --profile AdministratorAccess-367707589526
    aws iam create-policy --policy-name erickaldama-deploy-exec \
      --policy-document file://iam/deploy-exec-policy.json \
      --profile AdministratorAccess-367707589526

### 2. Bootstrap the environment (one-time)
    cdk bootstrap aws://367707589526/us-east-1 \
      --custom-permissions-boundary erickaldama-boundary \
      --cloudformation-execution-policies arn:aws:iam::367707589526:policy/erickaldama-deploy-exec \
      --profile AdministratorAccess-367707589526

### 3. Deploy the FoundationStack
    cdk deploy FoundationStack --profile AdministratorAccess-367707589526
    # Note the CfnOutput "NameServers" (4 NS) and "HostedZoneId".

### 4. Update the registrar with the new name servers
    aws route53domains update-domain-nameservers --region us-east-1 \
      --domain-name erickaldama.com \
      --nameservers Name=<ns1> Name=<ns2> Name=<ns3> Name=<ns4> \
      --profile AdministratorAccess-367707589526
    # Async — poll: aws route53domains get-operation-detail --operation-id <id> --region us-east-1

### Deferred SP-0/T13 test satisfied here
The deploy uses SSO Admin (≠ the agent's mail-readonly profile). After deploy, the agent runs
iam/post-deploy-identity-check.sh and confirms its own identity is still mail-readonly.
