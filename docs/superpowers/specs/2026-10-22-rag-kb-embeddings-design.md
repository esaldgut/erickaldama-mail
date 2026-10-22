# RAG/KB con pipeline de embeddings (Bedrock Titan) — spec de diseño

**Estado:** aprobado, listo para plan.
**Frente:** base de conocimiento RAG-ready para erickaldama-mail — chunks `.md` del conocimiento destilado de los
frentes (clases de defecto, decisiones con base, contexto del proyecto) + un pipeline que los embeddea con AWS
Bedrock Titan a un manifest `index.jsonl` versionado. Diseñado en `docs/KB/` de este repo, con formato genérico
para crecer cross-proyecto.
**Rama:** `feature/rag-kb-embeddings` (nuevo frente; no mezclar con la bitácora de arriba).

---

## 1. Motivación

Dos frentes de esta sesión (v0.7.1 tool-description hardening + NDA parametrización) produjeron conocimiento
reusable — clases de defecto, decisiones con base, lecciones de método — hoy disperso entre CHANGELOG,
METHODOLOGY, case-studies, skills y memorias. Una base de conocimiento **RAG-ready** (chunks embeddeados,
recuperables por similitud semántica) convierte ese conocimiento en algo consultable por un agente futuro: "¿cómo
saqué el account de un repo público?" recupera el chunk correcto, con su fuente. Es la pieza que cierra el barrido
de conocimiento de la sesión como un artefacto consultable, no solo narrativo.

## 2. Arquitectura — 4 componentes con límites claros

```
docs/KB/
  sources/           chunks .md temáticos, versionados, legibles — LA FUENTE DE VERDAD
    <slug>.md          uno por lección/decisión/contexto, con frontmatter
  build_kb.py        el pipeline: lee sources/*.md → guard de tokens → embeddea (Bedrock Titan) → index.jsonl
  query_kb.py        la utilidad de consumo de referencia: embeddea una query, carga index.jsonl, cosine top-k
                     (es también el smoke de retrieval de §7 — prueba que el índice recupera lo correcto)
  index.jsonl        el manifest versionado: 1 línea {id, source_ref, title, tags, type, project, text, embedding}
  README.md          cómo correr (--profile explícito), el esquema, cómo consumir el índice para retrieval
```

**Flujo de datos:**
```
sources/*.md (frontmatter + cuerpo)
   │  build_kb.py parsea frontmatter, usa el cuerpo como texto del chunk
   ▼  GUARD: token-count ≤ límite de Titan (si excede, ABORTA ruidoso — NO trunca)
Bedrock amazon.titan-embed-text-v2  (boto3 Session(profile_name=<profile>), --profile explícito)
   │  un invoke por chunk → vector 1024
   ▼
index.jsonl  (chunk + vector, 1 línea c/u, ordenado por id → diffable)  ← versionado
   │
   ▼  consumo futuro: un retriever carga el .jsonl, embeddea la query con el MISMO modelo, cosine top-k
```

**Decisiones de diseño:**
- **`sources/*.md` es la fuente de verdad**, no el `.jsonl`. Se editan a mano, legibles, diffables. El `.jsonl` se
  REGENERA del pipeline (reproducible: mismo input → mismo output salvo cambio del modelo Titan).
- **Un chunk = una unidad semántica** (una clase de defecto, una decisión, un contexto), NO un trozo de tamaño
  fijo. Trocear por idea es lo que hace bueno el retrieval, y evita que un chunk exceda el límite de tokens.
- **El frontmatter lleva `tags` + `source_ref` + `type` + `project`** — hace el retrieval explicable y permite el
  crecimiento cross-proyecto (un chunk de otro repo trae su propio `project`/`source_ref`).
- **`--profile AdministratorAccess-<account>` explícito** en el pipeline (regla del frente NDA —
  `aws-cli-pre-flight-canonical`, nunca el default de la shell para llamadas AWS).

## 3. Esquema del chunk `sources/<slug>.md`

```markdown
---
id: iac-inject-env-not-hardcode         # kebab-case, único, = nombre del archivo sin .md
title: "IaC — inyecta el account, no lo hardcodees"
tags: [iac, cdk, nda, aws, account, byte-identity]
source_ref: "CASE-STUDY-NDA-PARAMETRIZATION.md · skill:iac-inject-env-never-hardcode-account"
type: defect-class                       # defect-class | decision | context
project: erickaldama-mail                # permite el crecimiento cross-proyecto
---

<cuerpo: 150-300 palabras que DESTILAN la lección + el caso + cómo aplicarla.
 NO copia el documento completo — lo referencia por source_ref. Mantiene el chunk
 bajo el límite de tokens de Titan por construcción.>
```

## 4. Poblado inicial — 14 chunks (primera versión)

**Clases de defecto (type: defect-class):**
1. `tool-description-mechanism-not-result` — un tool `description` describe el mecanismo, no un resultado corpus-dependiente (v0.7.1).
2. `downstream-rejects-non-ascii` — la clase em-dash/no-ASCII que un consumidor downstream rechaza (AWS SSO, backend LLM).
3. `iac-inject-env-not-hardcode` — #3 Env struct / #4 tokens / #1 prohibido (NDA).
4. `plan-audit-catches-pre-code-defects` — auditar el plan caza consumidores omitidos + CRIT del CI antes de escribir código.
5. `spike-is-go-no-go` — el spike de 1 unidad cazó el CRIT del CI en vivo (--profile / CDK_DEFAULT_ACCOUNT).
6. `byte-identity-excludes-lambda-asset` — el método de verificación y su trampa (el hash del asset).
7. `local-env-hides-ci-gate` — el gofmt que local no ejercita.
8. `layered-audit-distinct-defect-classes` — cada capa de review caza una clase distinta (los 9 fogueos).

**Decisiones con base (type: decision):**
9. `decision-approach-a-over-strip` — v0.7.1: Description sobre strip (AR-1 ya cubre el peor caso).
10. `decision-env3-over-tokens4` — NDA: #3 sobre #4 (byte-identidad vs idiomática).
11. `identifier-vs-credential` — la distinción que decide urgencia (rotar vs parametrizar).

**Contexto (type: context):**
12. `project-overview` — qué es erickaldama-mail, arquitectura, estado.
13. `sp0-governance-hook` — el hook que fuerza git/go a humano.
14. `agent-mb-1-wire-single-mailbox` — el fast-follow abierto.

YAGNI: NO se chunkea todo METHODOLOGY ni cada runbook SP-* (narrativa de deploy, no lecciones reusables). Se
añaden si un retrieval futuro los necesita.

## 5. El pipeline `build_kb.py` (Python + boto3)

```
1. Lee sources/*.md, parsea frontmatter (PyYAML) + cuerpo.
2. GUARD anti-truncado-silencioso: cuenta tokens del cuerpo; si excede el límite de titan-embed-text-v2
   (8192 tokens), ABORTA ruidoso nombrando el chunk — NO lo trunca ni lo parte solo.
3. Pre-flight: sts get-caller-identity con el --profile → verifica que el account matchea el esperado
   antes de gastar invokes (aws-cli-pre-flight-canonical). Si no matchea, aborta.
4. Por cada chunk: bedrock-runtime.invoke_model(modelId="amazon.titan-embed-text-v2:0", body=inputText)
   → vector 1024. Session(profile_name=<profile>).
5. Determinismo: ordena los chunks por id; escribe index.jsonl con una línea por chunk
   {id, source_ref, title, tags, type, project, text, embedding:[1024 floats]}.
```

## 6. Manejo de errores (robustez)

- **Profile/account equivocado:** pre-flight `sts get-caller-identity` aborta si el account no matchea (NUNCA
  confiar en el default — el frente NDA probó que el default apuntaba a otra cuenta).
- **Throttle de Bedrock:** retry con backoff exponencial en `ThrottlingException`.
- **Un chunk falla el embed:** ABORTA todo — NO escribe un `.jsonl` parcial que parezca completo (un índice
  parcial da retrieval ciego, peor que ninguno).
- **El token-count guard (paso 2)** es la defensa clave contra `embedding-api-truncates-silently`: un chunk que
  excede el límite se nombra y el pipeline para, en vez de embeddear un texto truncado en silencio.

## 7. Testing

Es un script operativo, no una lib con invariantes complejos. La verificación realista es de dos capas:
- **Smoke del pipeline:** correrlo contra los 14 chunks reales → `index.jsonl` tiene 14 líneas, cada una con un
  `embedding` de exactamente 1024 floats; re-correrlo sin cambios produce el mismo output (determinismo).
- **Smoke de retrieval (el valor real):** una query de prueba (`"cómo saco el account del repo público"`) cargada +
  embeddeada con el mismo modelo debe recuperar el chunk `iac-inject-env-not-hardcode` en el top-3 por cosine.
  Esto prueba que el embedding SIRVE (recupera lo correcto), no solo que el pipeline corre —
  `search-query-requires-live-index-smoke` (un índice que existe ≠ un índice que recupera lo correcto). Un script
  `query_kb.py` corto hace esta verificación (y queda como la utilidad de consumo de referencia).

No hay guards de seguridad con dientes que mutar aquí (no es `mutation-test-must-have-teeth` territory) — el
smoke de retrieval es el test que importa.

## 8. Restricción del ejecutor (hook SP-0)

El hook `.claude/hooks/cdk-go-guard.sh` deniega `python`/`aws` al agente. **El agente escribe `build_kb.py`,
`query_kb.py` y los 14 chunks `.md`; el HUMANO corre `build_kb.py --profile AdministratorAccess-<account>`** para
generar `index.jsonl` y `query_kb.py` para el smoke de retrieval, y pega el output para que el agente verifique
(14 líneas, 1024 dims, el retrieval recupera el chunk esperado).

## 9. Fuera de alcance (registrado, no en esta versión)

- **Índice cross-proyecto real** — el formato (`project` en el frontmatter, `source_ref`) está diseñado para
  soportarlo, pero agregar chunks de yaan/erickaldama-site es un frente futuro.
- **Un vector store/DB** (OpenSearch, pgvector, Bedrock Knowledge Base) — el `.jsonl` plano basta para decenas de
  chunks; un store se justifica solo con miles. YAGNI.
- **Re-chunk automático al cambiar un doc fuente** — hoy los `sources/*.md` se editan a mano; un sync automático
  desde CASE-STUDY/METHODOLOGY es deuda futura.

## 10. Verificación (el gate)

- `index.jsonl` = 14 líneas, cada una con `embedding` de 1024 floats (smoke del pipeline).
- El smoke de retrieval recupera `iac-inject-env-not-hardcode` en el top-3 para su query de prueba.
- Re-correr `build_kb.py` sin cambios en `sources/` produce un `index.jsonl` idéntico (determinismo).
- Cero fuga NDA: los chunks ya están redactados (`grep <account> docs/KB/` = 0).

## 11. Referencias

- Conocimiento fuente: `docs/CASE-STUDY-v0.7.1.md`, `docs/CASE-STUDY-NDA-PARAMETRIZATION.md`,
  `docs/METHODOLOGY.md` (fogueos), las memorias del proyecto, los skills universales.
- Provider: AWS Bedrock `amazon.titan-embed-text-v2:0` (1024 dims).
- Reglas aplicadas: `aws-cli-pre-flight-canonical` (--profile explícito), `embedding-api-truncates-silently`
  (token guard), `search-query-requires-live-index-smoke` (el retrieval se prueba, no se asume),
  `history-in-embedding-poisons-retrieval` (NO se concatena historial al texto que se embeddea — solo el cuerpo
  del chunk).
