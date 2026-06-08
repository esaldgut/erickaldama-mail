#!/usr/bin/env bash
# cdk-go-guard.sh — PreToolUse hook (FRICTION/UX, scoped-to-mail-project, self-deny on error).
# NOT a security boundary (docs: hooks fail open). The boundary is IAM (see iam/readonly-policy.json).
# Default action is DENY; any internal error self-denies. Reads JSON on stdin.

DECISION_LOG="${DECISION_LOG:-$HOME/.claude/hooks/decision-log.json}"

# Sanitize: strip secret-shaped tokens before logging (SEC E1/N1).
sanitize() {
  printf '%s' "$1" | sed -E \
    -e 's/(AKIA|ASIA)[A-Z0-9]{16}/<AWS_KEY>/g' \
    -e 's/AWS_SECRET_ACCESS_KEY=[^[:space:]]+/AWS_SECRET_ACCESS_KEY=<redacted>/g' \
    -e 's/(aws_secret_access_key|--secret-access-key)([[:space:]=]+)[^[:space:]]+/\1\2<redacted>/gi' \
    -e 's/--token-code[[:space:]]+[0-9]+/--token-code <redacted>/g'
}

audit() { # $1=decision $2=tool
  local line
  line="$(jq -nc --arg d "$1" --arg t "$2" --arg c "$(sanitize "${CMD:-}")" \
    '{ts:(now|todate),decision:$d,tool:$t,command:$c}')" || return 0
  mkdir -p "$(dirname "$DECISION_LOG")" 2>/dev/null || true
  printf '%s\n' "$line" >> "$DECISION_LOG" 2>/dev/null || true
}

emit_deny() { audit deny "${TOOL:-Bash}"; printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":%s}}\n' "$1"; exit 0; }
emit_allow() { audit allow "${TOOL:-Bash}"; printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}\n'; exit 0; }

# SEC-C1: any error path -> deny, never trust the harness to fail closed.
trap 'emit_deny "\"hook error, fail-safe deny\""' ERR
set -uo pipefail

INPUT="$(cat)" || emit_deny '"no stdin, fail-safe deny"'
  # Assumes stdin is a pipe that reaches EOF (always true under Claude Code's hook invocation).
command -v jq >/dev/null 2>&1 || emit_deny '"jq missing, fail-safe deny"'

CMD="$(printf '%s' "$INPUT" | jq -r '.tool_input.command // empty')"
CWD="$(printf '%s' "$INPUT" | jq -r '.cwd // empty')"
PMODE="$(printf '%s' "$INPUT" | jq -r '.permission_mode // "default"')"

TOOL="$(printf '%s' "$INPUT" | jq -r '.tool_name // "Bash"')"
if [[ "$TOOL" == mcp__aws-api__* ]]; then
  # Inspect the MCP call's AWS command; allowlist of reads, else deny (best-effort; IAM is the boundary).
  MCMD="$(printf '%s' "$INPUT" | jq -r '.tool_input.cli_command // .tool_input.command // empty')"
  MSUB="$(printf '%s' "$MCMD" | awk '{print $3}')"
  case "$MSUB" in
    get-session-token|get-federation-token) emit_deny '"sts credential-minting via MCP denied; IAM enforces, runs out-of-band"' ;;
    describe*|list*|get*|ls) emit_allow ;;
    *) emit_deny '"AWS mutation via MCP denied; IAM enforces, runs out-of-band"' ;;
  esac
fi

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
