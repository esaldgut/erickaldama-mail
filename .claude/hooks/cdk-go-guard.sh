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

# SEC-I4 (scope FIRST): the hook is a no-op outside the mail project. Judge scope before any mode reasoning,
# so out-of-scope work in ANY permission mode passes through untouched (the hook is friction, not a boundary).
MAIL_ROOT="${MAIL_ROOT:-$HOME/dev/src/go/src/erickaldama-mail}"
# M-1: empty cwd is unresolvable → fail-safe deny (don't rely on `cd ""` staying in the hook's own dir).
[[ -z "$CWD" ]] && emit_deny '"missing cwd, fail-safe deny"'
real_cwd="$(cd "$CWD" 2>/dev/null && pwd -P || printf '%s' "$CWD")"
real_root="$(cd "$MAIL_ROOT" 2>/dev/null && pwd -P || printf '%s' "$MAIL_ROOT")"
# segment-boundary match (not raw prefix): cwd == root OR cwd starts with root + "/"
if [[ "$real_cwd" != "$real_root" && "$real_cwd" != "$real_root"/* ]]; then
  emit_allow  # out of scope → no-op (wins over the bypass gate below)
fi

# SEC-C2 (in-scope only): bypass/non-default modes must not evaporate the deny WITHIN the mail project.
if [[ "$PMODE" != "default" && "$PMODE" != "plan" ]]; then
  emit_deny '"permission_mode not default/plan; governed command denied"'
fi

# SEC-C3/I2: deny compound/substitution commands rather than parsing shell grammar.
# Word-bounded on the raw command. (eval is handled by the allowlist below, not here, to avoid
# false-positives on substrings like --query 'retrieval'.)
if printf '%s' "$CMD" | grep -Eq '(\&\&|\|\||;|\||\$\(|`|>|<|&[^&])' || [[ "$CMD" == *$'\n'* ]]; then
  emit_deny '"compound command (metacharacters); hand it to the human"'
fi

# Strip leading VAR=val assignments, then take the first token.
STRIPPED="$(printf '%s' "$CMD" | sed -E 's/^([[:space:]]*[A-Za-z_][A-Za-z0-9_]*=[^[:space:]]*[[:space:]]+)+//')"
FIRST="$(printf '%s' "$STRIPPED" | awk '{print $1}')"

case "$FIRST" in
  aws)
    SUB="$(printf '%s' "$STRIPPED" | awk '{print $3}')"
    SVCSUB="$(printf '%s' "$STRIPPED" | awk '{print $2" "$3}')"
    case "$SVCSUB" in
      "sts get-caller-identity") emit_allow ;;
      "sts get-session-token"|"sts get-federation-token") emit_deny '"sts credential-minting denied (IAM is the boundary; run out-of-band)"' ;;
    esac
    case "$SUB" in
      describe*|list*|get*|ls|help) emit_allow ;;
      *) emit_deny '"AWS mutation runs out-of-band (human); not via the agent"' ;;
    esac
    ;;
  cdk)
    CJSON="$CWD/cdk.json"
    [[ -f "$CJSON" ]] || emit_deny '"cdk.json missing; cannot confirm CDK-Go"'
    APP="$(jq -r '.app // empty' "$CJSON" 2>/dev/null)" || emit_deny '"cdk.json malformed; deny"'
    if ! printf '%s' "$APP" | grep -Eq 'go (run|mod)'; then
      emit_deny '"CDK must be Go (cdk.json app is not go run/go mod)"'
    fi
    SUB="$(printf '%s' "$STRIPPED" | awk '{print $2}')"
    case "$SUB" in
      diff|synth|ls) emit_allow ;;
      *) emit_deny '"cdk mutation (deploy/destroy) runs out-of-band (human)"' ;;
    esac
    ;;
  ls|cat|echo|grep|head|tail|jq|pwd|which|mkdir) emit_allow ;;
  *) emit_deny '"command not in narrow allowlist (go/git/find/env/scripts are execution engines); hand it to the human"' ;;
esac
