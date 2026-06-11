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
| T10 | Canonical IAM allowlist policy | ✅ done | 3438ad5 | spec ✅ (boundary fidelity) | EL LÍMITE. Allow 15 acciones exactas (region-pinned us-east-1), Deny 5 (ses:Send*/AssumeRole/GetObject/GetTemplate/iam:*). Ausencias SEC2 confirmadas (sin s3:GetObject/iam:*/sts:Get*/GetTemplate). Lógica deny+allow = reads-only sin recon/mint/mail-content |
| T11 | Bootstrap doc + acceptance-gate script | ✅ done | 0187a65 | spec ✅ | BOOTSTRAP.md (excepción t=0, ownership SP-0/SP-1/SP-3) + bootstrap-gate.sh (pre-flight account + 5 probes espejo de policy T10: 4 deny + 1 allow). bash -n ok, NO corrido (principal no existe hasta T13) |
| T12 | simulate-principal-policy matrix | ✅ done | 076c567 | spec ✅ | simulate-matrix.sh: 3 intended-allow + 6 intended-deny vía iam:simulate-principal-policy (corre con admin profile separado, NO el read-only). Alinea con policy T10. bash -n ok, NO corrido (necesita principal T13) |
| T13 | Live bootstrap acceptance (GATE HUMANO) | ✅ done | 5e0df9e | live ✅ | Humano creó mail-readonly. bootstrap-gate.sh → GATE PASS (8/8). simulate-matrix.sh → SIMULATE MATRIX PASS (13/13). Límite IAM verificado EN VIVO vs cuenta real. Hallazgo: GetSessionToken self-token no se deniega observablemente (AWS) pero NO escala (verificado) → probe re-calibrada a no-escalada. Out-of-band test DIFERIDO a SP-1 (#15, no hay deploy aún) |
| — IAM policy verificada vs SAR | (mejora de T10/T11/T12) | ✅ done | 6781ce4 | spec ✅ (5 artefactos coinciden) | propuesta del usuario → 2 agentes verificaron vs SAR oficial → policy de 4 statements (global-unconditioned + 2 regional-pinned + hard-deny). CAZÓ BUG: GetCallerIdentity bajo region-cond rompía pre-flight. +S3 ARN-scoping del usuario. +deny GetSessionToken/GetFederationToken (Read pero minters). Cero weakening (comm vacío) |

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
- 2026-06-08 — T9 ✅ (944784b): call_aws=cli_command verificado, batch caveat → #20. T10 ✅ (3438ad5): policy
  IAM canónica (EL LÍMITE), allowlist-pure region-pinned + hard-deny, fidelidad de boundary verificada. T11 ✅
  (0187a65): BOOTSTRAP.md + bootstrap-gate.sh (5 probes espejo de policy). T12 ✅ (076c567): simulate-matrix.sh
  (falsabilidad vía simulate-principal-policy, admin separado). 12/13. >>> Solo queda T13 = GATE HUMANO:
  el HUMANO crea el principal mail-readonly con su cred admin (el agente NO); luego el agente corre las
  verificaciones read-only (gate + simulate-matrix + test negativo out-of-band). Pausa para el humano.
- 2026-06-08 — MEJORA DE POLICY (antes de T13): el usuario propuso una policy alternativa. 2 agentes la
  verificaron contra el AWS Service Authorization Reference oficial (doc 09). Resultado: la policy mejoró Y
  se cazó un BUG LATENTE en la T10 original (sts:GetCallerIdentity bajo aws:RequestedRegion=us-east-1 rompía
  el pre-flight bajo CLI v2 endpoint regional → AccessDenied). Policy reescrita a 4 statements (commit 6781ce4):
  global-unconditioned (STS GetCallerIdentity + Route53, ambos GLOBALES) + 2 regional-pinned (SES/CFN/CW + S3
  con ARN-scoping *erickaldama* del usuario) + hard-deny por nombre (añadidos GetSessionToken/GetFederationToken,
  que son Read pero mintean credenciales). Eliminado cloudformation:Deploy* (acción inexistente). Confirmado: NO
  existe prefijo sesv2: (v1+v2 = ses:). gate+simulate+spec+plan reconciliados. Spec-review: 5 artefactos coinciden,
  cero weakening (comm old-vs-new vacío). PUNTO DE RETOMA: T13 = gate humano, el humano crea mail-readonly.
- 2026-06-08 — T13 ✅ EN VIVO. El humano creó el IAM user mail-readonly con la policy verificada. Corrí las
  verificaciones read-only: bootstrap-gate.sh → GATE PASS (8/8); simulate-matrix.sh → SIMULATE MATRIX PASS
  (13/13). EL LÍMITE IAM ESTÁ VERIFICADO EN VIVO CONTRA LA CUENTA REAL 367707589526. Hallazgo de la corrida:
  sts:GetSessionToken sobre el self-token NO se deniega observablemente (comportamiento AWS documentado) pero
  el token hereda read-only y NO escala (verificado en vivo: no iam, no assume-role, ni siquiera reads) → probe
  re-calibrada a verificar NO-ESCALADA, Deny en policy se mantiene como defensa-en-profundidad. Bug latente del
  region-pin CONFIRMADO arreglado (GetCallerIdentity funcionó bajo CLI v2 endpoint regional). Out-of-band test
  DIFERIDO a SP-1 (#15): SP-0 no despliega infra, no hay mutación que probar out-of-band; SP-1 crea mail-deploy
  y corre ese test en su primer cdk deploy.
  >>> SP-0 COMPLETO: 13/13 tareas. Hook (Fase 1) + Plugin (Fase 2) + IAM boundary verificado en vivo (Fase 3).

---

# SP-1 — Fundación DNS + cuenta (tarea #15) — 2026-06-10

Worktree aislado `sp-1-foundation` (rama worktree-sp-1-foundation, base d4ab603). Flujo: brainstorm → spec
(auditada por 4 agentes adversariales) → plan → subagent-driven-development (9 tareas) → deploy out-of-band
humano → verificación. Cuenta 367707589526, us-east-1.

## Tareas (subagent-driven, spec+quality review por tarea)
- T1 (d2ef0b5): CDK-Go module scaffold (awscdk v2.258.1, jsii v1.134.0, constructs v10.6.0). DONE+reviews.
- T2 (10f50ac): public hosted zone erickaldama.com + CAA Amazon-only (TDD). DONE+reviews.
- T3 (c8caa94): mail-readonly managed policy sobre user importado (prop Users, NO AddManagedPolicy →
  ValidationError; cero AWS::IAM::User). 4 statements mirror exacto de readonly-policy.json. DONE+reviews.
- T4 (15d05a7 + fb87a35): permissions boundary erickaldama-boundary. Quality-review cazó GAP IMPORTANTE:
  el boundary denegaba Delete*PermissionsBoundary pero no Put* → escape de la jaula. Fix: 10 deny actions
  (Put* + Delete* sellan la jaula). DONE+reviews.
- T5 (1bfde2a): stack tags + suite completa verde. DONE+reviews.
- T6 (7b48ad5): bootstrap JSON (exec-policy + boundary mirror) + BOOTSTRAP.md. Boundary JSON ≡ CDK (12 allow
  + 10 deny). DONE+reviews. Quality-review nota diferida: events:* para SP-3 si usa EventBridge.
- T8 (e0237e2): post-deploy-identity-check.sh (test diferido T13). Review cazó: identity check NO puede
  false-pass (bare-assignment $() bajo set -e aborta en fallo AWS o identidad incorrecta). DONE+review.
- T7 (HUMANO, out-of-band SSO Admin): bootstrap + deploy + (registrar pendiente). Ver hallazgos abajo.
- T9: este registro + plan checkboxes + RETOMAR + merge.

## Deploy LIVE EXITOSO (2026-06-10 22:39)
FoundationStack desplegado. HostedZoneId=Z023932911KA6S98A6ZRW. NameServers=ns-1845.awsdns-38.co.uk,
ns-1423.awsdns-49.org, ns-949.awsdns-54.net, ns-26.awsdns-03.com. Recursos: HostedZone + CAA + ManagedPolicy
readonly (3). Zona viva confirmada (list-resource-record-sets devuelve NS+CAA reales).

## Verificación post-deploy (test diferido SP-0/T13) — PASS
1. Identidad del agente == mail-readonly tras deploy con SSO Admin → PASS (deploy NO contaminó al agente).
2. bootstrap-gate.sh → GATE PASS (8/8). simulate-matrix.sh → SIMULATE MATRIX PASS (13/13).
3. Zona viva + CAA+NS presentes.
El boundary read-only de SP-0 sobrevivió intacto un deploy real. La tesis de gobernanza se sostiene en vivo.

## LOS 5 HALLAZGOS DEL PRIMER DEPLOY REAL (lecciones — el valor central de SP-1)
Ningún diseño de papel los anticipó. Cada uno verificado contra docs oficiales, resuelto al mínimo correcto.

1. **CLI-vs-librería version skew.** awscdk v2.258.1 sintetiza cloud-assembly schema 54.0.0; un `cdk` CLI
   viejo (schema ≤49) NO lee el manifest ("need at least CLI version 2.1126.0"). Síntoma engañoso: bootstrap
   imprime banners pero no completa → deploy falla "not bootstrapped". CAUSA RAÍZ: la regla "usar versión viva
   de la librería" acopla una restricción NO documentada al CLI (CLI ≥ schema de la lib). FIX: npm i -g aws-cdk@latest.
2. **exec-policy necesita ssm:GetParameters.** El cfn-exec-role resuelve el BootstrapVersion SSM parameter
   (AWS::SSM::Parameter::Value) en CADA deploy. AdministratorAccess (default) lo cubre; una exec-policy SCOPED
   debe añadir `ssm:GetParameters` sobre arn:...:parameter/cdk-bootstrap/* explícito. FIX verificado vs docs CDK.
3. **boundary TAMBIÉN necesita ssm.** Tras fix #2 el error cambió de "no identity-based policy allows" a "no
   permissions boundary allows" ssm:GetParameters. CAUSA RAÍZ (la lección más rica): un permissions boundary
   INTERSECTA (no une) — perm efectivo = identity policy ∩ boundary. Con --custom-permissions-boundary, CADA
   permiso que el MECANISMO de CDK necesita debe estar en AMBOS (exec-policy Y boundary). FIX: ssm:* en el techo.
4. **boundary 409 AlreadyExists.** El stack intentaba crear erickaldama-boundary, que debe PRE-EXISTIR para
   `cdk bootstrap --custom-permissions-boundary` (el humano la creó en t=0). CAUSA RAÍZ: el huevo-y-gallina del
   bootstrap — un recurso que debe preexistir para el bootstrap NO puede ser poseído por el stack. FIX: quitar
   el boundary del stack; es un artefacto de bootstrap (iam/erickaldama-boundary.json), gestionado out-of-band.
5. **ListHostedZonesByName fuera del allowlist (NO es bug — el boundary funcionando).** El script post-deploy
   usó list-hosted-zones-by-name → AccessDenied bajo mail-readonly, porque el allowlist de SP-0 tiene
   route53:ListHostedZones + GetHostedZone pero NO ListHostedZonesByName (acción distinta). Prueba VIVA de que
   el boundary es allowlist-puro real, no teatro: lo no explícitamente permitido se deniega. (El script T8 se
   ajustará a usar ListResourceRecordSets, que sí está permitido — ya verificado que devuelve la zona.)

## Cómo se vuelven productivos (4 destinos — decisión del usuario)
1. EXECUTION-LOG + dossier 10-sp1-audit-findings.md (este registro). HECHO.
2. Memorias personales feedback_* (transversales a cualquier CDK). HECHO (ver MEMORY.md).
3. Hardening del skill CDK-Go de SP-0 (checks pre-deploy idempotentes). PENDIENTE (#22).
4. Lecciones generalizables → lessongate → workspace público (#11).

## Re-delegación del registrar — SUCCESSFUL (2026-06-10 22:47)
UpdateDomainNameservers OperationId 16cb8c5d-7f36-4a50-a21b-b06b1340bc1f → Status SUCCESSFUL.
El registrar (Amazon Registrar) ahora apunta a los 4 NS nuevos de la zona (ns-1845.awsdns-38.co.uk,
ns-1423.awsdns-49.org, ns-949.awsdns-54.net, ns-26.awsdns-03.com), reemplazando el delegation set muerto.
Propagación DNS pública en curso (dig NS aún vacío al cierre — TTL normal, no bloquea).

>>> SP-1 COMPLETO: 9/9 tareas. FoundationStack desplegado en vivo, boundary read-only verificado intacto
post-deploy, registrar re-delegado. El examen de gobernanza PASÓ: el límite de SP-0 sobrevivió un deploy
real out-of-band. 5 hallazgos productivizados (EXECUTION-LOG + 3 memorias feedback_cdk_* + skill hardening #22
+ lessongate #11).
