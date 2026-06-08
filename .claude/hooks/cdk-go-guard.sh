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
  # Assumes stdin is a pipe that reaches EOF (always true under Claude Code's hook invocation).
command -v jq >/dev/null 2>&1 || emit_deny '"jq missing, fail-safe deny"'

CMD="$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty')"
CWD="$(printf '%s' "$INPUT" | jq -r '.cwd // empty')"
PMODE="$(printf '%s' "$INPUT" | jq -r '.permission_mode // "default"')"

# SEC-C2: bypass mode must not evaporate the deny.
if [[ "$PMODE" != "default" && "$PMODE" != "plan" ]]; then
  emit_deny '"permission_mode not default/plan; governed command denied"'
fi

# SEC-I4: scope to the mail project. MAIL_ROOT overridable for tests; default to the canonical path.
MAIL_ROOT="${MAIL_ROOT:-$HOME/dev/src/go/src/erickaldama-mail}"
real_cwd="$(cd "$CWD" 2>/dev/null && pwd -P || printf '%s' "$CWD")"
real_root="$(cd "$MAIL_ROOT" 2>/dev/null && pwd -P || printf '%s' "$MAIL_ROOT")"
# segment-boundary match (not raw prefix): cwd == root OR cwd starts with root + "/"
if [[ "$real_cwd" != "$real_root" && "$real_cwd" != "$real_root"/* ]]; then
  emit_allow  # out of scope → no-op
fi

# (command-inspection logic added in Task 3)
emit_deny '"unimplemented, fail-safe deny"'
