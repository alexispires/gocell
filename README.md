# gocell

<img src="logo.png" alt="gocell logo" width="220">

[![Go Reference](https://pkg.go.dev/badge/github.com/alexispires/gocell.svg)](https://pkg.go.dev/github.com/alexispires/gocell)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![CI](https://github.com/alexispires/gocell/actions/workflows/ci.yml/badge.svg)](https://github.com/alexispires/gocell/actions/workflows/ci.yml)
[![Lint](https://github.com/alexispires/gocell/actions/workflows/lint.yml/badge.svg)](https://github.com/alexispires/gocell/actions/workflows/lint.yml)
[![codecov](https://codecov.io/gh/alexispires/gocell/branch/main/graph/badge.svg)](https://codecov.io/gh/alexispires/gocell)
[![Binder](https://mybinder.org/badge_logo.svg)](https://mybinder.org/v2/gh/alexispires/gocell/HEAD?labpath=examples%2Fheavy-model.ipynb)

Run Go interactively — as a Jupyter kernel or a standalone REPL — by compiling every cell as
a real Go plugin (`-buildmode=plugin`) loaded into one long-lived process. Session state —
variables, goroutines, open connections — persists in memory across cells: no interpreter, no
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

Everything below works identically in Jupyter and in `gocell-repl` — both are driven by the
same [`session.Session`](pkg/session/session.go), which has no notion of which interface
called it. The linked examples are notebooks only because that's what's written down so far.

- **Goroutines that outlive a cell** — a `go func() { ... }()` started in one cell keeps
  running in the background, fed from and read from independent later cells
  ([examples/live-goroutines.ipynb](examples/live-goroutines.ipynb)).
- **State persists with zero effort** — no serialization, no `%store`-style magic: a
  variable's memory address is identical from cell to cell, because cells share the kernel
  process's own heap ([examples/heavy-model.ipynb](examples/heavy-model.ipynb)).
- **Real generics** — type parameters work both within a cell and across cells, the same as
  any other type or function declaration
  ([examples/generics.ipynb](examples/generics.ipynb)).
- **Real compiled speed** — every cell compiles to native code via `-buildmode=plugin`,
  instead of being evaluated by an interpreter. Measured on an Apple M2, back to back, running
  [examples/heavy-model.ipynb](examples/heavy-model.ipynb)'s exact workload (8M samples, 140
  epochs of gradient descent) unmodified against
  [gophernotes](https://github.com/gopherdata/gophernotes) (which evaluates cell code through
  the [gomacro](https://github.com/cosmos72/gomacro) interpreter rather than compiling it):
  gocell finished in **1.46s**, gophernotes in **80.5s** — about **55x** faster, both runs
  converging to the same result. Run the same way against
  [gonb](https://github.com/janpfeifer/gonb) (**1.44s**) the two are essentially tied — gonb
  also compiles real Go, so raw CPU-bound speed isn't the difference; the difference is that
  gonb recompiles and reruns its whole accumulated program every cell, rather than keeping
  live state and goroutines in one process.
- **Auto-import** — a cell can use `math.Sqrt(...)` with no `import "math"` line at all and it
  just compiles: every cell is run through real `goimports` before building, not just gofmt.
- **Go 1.25+** — builds and runs against current Go toolchains.

A couple of smaller, still-genuine conveniences:

- **Idempotent re-execution** — `:=` becomes `=` when every left-hand name already exists, so
  re-running a cell doesn't hit Go's "no new variables" error; combined with the plugin cache
  (keyed by the generated code's hash), re-running an unchanged cell is nearly free.
- **Auto-display** — a bare last expression is captured and shown as the cell's result
  (like Jupyter's `Out[n]`; the REPL just prints it), instead of failing to compile.

## Installation

### Jupyter kernel

```sh
go build -o gocell-kernel ./cmd/gocell-kernel
go build -o gocell-install ./cmd/gocell-install
./gocell-install
```

`gocell-install` points Jupyter at this repository via `GOCELL_MODULE_ROOT`, so the kernel
can find the `gocell` module regardless of how Jupyter launches it.

### Standalone REPL

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
- [examples/generics.ipynb](examples/generics.ipynb) — a generic type declared in one cell,
  its methods added in another, instantiated and used in a third.

## Known limitations

- **Memory never comes back down.** Go's `plugin` package can't unload a `.so` — roughly
  1-1.4 MB of RSS per *distinct* cell, measured directly (the plugin cache makes
  re-running the same cell nearly free). Only restarting the kernel reclaims it — this is
  structural to `-buildmode=plugin`, not something build flags can reduce.
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
