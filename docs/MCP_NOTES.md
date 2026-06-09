# MCP Notes — awslabs aws-api MCP `call_aws` input shape

**SP-0, Phase 3 (IAM boundary), Task 9 — verification spike.**
Verified: **2026-06-08**

## What was verified

Back in Phase 1, the hook's MCP branch (governing `mcp__aws-api__*` calls)
*assumed* the aws-api MCP's command-execution tool carries the AWS CLI command
under `.tool_input.cli_command`, with a `.tool_input.command` fallback and a
safe `*) deny` default. Task 9 verifies that assumption against the real source.

## Finding

| Item | Verified value |
| --- | --- |
| Tool name | **`call_aws`** |
| Input key carrying the CLI command | **`cli_command`** |
| Type of that key | **`str \| list[str]`** (single command **or** a list of complete AWS CLI commands) |
| Nesting | Top-level argument of the tool (surfaced under `.tool_input.cli_command` in the hook's PreToolUse payload) |
| Extra param | `max_results: int \| None` (optional pagination cap) — not command-bearing |

### Sources

- Tool name + purpose ("Executes AWS CLI commands with validation and proper
  error handling"):
  `https://raw.githubusercontent.com/awslabs/mcp/main/src/aws-api-mcp-server/README.md`
  (fetched 2026-06-08). The README names the tool but does **not** publish the
  parameter schema.
- Exact parameter name + type, from the tool's source:
  `https://raw.githubusercontent.com/awslabs/mcp/main/src/aws-api-mcp-server/awslabs/aws_api_mcp_server/server.py`
  (fetched 2026-06-08). The `call_aws` function signature is:

  ```python
  async def call_aws(
      cli_command: Annotated[
          str | list[str],
          Field(description='A single command or a list of complete AWS CLI commands to execute'),
      ],
      ctx: Context,
      max_results: Annotated[
          int | None,
          Field(description='Optional limit for number of results (useful for pagination)'),
      ] = None,
  ) -> list[CallAWSResponse]:
  ```

## Reconciliation result

**No code change needed.** The verified key is `cli_command`, which is already
the **primary** key in the hook's existing extraction chain:

```bash
MCMD="$(printf '%s' "$INPUT" | jq -r '.tool_input.cli_command // .tool_input.command // empty')"
```

The hook reads `.tool_input.cli_command` first (correct), falls back to
`.tool_input.command` (kept for safety), and otherwise emits empty → which
flows to the `*) deny` default. The existing MCP fixtures
(`TestMcpWriteDenied`, `TestMcpReadAllowed`, `TestMcpDeniesStsCredentialMinting`)
use `cli_command` and continue to pass.

## How the hook treats this key (and the boundary)

The hook's MCP branch reads `.tool_input.<key>` — specifically
`.tool_input.cli_command` (with the `.tool_input.command` fallback). If the arg
is un-inspectable, it denies all non-read `mcp__aws-api__*` calls (the
`*) deny` default). **Capa 1 (the IAM read-only policy) enforces regardless, so
the hook's MCP inspection is best-effort defense-in-depth, not the boundary.**

### Known best-effort limitation: `cli_command` can be a `list[str]`

`cli_command` accepts a **list** of commands (batch mode), not only a single
string. When the value is a list, `jq -r` emits newline-separated entries and
the hook's `awk '{print $3}'` inspects only the first line's third token. A
crafted batch could therefore present a read-looking first command while
carrying a mutation in a later list entry, or split tokens across lines.

This is acceptable and **does not change the conclusion**: the hook is
friction/UX defense-in-depth, hooks fail open by design (see the hook header),
and any command the hook fails to classify as a read still hits the `*) deny`
default. **The real enforcement is Capa 1 — the IAM read-only policy
(`iam/readonly-policy.json`) — which denies mutating AWS API calls out-of-band
regardless of what the hook does.** The hook narrows agent friction; IAM is the
boundary.
