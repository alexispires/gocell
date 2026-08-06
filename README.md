# gocell

<img src="logo.png" alt="gocell logo" width="220">

[![Go Reference](https://pkg.go.dev/badge/github.com/alexispires/gocell.svg)](https://pkg.go.dev/github.com/alexispires/gocell)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![CI](https://github.com/alexispires/gocell/actions/workflows/ci.yml/badge.svg)](https://github.com/alexispires/gocell/actions/workflows/ci.yml)
[![Lint](https://github.com/alexispires/gocell/actions/workflows/lint.yml/badge.svg)](https://github.com/alexispires/gocell/actions/workflows/lint.yml)
[![codecov](https://codecov.io/gh/alexispires/gocell/branch/main/graph/badge.svg)](https://codecov.io/gh/alexispires/gocell)
[![Binder](https://mybinder.org/badge_logo.svg)](https://mybinder.org/v2/gh/alexispires/gocell/HEAD?labpath=examples%2Fheavy-model.ipynb)

A Jupyter kernel for Go that runs every cell as a real compiled Go plugin
(`-buildmode=plugin`), loaded into the kernel's own process. Session state — variables,
goroutines, open connections — persists in memory across cells: no interpreter, no
re-execution, cells share the same Go heap.

## Architecture

Every cell goes through this pipeline, owned by [`session.Session`](pkg/session/session.go):

```
cell code
    │
    ▼
ParseCell        (pkg/compiler/parser.go)      splits imports / types / functions / statements
    │
    ▼
AnalyzeCell       (pkg/compiler/analyzer.go)    resolves which existing variables are used
    │                                           or new, via go/types
    ▼
GeneratePluginCode (pkg/compiler/generator.go)  generates the plugin's Go source
    │
    ▼
BuildPlugin       (pkg/compiler/builder.go)     `go build -buildmode=plugin` (or cached)
    │
    ▼
LoadAndExecute    (pkg/plugin/loader.go)        plugin.Open + Execute(ctx), recover()-guarded
```

`pkg/session` doesn't depend on Jupyter or ZMQ — it drives both the Jupyter kernel
([pkg/jupyter](pkg/jupyter)) and the standalone REPL ([cmd/gocell-repl](cmd/gocell-repl)).

Shared state lives in a [`runtime.Registry`](pkg/runtime/registry.go): each top-level
variable is an `unsafe.Pointer` to its own memory. A later cell that references it doesn't
hydrate a copy — `AnalyzeCell` rewrites `x` to `(*x_ptr)` throughout, so reads and writes go
straight through the shared pointer.

Types and functions declared in a cell are kept as source in a
[`runtime.TypeRegistry`](pkg/runtime/types.go) and re-injected into every later plugin.

## Notable features

- **Idempotent re-execution** — `:=` becomes `=` when every left-hand name already exists,
  so re-running a cell doesn't hit Go's "no new variables" error.
- **Auto-display** — a bare last expression is captured and shown as the cell's result
  (Jupyter's `Out[n]`), instead of failing to compile.
- **Plugin cache** — keyed by the generated code's hash; re-running a no-op cell doesn't
  recompile.
- **Panic resilience** — a panic in a cell (including nil dereferences) is caught and
  reported as a normal error; the session keeps going.

## Installation

```sh
go build -o gocell-kernel ./cmd/gocell-kernel
go build -o gocell-install ./cmd/gocell-install
./gocell-install
```

`gocell-install` points Jupyter at this repository via `GOCELL_MODULE_ROOT`, so the kernel
can find the `gocell` module regardless of how Jupyter launches it.

For a standalone REPL:

```sh
go build -o gocell-repl ./cmd/gocell-repl
./gocell-repl
```

A cell runs as soon as its braces balance, so multi-line `func`/`type`/`if`/`for` blocks
type naturally.

## Examples

- [examples/heavy-model.ipynb](examples/heavy-model.ipynb) — fits a model once, reuses it
  across independent cells: same memory address throughout.
- [examples/live-goroutines.ipynb](examples/live-goroutines.ipynb) — a background worker
  goroutine started in one cell, fed jobs from later ones.

## Known limitations

- **Memory never comes back down.** Go's `plugin` package can't unload a `.so` — roughly
  1-1.4 MB of RSS per *distinct* cell, measured directly (the plugin cache makes
  re-running the same cell nearly free). Only restarting the kernel reclaims it.
- **No Windows support**, and the kernel and every cell plugin must share a Go toolchain
  version (handled automatically, see [pkg/compiler/builder.go](pkg/compiler/builder.go)).

## License

[BSD 3-Clause](LICENSE)

## Credits

- Designed and driven by [Alexis Pires](https://github.com/alexispires), with
  [Claude](https://claude.com/claude-code) (Sonnet 5), Anthropic's AI coding assistant, as a
  pair-programmer on implementation.
- Inspired by two existing Go Jupyter kernels: [gonb](https://github.com/janpfeifer/gonb) and
  [gophernotes](https://github.com/gopherdata/gophernotes) — studying their design and test
  suites directly shaped several of gocell's own decisions and tests.
- The Go gopher was designed by Renée French and is licensed under the Creative Commons 3.0
  Attributions license.
