#!/usr/bin/env bash
# Bootstrap acceptance gate (SEC2-I1). Run BEFORE pointing the agent at the read-only principal.
# All probes are read-only or expected-to-be-denied; nothing here mutates.
# Applies aws-cli-pre-flight-canonical: verify identity + account before any service call.
set -uo pipefail
PROFILE="${MAIL_RO_PROFILE:-mail-readonly}"
fail=0

echo "== aws-cli-pre-flight-canonical =="
acct="$(aws sts get-caller-identity --profile "$PROFILE" --query Account --output text 2>/dev/null)"
[[ "$acct" == "367707589526" ]] || { echo "FAIL: wrong/empty account: '$acct' (expected 367707589526)"; exit 1; }
echo "ok: account 367707589526"

# expect_denied <description> <aws args...>
expect_denied() {
  local desc="$1"; shift
  if aws "$@" --profile "$PROFILE" >/dev/null 2>&1; then
    echo "FAIL (should be denied): $desc"; fail=1
  else
    echo "ok (denied): $desc"
  fi
}
# expect_allowed <description> <aws args...>
expect_allowed() {
  local desc="$1"; shift
  if aws "$@" --profile "$PROFILE" >/dev/null 2>&1; then
    echo "ok (allowed): $desc"
  else
    echo "FAIL (should be allowed): $desc"; fail=1
  fi
}

expect_denied  "ses send-email"        sesv2 send-email --region us-east-1 --from-email-address a@b.com --destination ToAddresses=c@d.com --content '{"Simple":{"Subject":{"Data":"x"},"Body":{"Text":{"Data":"y"}}}}'
expect_denied  "sts assume-role"       sts assume-role --role-arn arn:aws:iam::367707589526:role/none --role-session-name s
expect_denied  "s3 get-object (mail)"  s3api get-object --bucket erickaldama-mail-raw --key any /dev/null
expect_denied  "iam list-access-keys"  iam list-access-keys
expect_allowed "ses get-account read"  sesv2 get-account --region us-east-1

[[ "$fail" -eq 0 ]] && echo "GATE PASS" || { echo "GATE FAIL"; exit 1; }
