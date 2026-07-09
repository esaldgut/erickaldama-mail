# SP-1 — Foundation: deploy runbook & the five first-deploy findings

> First real AWS provisioning of the project (2026-06-10), account `367707589526` (ErickSA),
> `us-east-1`. This is the bootstrap-and-first-deploy story — the procedure lives in
> [`BOOTSTRAP.md`](BOOTSTRAP.md); this file distills the **five findings from the very first `cdk
> bootstrap`/`cdk deploy`**, the ones that only surface when you scope IAM tightly instead of using
> AdministratorAccess. They shaped every deploy since.

## What SP-1 provisions (`FoundationStack`)

| Resource | Purpose |
|---|---|
| `AWS::Route53::HostedZone` (`erickaldama.com`) | Public hosted zone (`Z023932911KA6S98A6ZRW`) for all DNS records |
| `AWS::Route53::RecordSet` (CAA) | CAA pinned to Amazon (only ACM/Amazon may issue certs for the domain) |
| `AWS::IAM::ManagedPolicy` (`mail-readonly`) | The agent's read-only boundary: region-pinned allowlist + hard-deny on mutation/credential-minting/recon |

The permissions boundary `erickaldama-boundary` and the cfn-exec-policy `erickaldama-deploy-exec` are
**bootstrap artifacts**, NOT stack-owned — they must pre-exist for `cdk bootstrap
--custom-permissions-boundary`. They live as JSON mirrors in `iam/` and are managed out-of-band.

## The procedure (human, SSO Admin)

See [`BOOTSTRAP.md`](BOOTSTRAP.md) §"SP-1 bootstrap" for the exact commands:
1. Create the boundary + exec-policy managed policies (first time only).
2. `cdk bootstrap aws://367707589526/us-east-1 --custom-permissions-boundary erickaldama-boundary
   --cloudformation-execution-policies arn:aws:iam::367707589526:policy/erickaldama-deploy-exec`.
3. `cdk deploy FoundationStack`.
4. Update the registrar with the four NS from the `NameServers` output.
5. Agent runs `iam/post-deploy-identity-check.sh` — confirms its identity is still `mail-readonly`.

## The five findings from the first real deploy

These are the reason the project audits before implementing and verifies real output before declaring
done. Each is also a personal memory (`feedback_cdk_*`).

### #1 — CLI-vs-library schema skew (deceptive "not bootstrapped")
The project pins the *live* `aws-cdk-go/awscdk/v2` (v2.258.1), which synthesizes with cloud-assembly
schema 54.0.0. A stale `cdk` CLI cannot read it:
`Maximum schema version supported is 49.x.x, but found 54.0.0. You need at least CLI version 2.1126.0`.
The symptom is deceptive — the deploy later fails with "not bootstrapped" even though bootstrap printed
banners. **Fix:** `npm install -g aws-cdk@latest`. **Rule:** the CLI must be ≥ the library; "use the
live library version" implies "CLI @latest". (`feedback_cdk_cli_vs_library_version_skew`)

### #2 — The exec-policy needs `ssm:GetParameters`
A scoped cfn-exec-policy got `AccessDenied ssm:GetParameters` on the BootstrapVersion check that runs on
*every* deploy. **Fix:** add `ssm:GetParameters` on
`arn:aws:ssm:*:367707589526:parameter/cdk-bootstrap/*`. AdministratorAccess (the bootstrap default)
covers this silently — it only appears when you scope.

### #3 — The boundary INTERSECTS the exec-policy (so it needs ssm too)
After fixing #2, the error changed from "no identity-based policy allows" to "no **permissions
boundary** allows" — the boundary was a second gate. Effective permission = exec-policy ∩ boundary, so
`ssm:GetParameters` had to go in **both** the exec-policy and the boundary. **Rule:** any permission the
CDK *mechanism* needs lives in both layers. (`feedback_cdk_permissions_boundary_intersects` — this same
lesson reappeared in SP-2 with `events:*`.)

### #4 — The boundary is bootstrap-owned, not stack-owned (409 AlreadyExists)
The FoundationStack initially tried to create `erickaldama-boundary` as a CDK resource — but it must
pre-exist for `cdk bootstrap --custom-permissions-boundary`, so CFN got `409 AlreadyExists`. **Fix:**
remove the boundary from the stack; it is a bootstrap artifact (JSON mirror only), managed out-of-band
like the exec-policy. The stack owns exactly one managed policy (the readonly one); a template assertion
pins that. (`feedback_cdk_bootstrap_owned_not_stack_owned`)

### #5 — `ListHostedZonesByName` is outside the allowlist (not a bug)
The post-deploy script got `AccessDenied` on `route53:ListHostedZonesByName`. This is the allowlist
working as designed — it grants `ListHostedZones`/`GetHostedZone` but not the `-ByName` variant. **Fix:**
the script uses `list-hosted-zones` + a JMESPath filter. (The same class of finding recurred in SP-3 as
the SNS/EventBridge read gap — see finding #7/#8.)

## The thesis, confirmed live

The SP-0 read-only IAM boundary survived a real `cdk deploy` intact: the deploy ran with SSO Admin
(≠ the agent's `mail-readonly` profile), and afterward the agent's next command still resolved to
`mail-readonly`. The governance premise — *the agent never deploys; a hook + a scoped principal enforce
it; the human deploys out-of-band* — holds against real infrastructure, not just in theory.
