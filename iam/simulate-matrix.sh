#!/usr/bin/env bash
# Falsifiability of the allowlist (SEC2-I2): simulate the read-only principal's policy and assert the matrix.
# Run with an ADMIN/separate profile (simulate-principal-policy needs iam:Simulate*, NOT in the read-only set).
set -uo pipefail
ADMIN="${ADMIN_PROFILE:-AdministratorAccess-367707589526}"
PRINCIPAL_ARN="${RO_PRINCIPAL_ARN:?set RO_PRINCIPAL_ARN to the mail-readonly user/role arn}"
fail=0

sim() { # $1=expected(allowed|*Deny) $2=action
  local expect="$1"; local action="$2"
  local dec
  dec="$(aws iam simulate-principal-policy --profile "$ADMIN" \
    --policy-source-arn "$PRINCIPAL_ARN" --action-names "$action" \
    --query 'EvaluationResults[0].EvalDecision' --output text 2>/dev/null)"
  if [[ "$dec" == "$expect" || ( "$expect" == "*Deny" && "$dec" == *Deny ) ]]; then
    echo "ok $action -> $dec"
  else
    echo "FAIL $action -> $dec (expected $expect)"; fail=1
  fi
}

# intended-allow
sim allowed  ses:GetAccount
sim allowed  cloudformation:DescribeStacks
sim allowed  sts:GetCallerIdentity
# intended-deny (explicit or implicit)
sim "*Deny"  ses:SendEmail
sim "*Deny"  sts:AssumeRole
sim "*Deny"  s3:GetObject
sim "*Deny"  iam:ListAccessKeys
sim "*Deny"  cloudformation:GetTemplate
sim "*Deny"  lambda:InvokeFunction

[[ "$fail" -eq 0 ]] && echo "SIMULATE MATRIX PASS" || { echo "SIMULATE MATRIX FAIL"; exit 1; }
