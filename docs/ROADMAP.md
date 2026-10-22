# Roadmap — bitácora viva de qué está entregado y qué sigue

Este documento es la **bitácora versionada** de la evolución del cliente de correo `erickaldama-mail`: qué
versión se entregó, qué quedó diferido a propósito (fast-follow) dentro de cada versión, y qué candidatos de
versión todavía no arrancan. El backend (SP-0 … SP-4, provisionado con AWS CDK en Go) está desplegado y estable
en la cuenta `<ACCOUNT_ID>` (`us-east-1`); el cliente evoluciona en capas sucesivas encima de ese backend
(CLI → render rico → TUI → cache local → render de enlaces → FTS5 en el agente AI). El objetivo de este archivo
es que ningún hallazgo o próximo paso viva **solo** en un comentario de PR o en la conversación efímera de una
sesión — todo lo que hay que recordar entre sesiones está aquí, versionado en el repo.

---

## Entregado

| Versión | Qué | Merge | PR |
|---|---|---|---|
| v0.2 | config.toml multi-mailbox, CC/BCC con envelope privacy, reply-all, composer TUI multi-campo | `44bc7f6` | #10 |
| v0.3 | Render rico HTML+imágenes sanitizado (bluemonday + chafa + glamour) | `b107afd` | #12 |
| v0.4 | TUI estética de 2 paneles (lista + reader, viewport real, spinner async) | `a73e96e` | #13 |
| v0.5 | Cache local SQLite+FTS5 — `mail search`, hora+s3Key en `mail ls` | `b74e048` | #14 |
| v0.5.1 | Fix reader vacío + refresh de la TUI (manual `g` + auto 30s); CRITICAL de fuga de `inflight` | `f917968` | #15 |
| v0.6 | Render de enlaces del correo (OSC 8 + lista al pie), defensa de inyección terminal en 2 capas | `459d829` | #16 |
| v0.7 | FTS5 en las tools del agente AI (`search_messages`, `Searcher`/`fallbackSearcher`, AR-1) | `7bef535` | #18 |
| v0.7.1 | Endurecer el `Description` de `search_messages` contra operadores estilo Gmail (declarativo, ASCII, describe el mecanismo) | `b22449f` | #21 |

**Infra (no versión de cliente):**

| Frente | Qué | Merge | PR |
|---|---|---|---|
| NDA parametrización | Account ID + ARNs fuera del source público — `Env` struct inyectado (approach #3) + `EnvStackProps`, role ARNs a GitHub secrets, export de `CDK_DEFAULT_ACCOUNT` en CI, docs a `<ACCOUNT_ID>`. Byte-identidad verificada (`cdk diff` vacío); `grep <account>` = 0. | `ab70c95` | #22 |

Los SHA son el commit de merge de cada PR (`gh pr list --state merged`), no el HEAD del branch de feature —
la fuente de verdad si hace falta re-verificar es el historial de GitHub del repo.

---

## Fast-follows registrados (por versión, NO bloquean)

Ítems diferidos a propósito, con motivo explícito, que hoy viven dispersos entre comentarios de PR y memoria.
Consolidados aquí para que sobrevivan a través de sesiones.

### Infra NDA (PR #22)

- **Approach #4 (tokens CDK nativos) como evolución futura** — hoy #3 (Env struct) preserva byte-identidad. Si en
  algún momento se acepta que el template cambie (literal → `{"Ref":"AWS::AccountId"}`) y se reescriben los
  template-assertion tests para asertar tokens, #4 es el techo idiomático (el account desaparece también del
  template renderizado, no solo del source). No urgente — #3 ya saca el account del source. Ver el skill
  `iac-inject-env-never-hardcode-account`.
- **`Region` sigue como literal `us-east-1`** en varias condiciones IAM (`aws:RequestedRegion`) y en `cd.yml` —
  NO es fuga de account (región no es secreta), pero si algún día el proyecto se multi-región, parametrizarla
  por el mismo `Env` struct cierra la portabilidad. Deuda consciente, no urgente.
- **Los `iam/*.sh` leen `${EXPECTED_ACCOUNT}`** — el humano que los corre out-of-band debe exportarlo (documentado
  en `docs/BOOTSTRAP.md`). Candidato: un wrapper que lo derive de `aws sts get-caller-identity` para no exportarlo a
  mano. Menor.

### v0.7

- **PATRON-1 (MENOR)** — el árbol `DefaultPath→Open→Sync` es ahora una **4ª copia parcial** entre `mail ls` /
  `mail search` / `mail ai agent`. Candidato de refactor: extraer un helper compartido (`internal/cache` o
  `internal/wire`) que las tres invocaciones consuman. Diferido porque cada call-site difiere ligeramente en
  qué hace si el cache no está disponible. Ver `docs/v0.7-AI-FTS5.md` §8(a).
- **BP-1 (MENOR)** — el clamp `[1, 1000]` de `limit` (evita overflow de `int32(limit)`) lo aplica el caller
  (`execTool`) por convención, no `substringSearcher` mismo — defensa en profundidad diferida, no urgente
  mientras haya un solo caller. Ver `docs/v0.7-AI-FTS5.md` §8(b).
- **~~Endurecer el `Description` de `search_messages`~~ — RESUELTO por v0.7.1** (la primera de las dos
  mitigaciones de §8(c)). El Description se reescribió a ~4 oraciones ASCII que describen el mecanismo
  ("`from:amazon` se busca como las dos palabras literales `from` y `amazon`, no como filtro"); el smoke E2E
  con `qwen2.5:7b` confirmó `query="amazon"` limpia en 4 corridas (antes `from:amazon`). La segunda mitigación
  —**despojar operadores en el wire (Approach B)**— sigue diferida, condicionada a evidencia de un smoke donde
  un modelo siga inventando operadores pese al Description. Ver `CHANGELOG.md` §`[mail v0.7.1]` y
  `docs/superpowers/specs/2026-07-28-search-messages-operator-hardening-design.md`.
- **AGENT-MB-1 (deploy finding, descubierto por el smoke de v0.7.1)** — el wire de `mail ai agent`
  (`cmd/mail/main.go:511`) pasa un **único** `mailboxName` a `RunAgent`, mientras `ls` (`main.go:131`) y
  `search` (`main.go:537`) caen a `cfg.Mailboxes` (los 6 buzones del config) cuando `--mailbox` no se pasó. Con
  un buzón multi-mailbox, el agente reporta "sin coincidencias" honesto (AR-1 correcto) aunque el correo exista
  en otro mailbox del config — confirmado con evidencia binaria (`mail search --json` → los correos están en
  `mailbox#erick+awsbridge@erickaldama.com`, no en el default). Candidato: el wire del agent debería iterar
  `cfg.Mailboxes`, o `RunAgent`/`Searcher` aceptar múltiples mailboxes. Es su propio frente (pre-existente de
  v0.7).

### v0.6

- **LNK-S2** — URLs bare-text (fuera de `<a>`) las auto-linkifica goldmark, duplicándolas inline (comportamiento
  pre-existente de glamour, benigno). Candidato de fix: desactivar la extensión autolink.
- **D2** — los error-returns de `stripSentinelsFromHTML` (`html.Parse`/`Render`) son inalcanzables en la
  práctica; quedan sin cobertura de test.
- **P1** — `stripSentinelsFromHTML` es un 3er parse HTML del cuerpo del correo — tradeoff SRP consciente
  (O(n), ~295ms medido en un cuerpo de 2MB), no una regresión de performance a resolver.

### v0.5

- **MENOR-2** — patrón N+1 en `Sync` contra DynamoDB, acotado por `SyncPageLimit = 500` (el tope fijo limita
  el radio del problema, no lo elimina).
- **`cache.Sync` devuelve `len(hs)`, no "cuántas filas cambiaron"** — relevante para la eficiencia de un futuro
  auto-refresh incremental, y ahora también documentado como la **raíz** de H8-SYNC-N-1 de v0.7 (el wire de
  `mail ai agent` interpretó ese `n` como proxy del tamaño del índice, y no lo es). Ver
  `docs/v0.7-AI-FTS5.md` §3.

### v0.5.1

- **I-1** — `doRefresh` usa `context.Background()`; si el usuario sale de la TUI con un refresh en vuelo, la
  goroutine queda huérfana (no se cancela con el cierre de la app).
- **I-2** — `startReply` no participa del contador `inflight` — un reply iniciado durante una carga async no
  se coordina con el resto de los guards de concurrencia de la TUI.

---

## Candidatos de versión (NO empezados)

Backlog hacia adelante, sin fecha ni compromiso — ideas evaluadas pero sin brainstorm/spec todavía:

- **(a) ~~AI tools usando substring en vez de FTS5~~ — RESUELTO por v0.7.** Era el candidato (b) en versiones
  anteriores de este backlog; `search_messages` + `fallbackSearcher` lo cierran (ver `CHANGELOG.md` §`[mail
  v0.7]` y `docs/v0.7-AI-FTS5.md`).
- **(c) Orphan sweep del cache** — el cache local no detecta correo **borrado** en DynamoDB (la fuente de
  verdad); hoy la única forma de reconciliar una eliminación es borrar manualmente los archivos
  `.sqlite*` y dejar que el próximo `Sync` reconstruya el índice desde cero. Un sweep que compare rowids
  del cache contra un listado fresco de DynamoDB (o un TTL de invalidación) cerraría esto sin intervención
  manual.
- **(d) Integración TUI/AI más profunda** — hoy el agente AI (`mail ai agent`) es un subcomando separado de
  la TUI interactiva; un candidato de versión futura es traer el agente AI dentro de la TUI misma (un panel
  de chat, o comandos `:ai <goal>` estilo vim), reusando el mismo `Searcher`/cache ya construido.

---

## Notas de proceso

Este roadmap es la bitácora **versionada** de qué sigue — vive en el repo, no en memoria de sesión ni en
comentarios de PR que se archivan. La división de responsabilidades entre documentos:

- **`docs/vX-*.md`** (p. ej. `docs/v0.5-CACHE.md`, `docs/v0.7-AI-FTS5.md`) — el runbook técnico de cada
  versión: arquitectura, decisiones de diseño, hallazgos de auditoría con causa raíz, smoke E2E, deploy
  findings. v0.6 no tiene runbook propio — su narrativa completa vive en `CHANGELOG.md` §`[mail v0.6]`.
- **`docs/METHODOLOGY.md`** — las lecciones de PROCESO que cada versión refuerza o extiende (qué clase de
  defecto cazó cada capa de auditoría, y las sub-lecciones nuevas que cada fogueo del método produjo).
- **`CHANGELOG.md`** — la narrativa orgánica versión-por-versión: qué se construyó, por qué, y los hallazgos
  completos, en el orden en que ocurrieron.
- **Este archivo (`docs/ROADMAP.md`)** — el resumen ejecutable: qué está entregado (tabla), qué quedó
  diferido por versión (fast-follows), y qué backlog no ha empezado (candidatos). Si un ítem aquí se
  resuelve en una versión futura, se mueve de "candidatos"/"fast-follows" a "entregado" y se anota la
  versión que lo cerró — como se hizo arriba con el candidato (b) resuelto por v0.7.
