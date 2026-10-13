# Change: pr-diff — comparativa `base...HEAD` antes de abrir la PR

## Why

El diff del rango que va a convertirse en pull request hoy solo lo lee el agente.
El humano queda afuera del bucle hasta que abre el navegador, y esa vista de
comparación es justamente la que le permite entender qué pasó.

Pedido del usuario, verbatim:

> quiero uan cosa siempre me apalanco en github para ver los cambios producidos en la pr y ver la comparativa develop con las branch que desarrrolla el agnete porque es la vista que me doi mas cuenta de que ha pasado puedo tenerlo en pi antes de hacer la pr?¿

`devctl` ya resuelve la parte difícil de esa comparación y hoy nadie la usa:
`gitx.ResolveBase` con su cadena de fallback (`internal/gitx/branches.go:98`),
el flag `--base` ya ligado como flag común (`internal/cli/common.go:44`), `merge-base`
ya usado para el veredicto de merge, y un envelope JSON versionado pensado para
consumidores (`internal/output/output.go:20`).

Lo que falta es la única pieza sin implementar: **el diff a nivel archivo**.
No existe hoy ningún parseo de `--numstat`, `--name-status`, `--raw` ni `diff`
en el repositorio. `internal/survey` calcula divergencia por rama
(`survey/survey.go:76-86`), y `worktree.ChangedPaths` es working tree contra HEAD
(`internal/worktree/worktree.go:152`), no un rango de commits.

Este change entrega la **capa de datos** de esa vista. La capa de presentación
en Pi es un change posterior.

## What changes

- Nuevo subcomando `devctl changes [--base REF] [--root PATH] [--patch] [--json]`.
- Nueva capa de datos en `internal/gitx/changes.go`: resolución de merge-base,
  listado de archivos con estado y conteos, y patch opcional.
- Tests directos de `internal/cli` (el gap declarado en `openspec/config.yaml`).
- `internal/app/app.go` (dispatch), `internal/app/help.go` (COMMANDS) y `README.md`.

## Non-goals

- **La extensión de Pi.** Vive fuera de este repositorio y es un change propio;
  este change la habilita, no la incluye.
- **Trabajo sin commitear.** El rango es el que GitHub compara en una PR:
  commits, no working tree. Un PR todavía no tiene cambios sin commitear:
  su skill `pr` commitea y pushea antes de crearlo. Si más adelante se quiere
  ver el estado sucio, es un flag nuevo, no un cambio de semántica del default.
- **Análisis semántico del diff.** No interpreta, no resume, no clasifica riesgo.
  Reporta el dato.

## Safety and contracts

Se preservan los contratos existentes:

- `changes` es **read-only**. No escribe, no requiere `--apply`, no crea manifiestos
  de recuperación, no toca el object store.
- Dry-run sigue siendo el default global y `--apply` sigue siendo el único camino
  mutante del binario.
- `UNKNOWN` sigue sin convertirse en borrado ni en una respuesta inventada.
- Ninguna integración concreta nueva: `internal/adapters` sigue siendo el único
  paquete con nombres de vendor. La lógica nueva no menciona ninguno, y
  `internal/app/agnosticism_test.go` lo verifica.

## Rollback

Bajo riesgo y reversible sin residuo: la rama se descarta o se revierte el
commit. No hay migración de datos, ni formato persistido, ni estado compartido.
El único efecto es un subcomando nuevo y la línea correspondiente en la ayuda;
al revertir, el binario queda exactamente como estaba.
