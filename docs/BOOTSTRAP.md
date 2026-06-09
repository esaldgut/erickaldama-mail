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

## Acceptance record
(Filled in at Task 13 after the live gate + simulate matrix + out-of-band negative test pass. Sanitized.)
