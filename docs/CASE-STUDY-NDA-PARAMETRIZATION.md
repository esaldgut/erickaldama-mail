# Case study — sacar el Account ID de un repo público sin romper el CD (NDA parametrization)

> Evidencia de ingeniería AI-native. Un frente real de `erickaldama-mail`: el repo es público y exponía el AWS
> Account ID y los ARNs como literales en el source Go, el workflow de CI y los docs. El reto: sacarlos del código
> versionado **al 100%** sin romper el CI/CD que ya despliega. La tesis del documento es cómo la auditoría del
> *plan* y un *spike* de una unidad cazaron, antes de desplegar, los defectos que habrían roto el CD.

## TL;DR

El Account ID (`<ACCOUNT_ID>` tras la redacción), 6 constantes ARN y 2 `role-to-assume` del CI vivían como
literales en un repo **público** — exposición NDA de superficie de reconocimiento. El fix (PR #22, CI verde
end-to-end) los saca del source: el account se resuelve en synth desde `CDK_DEFAULT_ACCOUNT` (del rol OIDC en CI,
`--profile` en local) y se inyecta vía un `Env` struct; los role ARNs pasan a GitHub secrets; los docs se redactan
a `<ACCOUNT_ID>`. Verificación: `grep -rn <account> .` = **0** en todo el repo, y el `cdk diff` del CI comenta
**vacío** — el template desplegado no cambia (byte-identidad). Costo: **$0/mes** (sin Secrets Manager).

## El problema — identificadores, NO credenciales

Un inventario exhaustivo (3 agentes) estableció el encuadre correcto, que decide toda la urgencia: **cero
credenciales en git** — ni access keys, secret keys, session tokens, API keys, DB URIs, bearer tokens. Cada string
tipo-secreto era una regex de redacción, un nombre de env var, o un placeholder de runbook. **Nada que rotar.** Lo
expuesto eran *identificadores* (Account ID, ARNs) — no otorgan acceso sin un principal autenticado; el OIDC trust
(`sub: repo:esaldgut/erickaldama-mail:environment:production`) es el boundary real, intacto. El tráfico de GitHub
(0 forks, 0 views, ~5 clones dispersos) confirmó que la exposición es higiene de reconocimiento hacia adelante, no
contención de brecha. **Distinguir identificador de credencial es la primera decisión: una credencial en git = rotar
YA; un identificador = parametrizar sin pánico.**

## La decisión — approach #3 (Env struct inyectado), no #4 (tokens CDK)

Se evaluaron 4 alternativas (la regla universal quedó destilada en el skill `iac-inject-env-never-hardcode-account`):

| # | Approach | Veredicto |
|---|---|---|
| 1 | `const→var` con `os.Getenv` en `init()` | ❌ PROHIBIDO — estado global capturado temprano, frágil |
| 2 | funciones (`IdentityArn()`) | aceptable, ruidoso |
| **3** | **`Env` struct construido 1× + inyectado (`EnvStackProps`)** | ✅ ELEGIDO |
| 4 | tokens CDK nativos (`Fn::Sub`/`AWS::AccountId`) | ❌ cambia el template → rompe byte-identidad + tests |

Se eligió **#3** por un criterio concreto: **byte-identidad del template**. `NewEnv()` compone cada ARN desde
`Env.Account` con `fmt.Sprintf`, produciendo strings idénticos char-por-char a los literales viejos — así el
template sintetizado NO cambia (`cdk diff` vacío), y la CD ya viva no corre riesgo. El approach #4 (tokens) cambiaría
el template (literal → `{"Ref":"AWS::AccountId"}`), rompiendo la byte-identidad y los template-assertion tests; y los
4 `cdk-hnb659fds-*` role ARNs llevan el account **dentro del nombre**, forzando `Fn::Sub` de todos modos. #3 preserva
el template y cierra dos deudas pre-existentes de paso (el `main.go` que re-hardcodeaba el account; los ARN
recompuestos inline).

## La auditoría del PLAN cazó 3 CRIT + 4 MAJOR — antes de una sola línea de código

El frente más distintivo de este caso: la pieza de auditoría que cazó el defecto más caro **no fue sobre el código
ni el texto — fue sobre el PLAN de implementación**. El plan pasó por una auditoría adversarial de 3 frentes
(byte-identidad de los `fmt.Sprintf` vs los literales reales · blast-radius de borrar las constantes · coherencia +
CI). Hallazgos, cada uno un defecto que habría roto la ejecución:

| Hallazgo | Sev | Qué habría pasado |
|---|---|---|
| 2 consumidores omitidos del plan (`NewAccountPrincipal`, 2× `ReceiptRuleArn`) | CRIT | compile break — el plan migraba unas referencias y dejaba otras |
| `CDK_DEFAULT_ACCOUNT` NO lo setea `configure-aws-credentials` (verbatim del README) | CRIT | el synth del CI usaría el placeholder → diff gigante / cuenta equivocada |
| threading no-uniforme (5 firmas de stack heterogéneas) | MAJOR | un 4º param no encajaba → se diseñó el `EnvStackProps` wrapper |
| baseline byte-identidad all-stacks nunca capturado | MAJOR | el gate diffearía contra un HEAD ya mutado |
| trampa de consistencia `NewEnv()` en tests | MAJOR | template y assert podían derivar de cuentas distintas |
| 5º test omitido del plan | MINOR | churn del wrapper sin cubrir ese call-site |

**El plan es código-por-escribir; se audita como código.** Un plan con un consumidor omitido garantiza un compile
break; un plan que apuesta a una variable de entorno implícita garantiza un diff gigante en CI. Ninguno de estos
vive en un diff todavía — solo la auditoría del plan los ve.

## El spike cazó el CRIT del CI en vivo — el go/no-go antes de propagar

El approach #3 asume que `fmt.Sprintf` produce strings byte-idénticos → template idéntico. En vez de propagarlo a
los 5 stacks por fe, el **spike** lo probó en 1 (`SendingStack`): capturar el template de HEAD → parametrizar solo
ese → `cdk synth` → `diff` contra el baseline. **El primer `diff` NO fue vacío** — sintetizó los ARN con una cuenta
distinta (`288761749126`/`us-west-2`, otra cuenta del ecosistema) en vez de la correcta. Causa: la CDK CLI popula
`CDK_DEFAULT_ACCOUNT` de las credenciales **activas** de la shell, y el default apuntaba a otra cuenta. Con
`cdk synth --profile AdministratorAccess-<account>` explícito, el diff fue vacío.

**El diff equivocado no fue un fallo — fue el spike cazando, en vivo, exactamente el CRIT que la auditoría del plan
había señalado** sobre `CDK_DEFAULT_ACCOUNT`. Dos reglas operativas nacieron del spike:
1. **`--profile` explícito es imperativo** en todo comando `cdk`/`aws` del frente — nunca confiar en el default de la
   shell (`aws-cli-pre-flight-canonical`).
2. **El CI necesita un step que exporte `CDK_DEFAULT_ACCOUNT` explícito** desde el rol OIDC asumido
   (`aws sts get-caller-identity`), porque `configure-aws-credentials` no lo hace. Sin el export, el synth del CI
   usaría el placeholder → diff gigante / deploy a la cuenta equivocada.

Sin el spike, ambos defectos habrían aparecido en el `cdk-diff` job del CI (o peor, en un deploy a la cuenta
equivocada). El spike convirtió un go/no-go de 1 stack en la prueba de todo el approach.

## Verificación — el gate de byte-identidad (y su trampa)

- `go build`/`vet`/`test -count=1 ./...` → verde (18 paquetes).
- **Byte-identidad de los 5 stacks:** el `diff` de los `*.template.json` contra el baseline (capturado antes de
  editar) = vacío, **salvo** el `S3Key`/`aws:asset:path` del bundle del Lambda. Nota de método importante: al
  verificar byte-identidad de un template CDK con un Lambda bundleado, el hash del asset SIEMPRE difiere entre
  synths (varía por el build Go) — hay que **excluir** esas líneas del diff; un `diff` que solo muestra `asset.*` es
  un PASS, no un fallo.
- **`grep -rn <account> .` = 0** en todo el repo — el account erradicado del source; solo vive en el runtime del env.
- **Gate end-to-end:** el `cdk-diff` job del PR corrió con el rol OIDC real (vía el secret) + el export de
  `CDK_DEFAULT_ACCOUNT` → comentó **diff vacío**, probando que el CD funciona con el account resuelto del entorno y
  que el template desplegado no cambia.
- **Nota `local-env-hides-ci-precondition`:** el `build & test` del CI tiene un gate `gofmt -l` que
  `go build`/`vet`/`test` local NO ejercitan — un `.go` creado con escritura directa quedó gofmt-desalineado, pasó
  local pero rompió el CI. Lección: correr el gate del CI (gofmt/lint) local antes del PR, no solo build+test.

## Lo que se interiorizó

- **Skill `iac-inject-env-never-hardcode-account` v1.1.0** — la regla universal, ahora con el caso entregado (#3
  piso, #4 techo, `const→var` prohibido; el corte es la byte-identidad; `--profile` imperativo; export del account
  en CI; método byte-identidad-menos-asset).
- **Skill `local-env-hides-ci-precondition` v1.1.0** — la variante "gate del CI que los comandos locales no
  ejercitan" (el gofmt).
- **9º fogueo en `docs/METHODOLOGY.md`** — la auditoría del plan + el spike como go/no-go.
- **Diagramas** (mermaid en `architecture.md` + mingrammer en `docs/diagrams/`) — el flujo de resolución del account.
- **Memorias** actualizadas a estado entregado.

## Por qué es evidencia de portafolio

El cambio es de "seguridad/NDA" — la clase de trabajo donde es fácil o sobre-reaccionar (rotar en pánico lo que no
es credencial) o romper algo vivo (cambiar de dónde sale el account y tumbar el CD). Este frente hizo ninguna de las
dos: inventarió primero (0 credenciales → sin urgencia de brecha), eligió el approach que preserva byte-identidad
(sin tocar el template desplegado), auditó el *plan* antes de escribir código (3 CRIT + 4 MAJOR cazados), y probó el
approach en 1 unidad antes de propagarlo (el spike cazó el CRIT del CI en vivo). La disciplina escala con las
consecuencias — sacar un identificador de un repo público tiene la consecuencia de poder tumbar un CD en producción,
y se trató con el rigor que eso merece.

Encaja en el ecosistema del repo (ver el README, "Ecosystem"): la lección generalizable —no los detalles del
cliente de correo— está destilada como un skill reutilizable, candidato a graduar hacia
[`ai-native-engineering-workspace`](https://github.com/esaldgut/ai-native-engineering-workspace).

## Referencias

- CHANGELOG: [`CHANGELOG.md`](../CHANGELOG.md) §`[infra · NDA]` — narrativa completa.
- Spec + plan de diseño: `docs/superpowers/specs/` y `docs/superpowers/plans/` (frente NDA).
- Metodología (9º fogueo): [`docs/METHODOLOGY.md`](METHODOLOGY.md).
- Arquitectura (diagrama de resolución del account): [`docs/architecture.md`](architecture.md).
- Skill universal: `iac-inject-env-never-hardcode-account`.
