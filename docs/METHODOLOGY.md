# Metodología — cómo se construyó este sistema

Este documento explica **cómo** se construyó el sistema de correo `erickaldama.com`, no solo qué. Cada subproyecto
(SP-0 … SP-4, y el CD) siguió el mismo flujo de fases gated. Es la parte del repo que demuestra el *proceso* de
ingeniería — la metodología es tan parte del entregable como el código.

> El sistema completo está vivo y verificado end-to-end: un correo enviado por el cliente Go recorre
> SES → S3 → Lambda → DynamoDB y se lee de vuelta. Ver [CHANGELOG.md](../CHANGELOG.md) y los runbooks
> `docs/SP-*-DEPLOY.md`.

---

## El flujo de un subproyecto (6 fases, cada una un gate)

```
F0 Investigación → F1 Brainstorm → F2 Spec+Audit → F3 Plan+Audit → F4 Build+6ejes → F5 Review+Deploy+Smoke → F6 Evidencia+PR
   (doc oficial      (worktree       (auditoría      (plan-audit:    (subagente/      (whole-branch       (runbook +
    viva, citar       aislado,        adversarial     gaps, bugs,     tarea +          + gate humano       CHANGELOG +
    verbatim,         decisiones      que COMPILA     base)          auditoría        + smoke REAL →      IAM JSON +
    NO VERIFICADO)    cerradas)       vs libs vivas)                 6-ejes/tarea)    deploy findings)    memoria + PR)
```

### F0 — Investigación antes de implementar
Cuando hay un patrón nuevo (primera cuenta SES, primer OIDC) o una API que no se domina: agentes investigan contra
**documentación oficial viva**, citan verbatim con URL, y marcan **NO VERIFICADO** lo que la doc no cubre. Los
hechos + los gaps se consolidan en un documento durable. Esto ancla el diseño en realidad, no en supuestos.

### F1 — Worktree aislado + brainstorm
Cada subproyecto vive en un worktree git aislado (protege la rama base). Brainstorm guiado: las decisiones de
alcance se cierran una a una, con la investigación como ancla. Hard gate: no se implementa nada sin diseño aprobado.

### F2 — Spec + auditoría adversarial (compilando)
El spec formal pasa por una **auditoría adversarial**: 3-4 agentes que verifican contra doc oficial Y **compilan el
código propuesto contra las librerías en sus versiones reales**. El objetivo es cazar las trampas que rompen el
deploy ANTES de tocar AWS — firmas de API inventadas, una API no soportada en el contexto asumido, un recurso que
el IAM no cubre.

### F3 — Plan ejecutable + su auditoría
El plan (tareas bite-sized, TDD, código completo) se audita TAMBIÉN — el plan-audit caza lo que el spec-audit no
ve: promesas del spec sin tarea, bugs en el código del plan, bloqueantes de base.

### F4 — Subagent-driven-development con auditoría 6-ejes por tarea
Subagente fresco por tarea. Por cada una: implementer (TDD) → **el reporte se valida contra el código real**
(`go test -count=1` + `vet` + `build` + `go mod verify` — los reportes mienten) → spec + quality review →
**auditoría de 6 ejes** (robustez · eficiencia · patrones de diseño · mantenibilidad · seguridad · deuda técnica,
cada hallazgo con evidencia `archivo:línea`) como gate → aplicar hallazgos → marcar completa. Cualquier eje puede
bloquear el merge.

### F5 — Review final whole-branch + deploy + smoke real
La rama completa se revisa junta (para lo que solo emerge cross-package). Las mutaciones a AWS las ejecuta el
humano out-of-band (SSO Admin); el agente verifica post-deploy con menor privilegio empírico. **El smoke
end-to-end real** contra el sistema desplegado es donde aparecen los **deploy findings** — los hallazgos que ningún
code review puede cazar porque viven en el boundary/la cuenta/el config real.

### F6 — Evidencia en repo + PR + review final consolidado
Runbook con los deploy findings (causa raíz + fix), CHANGELOG orgánico, los JSON de IAM auditables en `iam/`,
memoria de patrones canónicos, y el PR a develop con CI verde (Git Flow). Los artefactos revisables van al PR como
comentarios.

Tras abrir el PR, una **pasada de auditoría 6-ejes final sobre el PR ya en GitHub, con un revisor independiente**
(agente fresco, modelo más capaz, que NO construyó el código). Es distinta del review de F5: F5 mira la rama en el
worktree *durante* la construcción y caza lo cross-package; este mira el `gh pr diff` *terminado* como lo vería un
revisor externo, y elimina el **sesgo de constructor** — la última red antes del merge la pone alguien sin el contexto
de haberlo escrito. Las cinco capas de auditoría del flujo (spec-audit, plan-audit, 6-ejes por tarea, whole-branch F5,
y este review final sobre el PR) no son redundantes: cada una caza una clase distinta de defecto — una API inventada,
un bug en el código del plan, un defecto por tarea, una incoherencia cross-package, y el sesgo de quien lo construyó.

---

## Por qué los "deploy findings" son la parte más valiosa

Un diagrama de arquitectura bonito puede describir algo que nunca tocó AWS. Lo que prueba que un sistema se desplegó
**de verdad** son los hallazgos que solo aparecen al desplegar — porque viven en capas que el código no ve:

| Subproyecto | Deploy finding real | Lección |
|---|---|---|
| SP-1 | El permissions boundary INTERSECTA la exec-policy (no une); `ssm:GetParameters` debía estar en ambas | El boundary es una 2ª compuerta, no un duplicado |
| SP-2 | El boundary también necesita `events:*`, no solo la exec-policy | Al añadir un servicio nuevo, tocar AMBAS capas |
| SP-3 | El read-only del agente no podía leer logs Lambda ni el estado de la suscripción SNS | La observabilidad es parte del límite, no un extra |
| SP-4 | El boundary DENIEGA `iam:CreateUser` (anti-escalación); la policy de envío necesitaba el config-set ARN; una access key impresa en chat → rotación segura | El primer recurso de un tipo nuevo topa con denies deliberados; SES aplica el default config-set a todo send |

Cada uno se documentó con su causa raíz y su fix en el runbook correspondiente. Eso es ingeniería real, no una foto.

---

## Disciplinas transversales

- **Validar, no confiar** — cada reporte de subagente se verifica contra el código/sistema real.
- **Gate humano out-of-band** — el agente nunca ejecuta writes a sistemas reales; entrega los comandos exactos.
- **Menor privilegio verificado empíricamente** — no se asume; se prueba (`mail-client-read` puede leer pero NO
  enviar → AccessDenied real).
- **Secrets nunca en chat/binario/git/logs** — si una credencial toca un canal observable, se revoca y rota
  (patrón `create-access-key` capturado a variable + `aws configure set`, la secret nunca se imprime).
- **NDA / repo público** — gate sobre todo el output antes de exponer.

---

## La metodología es reutilizable

Este flujo está codificado como skills de agente reutilizables (`subproject-delivery-canonical` orquesta las fases;
`pr-as-auditable-evidence` y `engineering-audit-6-axes` son piezas). Este documento es la vitrina pública de esa
metodología — la fuente vive en la configuración del agente, esta página la hace legible para quien lee el repo.
