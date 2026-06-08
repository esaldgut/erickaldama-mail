#!/usr/bin/env bash
# Falsifiability of the allowlist (SEC2-I2): simulate the read-only principal's policy and assert the matrix.
# Run with an ADMIN/separate profile (simulate-principal-policy needs iam:Simulate*, NOT in the read-only set).
set -uo pipefail
ADMIN="${ADMIN_PROFILE:-AdministratorAccess-367707589526}"
PRINCIPAL_ARN="${RO_PRINCIPAL_ARN:?set RO_PRINCIPAL_ARN to the mail-readonly user/role arn}"
fail=0

# _sim <expected> <action> [extra args...] — core evaluator; pass-through extra args to the CLI.
_sim() {
  local expect="$1"; local action="$2"; shift 2
  local dec
  dec="$(aws iam simulate-principal-policy --profile "$ADMIN" \
    --policy-source-arn "$PRINCIPAL_ARN" --action-names "$action" "$@" \
    --query 'EvaluationResults[0].EvalDecision' --output text 2>/dev/null)"
  if [[ "$dec" == "$expect" || ( "$expect" == "*Deny" && "$dec" == *Deny ) ]]; then
    echo "ok $action -> $dec"
  else
    echo "FAIL $action -> $dec (expected $expect)"; fail=1
  fi
}

# sim <expected> <action> — global / unconditioned actions and all denies (no region context).
sim() { _sim "$1" "$2"; }

# sim_regional <expected> <action> — region-pinned Allows (Statements 2 & 3). Pass aws:RequestedRegion=us-east-1
# so the StringEquals condition is satisfied; without it simulate evaluates WITHOUT region context and the
# region-pinned Allow would WRONGLY return implicitDeny (latent false-FAIL).
sim_regional() {
  _sim "$1" "$2" \
    --context-entries ContextKeyName=aws:RequestedRegion,ContextKeyValues=us-east-1,ContextKeyType=string
}

# intended-allow — REGIONAL (Statements 2 & 3): pass region context so the us-east-1 condition is met.
sim_regional allowed  ses:GetAccount
sim_regional allowed  cloudformation:DescribeStacks
# intended-allow — GLOBAL (Statement 1, unconditioned): NO region context; allows regardless of region.
sim allowed  sts:GetCallerIdentity
sim allowed  route53:ListHostedZones
# intended-deny — explicit HardDeny set (verified vs SAR 2026-06-08). Explicit Deny wins regardless of region.
sim "*Deny"  ses:SendEmail
sim "*Deny"  sts:AssumeRole
sim "*Deny"  sts:GetSessionToken
sim "*Deny"  sts:GetFederationToken
sim "*Deny"  s3:GetObject
sim "*Deny"  cloudformation:GetTemplate
sim "*Deny"  ses:GetIdentityPolicies
sim "*Deny"  iam:ListAccessKeys
# intended-deny — implicit (not in any Allow)
sim "*Deny"  lambda:InvokeFunction

# NOTE: simulate-principal-policy evaluates identity policy WITHOUT a region context by default. The
# REGIONAL intended-allows (Statements 2 & 3) therefore use sim_regional (aws:RequestedRegion=us-east-1)
# to satisfy the StringEquals condition; the GLOBAL allows (Statement 1, unconditioned) and the explicit
# denies need no context. The live gate (bootstrap-gate.sh eu-west-1 probe) additionally confirms the
# region condition DENIES the same regional read outside us-east-1.

[[ "$fail" -eq 0 ]] && echo "SIMULATE MATRIX PASS" || { echo "SIMULATE MATRIX FAIL"; exit 1; }
