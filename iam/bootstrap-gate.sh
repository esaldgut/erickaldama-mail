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

# expect_session_token_no_escalation: GetSessionToken on the user's OWN creds is allowed by AWS design
# (the token inherits the user's read-only perms and CANNOT be observably denied via identity policy).
# What matters is that the resulting token does NOT escalate. We mint a token and confirm it cannot
# do a privileged op (iam list-access-keys) nor assume a role. (The Deny in the policy stays as
# harmless defense-in-depth.)
expect_session_token_no_escalation() {
  local creds akid skey stok
  creds="$(aws sts get-session-token --profile "$PROFILE" --duration-seconds 900 --output json 2>/dev/null)" || {
    echo "ok (get-session-token denied outright): no escalation possible"; return
  }
  akid="$(printf '%s' "$creds" | jq -r '.Credentials.AccessKeyId // empty')"
  skey="$(printf '%s' "$creds" | jq -r '.Credentials.SecretAccessKey // empty')"
  stok="$(printf '%s' "$creds" | jq -r '.Credentials.SessionToken // empty')"
  if [[ -z "$akid" || -z "$skey" || -z "$stok" ]]; then
    echo "ok (get-session-token returned no usable creds): no escalation"; return
  fi
  # Use the minted token; assert it CANNOT escalate.
  if AWS_ACCESS_KEY_ID="$akid" AWS_SECRET_ACCESS_KEY="$skey" AWS_SESSION_TOKEN="$stok" \
       aws iam list-access-keys >/dev/null 2>&1; then
    echo "FAIL (session token ESCALATED): iam list-access-keys succeeded with the minted token"; fail=1
  else
    echo "ok (session token does NOT escalate): iam list-access-keys denied with the minted token"
  fi
}

# --- DENY: mutation / credential-minting / recon (HardDeny + implicit-deny) ---
expect_denied  "ses send-email"        sesv2 send-email --region us-east-1 --from-email-address a@b.com --destination ToAddresses=c@d.com --content '{"Simple":{"Subject":{"Data":"x"},"Body":{"Text":{"Data":"y"}}}}'
expect_denied  "sts assume-role"       sts assume-role --role-arn arn:aws:iam::367707589526:role/none --role-session-name s
expect_session_token_no_escalation     # GetSessionToken on own creds is allowed by AWS; verify it does NOT escalate (see docs/BOOTSTRAP.md)
expect_denied  "s3 get-object (mail)"  s3api get-object --bucket erickaldama-mail-raw --key any /dev/null
expect_denied  "iam list-access-keys"  iam list-access-keys

# --- ALLOW: regional read (region-pinned) + global read (unconditioned) ---
expect_allowed "ses get-account read"  sesv2 get-account --region us-east-1
expect_allowed "route53 list-zones (global, unconditioned)"  route53 list-hosted-zones

# --- region-pin enforcement: the SAME regional read in a different region must be DENIED ---
expect_denied  "ses get-account in eu-west-1 (region-pin)"  sesv2 get-account --region eu-west-1

[[ "$fail" -eq 0 ]] && echo "GATE PASS" || { echo "GATE FAIL"; exit 1; }
