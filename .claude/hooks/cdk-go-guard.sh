#!/usr/bin/env bash
# cdk-go-guard.sh — PreToolUse hook (FRICTION/UX, scoped-to-mail-project, self-deny on error).
# NOT a security boundary (docs: hooks fail open). The boundary is IAM (see iam/readonly-policy.json).
# Default action is DENY; any internal error self-denies. Reads JSON on stdin.

emit_deny() { printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":%s}}\n' "$1"; exit 0; }
emit_allow() { printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}\n'; exit 0; }

# SEC-C1: any error path -> deny, never trust the harness to fail closed.
trap 'emit_deny "\"hook error, fail-safe deny\""' ERR
set -uo pipefail

INPUT="$(cat)" || emit_deny '"no stdin, fail-safe deny"'
command -v jq >/dev/null 2>&1 || emit_deny '"jq missing, fail-safe deny"'

CMD="$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty')"
CWD="$(printf '%s' "$INPUT" | jq -r '.cwd // empty')"
PMODE="$(printf '%s' "$INPUT" | jq -r '.permission_mode // "default"')"

# (logic added in following tasks)
emit_deny '"unimplemented, fail-safe deny"'
