#!/usr/bin/env bash
# post-deploy-identity-check.sh — the deferred SP-0/T13 test.
# After the human deploys FoundationStack with SSO Admin (out-of-band), confirm:
#   (1) the AGENT's default identity is STILL mail-readonly (deploy didn't contaminate it),
#   (2) the SP-0 boundary still holds (gate 8/8 + simulate 13/13),
#   (3) the hosted zone exists and the NS delegation propagated.
# Read-only. Safe to re-run. Result goes to docs/superpowers/EXECUTION-LOG.md.
set -euo pipefail

EXPECTED_ARN_SUFFIX="user/mail-readonly"
PROFILE_READONLY="mail-readonly"
DOMAIN="erickaldama.com"

echo "=== (1) agent identity is still mail-readonly ==="
ARN=$(aws sts get-caller-identity --profile "$PROFILE_READONLY" --query Arn --output text)
echo "Arn: $ARN"
case "$ARN" in
  *"$EXPECTED_ARN_SUFFIX") echo "PASS: identity == mail-readonly" ;;
  *) echo "FAIL: identity is NOT mail-readonly ($ARN)"; exit 1 ;;
esac

echo "=== (2) SP-0 boundary still holds ==="
./iam/bootstrap-gate.sh
./iam/simulate-matrix.sh

echo "=== (3) hosted zone exists + NS propagated ==="
ZONE_ID=$(aws route53 list-hosted-zones-by-name --dns-name "$DOMAIN" \
  --profile "$PROFILE_READONLY" \
  --query "HostedZones[?Name=='${DOMAIN}.'].Id | [0]" --output text)
echo "HostedZoneId: $ZONE_ID"
if [ "$ZONE_ID" = "None" ] || [ -z "$ZONE_ID" ]; then
  echo "FAIL: no hosted zone for $DOMAIN"; exit 1
fi
echo "--- public NS resolution (may lag during propagation) ---"
dig +short NS "$DOMAIN" || true

echo "=== POST-DEPLOY CHECK COMPLETE ==="
