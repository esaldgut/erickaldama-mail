#!/usr/bin/env bash
# ses-dkim-wait.sh — the async DKIM verification gate (SP-2).
# A `cdk deploy` of SendingStack returns CREATE_COMPLETE with DKIM still PENDING — verification is
# asynchronous (minutes with Route53 same-account, up to 72h worst case). This polls until DKIM
# is SUCCESS and the identity is verified for sending, BEFORE the smoke. Read-only. Safe to re-run.
set -euo pipefail

IDENTITY="erickaldama.com"
PROFILE="${SES_PROFILE:-mail-readonly}"   # agent reads with mail-readonly (ses:Get* is allowed)
MAX_ATTEMPTS="${MAX_ATTEMPTS:-30}"        # 30 × 60s = 30 min cap
SLEEP_SECS="${SLEEP_SECS:-60}"

echo "=== waiting for DKIM=SUCCESS on $IDENTITY (profile $PROFILE) ==="
attempt=0
while [ "$attempt" -lt "$MAX_ATTEMPTS" ]; do
  attempt=$((attempt + 1))
  STATUS=$(aws sesv2 get-email-identity --email-identity "$IDENTITY" \
    --profile "$PROFILE" --region us-east-1 \
    --query 'DkimAttributes.Status' --output text 2>/dev/null || echo "ERROR")
  VERIFIED=$(aws sesv2 get-email-identity --email-identity "$IDENTITY" \
    --profile "$PROFILE" --region us-east-1 \
    --query 'VerifiedForSendingStatus' --output text 2>/dev/null || echo "ERROR")
  echo "attempt $attempt/$MAX_ATTEMPTS: DKIM=$STATUS VerifiedForSending=$VERIFIED"
  if [ "$STATUS" = "SUCCESS" ] && [ "$VERIFIED" = "True" ]; then
    echo "PASS: DKIM verified, identity ready for sending"
    exit 0
  fi
  if [ "$STATUS" = "FAILED" ]; then
    echo "FAIL: DKIM status FAILED — check the CNAMEs in the hosted zone"; exit 1
  fi
  sleep "$SLEEP_SECS"
done
echo "TIMEOUT: DKIM still not SUCCESS after $MAX_ATTEMPTS attempts (propagation can take up to 72h)"
exit 2
