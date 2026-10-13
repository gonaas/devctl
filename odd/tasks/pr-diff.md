# pr-diff — capa de datos `devctl changes`

Feature: dar al humano la comparativa `base...HEAD` que hoy solo ve en el navegador,
antes de abrir la PR. Change size: large (falla el resume test — varios entregables,
dos repos, decisiones de diseño abiertas al inicio).

Artefactos normativos en `openspec/changes/pr-diff/` (proposal, specs con Given/When/Then
y RFC 2119, design). Este documento es el ledger de ejecución: pedido verbatim, specs
cortas, tareas y bitácora. Los IDs `S#` y `T#` son los mismos en ambos documentos.

## Specs

- **S1 — Base.** *"la comparativa develop con las branch que desarrrolla el agnete"*.
  La base se resuelve con `gitx.ResolveBase` (override explícito → remote HEAD →
  nombres conocidos: `develop`, `main`, `master`, `trunk`), con `--base REF` como
  override. Base irresoluble ⇒ error con exit distinto de cero, nunca un listado vacío.
- **S2 — Rango.** *"los cambios producidos en la pr"*. El rango es
  `merge-base(base, HEAD)..HEAD` y el commit resultante se reporta como `merge_base`.
  No es `base..HEAD`.
- **S3 — Listado por archivo.** Estado (added/modified/deleted/renamed/copied/
  type-changed) y conteos `+/-` por archivo. Rename = **una** entrada con path viejo y
  nuevo, no borrado+agregado. Binario ⇒ conteos no disponibles, nunca `0`.
  Paths con espacios o no-ASCII se reportan byte a byte.
- **S4 — Solo lectura.** *"antes de hacer la pr"*. Sin `--apply`, sin manifiestos,
  sin escribir objetos.
- **S5 — Vista.** *"es la vista que me doi mas cuenta de que ha pasado"*. Tabla de
  texto para leer, `--json` versionado (`devctl/changes/1`) para consumir.
- **S6 — Patch.** `--patch` incluye el diff unificado del rango para que un consumidor
  renderice hunks sin reimplementar plumbing de git.
- **S7 — Tests directos de `internal/cli`** (regla del repo en `openspec/config.yaml`).
- **S8 — Agnosticismo de vendor** preservado (`internal/adapters` sigue siendo el único
  paquete con integraciones concretas).
- **Fuera de alcance:** la extensión TUI de Pi (change posterior) y los cambios sin
  commitear (ver `openspec/changes/pr-diff/tasks.md`, 5.1).

## Tasks

| ID | S# | Ruta | Commit |
|---|---|---|---|
| T1.1 | S2 | inline — `gitx.MergeBase` | `pending` |
| T1.2 | S3 | inline — `gitx.ChangedFiles` | `pending` |
| T1.3 | S6 | inline — `gitx.Patch` | `pending` |
| T1.4 | S2,S3 | inline — fixture builder | `pending` |
| T2.1 | S1,S2,S4 | inline — `cli.RunChanges` | `pending` |
| T2.2 | S5 | inline — render texto | `pending` |
| T2.3 | S5 | inline — `--json` | `pending` |
| T2.4 | S6 | inline — `--patch` | `pending` |
| T3.1 | — | inline — dispatch | `pending` |
| T3.2 | — | inline — ayuda | `pending` |
| T3.3 | — | inline — README | `pending` |
| T4.1 | S7 | inline — tests directos de `internal/cli` | `pending` |
| T4.2 | — | inline — `gofmt` / `vet` / `go test ./...` | `pending` |
| T4.3 | S1,S5 | manual — end-to-end sobre un repo real | `pending` |

Rutas: todo inline. Es una unidad coherente (capa de datos + superficie de comando)
con archivos acoplados entre sí; la regla del Writer no aplica y dividirla artificialmente
agregaría coordinación sin reducir riesgo.

## Log

- **L1** (pedido original, verbatim): "quiero uan cosa siempre me apalanco en github
  para ver los cambios producidos en la pr y ver la comparativa develop con las branch
  que desarrrolla el agnete porque es la vista que me doi mas cuenta de que ha pasado
  puedo tenerlo en pi antes de hacer la pr?¿"
- **L2**: el usuario eligió `devctl changes + extensión Pi fina` sobre la extensión
  autocontenida y sobre el mínimo sin TUI. Consecuencia: la capa de datos se implementa
  en `devctl` reutilizando `ResolveBase`, y la TUI queda para un change posterior.
- **L3**: `devctl` no tenía proceso ODD documentado pero sí `openspec/config.yaml`
  (`schema: spec-driven`, `strict_tdd: true`, `review_budget_lines: 800`,
  `delivery_strategy: ask-on-risk`) con reglas explícitas de proposal/specs/design/tasks.
  Decisión: cumplir las reglas del repo en `openspec/changes/pr-diff/` **y** mantener este
  ledger, compartiendo los IDs `S#`/`T#` para que la reconciliación sea mecánica.
- **L4**: rama `feat/devctl-changes-command` creada desde `main` antes de la primera
  escritura (devctl estaba en la rama por defecto).
