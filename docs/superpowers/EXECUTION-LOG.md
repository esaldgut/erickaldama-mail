# EXECUTION-LOG — flujo de implementación (estado durable, sobrevive sesiones)

> Journal de ejecución del plan global del sistema de correo erickaldama.com (SP-0..SP-4).
> Capa 1 de persistencia: checkboxes en cada plan. Capa 2: este log (qué/quién/commit/hallazgos).
> Capa 3: task framework (#14..#18). Modo: subagent-driven-development (subagente fresco/tarea + review).
> Patrón canónico (tarea #21): cada subproyecto vive en su propio WORKTREE aislado para paralelismo.

## Worktrees activos (dónde vive qué trabajo)

| Subproyecto | Rama | Worktree path | Plan | Estado |
|---|---|---|---|---|
| SP-0 Gobernanza CDK-Go | `worktree-sp-0-governance` | `.claude/worktrees/sp-0-governance` | `docs/superpowers/plans/2026-06-08-sp0-governance-cdk-go.md` | EN CURSO |
| SP-1..SP-4 | (pendientes) | (cada uno su worktree) | (pendientes) | pending |

Repo base: `~/dev/src/go/src/erickaldama-mail` (main, commit base 725fb88). worktree.baseRef=head (sin remoto).
Cuenta AWS: ErickSA 367707589526, us-east-1.

---

## SP-0 — progreso de las 13 tareas

Fase 1 (hook bash, offline) · Fase 2 (plugin, sin AWS) · Fase 3 (IAM read-only, gate humano en T13).

| # | Tarea | Estado | Commit | Review (spec/calidad) | Notas/hallazgos |
|---|---|---|---|---|---|
| T1 | Hook test harness + fail-safe skeleton | ✅ done | 87e3646 | spec ✅ / calidad ✅ | TDD red→green ok; fail-safe-deny verificado por construcción. Minor: añadir comentario "stdin=pipe-EOF" en T2 |
| T2 | permission_mode gate + scope check | ✅ done | c22922e→6219898 | spec ✅ / calidad ✅ (tras fix) | quality cazó I-1: scope debía ir ANTES de bypass (hook no-op fuera del mail) + M-1 empty-cwd deny. Fix + test regresión. 4 tests verdes |
| T3 | Metachar deny + allowlist + aws/cdk refine | ✅ done | fead53d→06142d2 | spec ✅ / calidad ✅ | TDD; implementador cazó bug del plan (grep $'\n' rompe en BSD/macOS) → fix portable [[==*\n*]]. Quality OK (column-shift solo over-deny). Hardening SEC2: deny sts get-session/federation-token. 7 tests verdes |
| T4 | Wire settings.json + audit log | ✅ done | 0dff928→5da5f60 | spec ✅ / calidad ✅ (tras fix) | wiring 2 matchers + MCP branch + audit. Quality cazó IMP-1: secreto plaintext (aws configure set / --secret-access-key) se filtraba al log → fix sanitize. + mkdir-p (MIN-2) + paridad MCP-Bash credential-minting (MIN-3, revertía T3 en MCP). 12 tests verdes. **FASE 1 (hook) COMPLETA** |
| T5 | Plugin manifest + .mcp.json | ✅ done | 5b0fcf8 | spec ✅ | `claude plugin validate --strict` PASS (v2.1.169). author objeto, mcpServers pointer, server-key aws-api. config simple → solo spec-review |
| T6 | cdk-verifier agent + cdk-go-recipe skill | ✅ done | 4a44bb4 | spec ✅ (fidelidad) | agent tools = solo Knowledge MCP (no Bash/Write/aws-api); skill encode decisiones auditadas sin softening (best-effort dispatch, anti-poison, go-denied-by-hook, out-of-band F3). validate --strict PASS |
| T7 | ses-domain-recipe skill | ✅ done | 6acadce | spec ✅ (fidelidad) | 8 pasos + 6 trampas VERBATIM (SigningHostedZone, RFC 7208, v1-ses-NOT-sesv2) + thresholds + alarmas CDK-Go + ownership SP-0-emite/SP-3-deploya. validate --strict PASS |
| T8 | Eval harness (golden + assertions + Pass@k) | ✅ done | 7a2d397→aa4169b | spec ✅ / calidad ✅ (tras fix) | TDD assertion engine. Quality cazó 2 Important: (1) runner sin ignición (main en package eval → go run falla) → cmd/runeval package main real; (2) Pass@1==Pass@3 (métrica falsa) → cómputo correcto por triples. + regexes ws-resilientes + tests negativos pinneados. Suite verde. **FASE 2 (plugin) COMPLETA** |
| T9 | Verify call_aws tool_input shape (spike) | ✅ done | 944784b | spec ✅ (research+doc) | verificado vs server.py: key=cli_command, YA cubierto (sin cambio de hook). Caveat: cli_command puede ser list[str] (batch) → awk ve solo 1ª línea → un batch podría esconder mutación; pero cae a *)deny y Capa 1 IAM es el límite. Caveat → input para tarea #20 (hook global). 12 tests intactos |
| T10 | Canonical IAM allowlist policy | pending | — | — | — |
| T11 | Bootstrap doc + acceptance-gate script | pending | — | — | — |
| T12 | simulate-principal-policy matrix | pending | — | — | — |
| T13 | Live bootstrap acceptance (GATE HUMANO) | pending | — | — | humano crea principal admin; agente solo verifica read-only |

## Bitácora cronológica (append-only)

- 2026-06-08 — Worktree `sp-0-governance` creado desde 725fb88. Journal inicializado. Arrancando T1.
- 2026-06-08 — T1 ✅ (commit 87e3646). go.mod (erickaldama-mail, go 1.26.4) + test/hook harness + fail-safe-deny
  skeleton. Spec-review ✅ (cero over-build), quality-review ✅ (invariante fail-safe verificado en todo path).
  Pendiente menor para T2: comentario "stdin es pipe que alcanza EOF". Siguiente: T2.
- 2026-06-08 — T2 ✅ (c22922e + fix 6219898). Gate permission_mode + scope check. Quality-review cazó un
  defecto REAL de diseño (I-1): el gate de bypass disparaba antes del scope → denegaba trabajo en OTROS
  proyectos (sample-ios-app) en modo bypass, violando "hook = no-op fuera del mail". Fix: scope ANTES de bypass
  + guard empty-cwd (M-1) + test de regresión. La review valió. 4 tests verdes. Comentario stdin-EOF añadido. Siguiente: T3.
- 2026-06-08 — T3 ✅ (fead53d + hardening 06142d2). Lógica core del hook: metachar-deny + VAR=strip +
  allowlist de comandos + refinamiento aws-read/cdk-Go. TDD destapó un BUG DEL PLAN: grep -q newline da
  falso-positivo en BSD grep (macOS) → habría denegado TODO; fix portable bash-native. Quality ✅ (divergencias
  solo sesgan a deny; get-session-token over-allow aceptable porque IAM es el límite, pero añadí deny targeted
  SEC2 igual). El hook Bash tiene su lógica completa. 7 tests verdes. Siguiente: T4 (wiring + audit log).
- 2026-06-08 — T4 ✅ (0dff928 + fix 5da5f60). Wiring settings.json (matchers Bash + mcp__aws-api__.*) +
  audit log sanitizado + rama MCP del hook. Quality cazó IMP-1 (secreto plaintext de `aws configure set
  aws_secret_access_key` se filtraba al audit log — el punto mismo de la tarea) → sanitize ampliado + test
  anti-leak. + mkdir-p log dir + paridad MCP/Bash en credential-minting (MIN-3 revertía T3 en la superficie MCP).
  12 tests verdes. >>> FASE 1 (el hook completo, testeado offline) COMPLETA. Siguiente: FASE 2 — T5 (plugin manifest).
- 2026-06-08 — T5 ✅ (5b0fcf8): manifest + .mcp.json, validate --strict PASS. T6 ✅ (4a44bb4): cdk-verifier
  agent (Knowledge-MCP-only) + cdk-go-recipe skill, decisiones auditadas fieles. T7 ✅ (6acadce):
  ses-domain-recipe (8 pasos + 6 trampas verbatim + alarmas). T8 ✅ (7a2d397+aa4169b): eval harness;
  quality cazó 2 Important (runner sin ignición → cmd/runeval package main; Pass@1==Pass@3 → métrica real).
  >>> FASE 2 (el plugin completo, validate --strict + eval) COMPLETA. 8/13. Siguiente: FASE 3 — T9..T13 (IAM,
  read-only; T13 = GATE HUMANO, el humano crea el principal IAM admin).
