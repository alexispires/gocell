# gosk

A Jupyter kernel for Go that runs every cell as a real compiled Go plugin
(`-buildmode=plugin`), dynamically loaded into the kernel's process. A session's state
(variables, channels, background goroutines, open connections...) genuinely persists in
memory from one cell to the next: there is no interpreter, and no re-execution of an
accumulated program — successive cells share the same Go heap, via raw pointers.

## Architecture

Every cell execution goes through the following pipeline, owned by
[`session.Session`](pkg/session/session.go):

```
cell code
    │
    ▼
ParseCell        (pkg/compiler/parser.go)      splits imports / types / functions / statements
    │
    ▼
AnalyzeCell       (pkg/compiler/analyzer.go)    detects which existing variables are used or
    │                                           mutated, and which ones are new
    ▼
GeneratePluginCode (pkg/compiler/generator.go)  generates a complete Go file: hydrates
    │                                           existing variables from the Registry, runs
    │                                           the cell's code, writes the state back
    ▼
BuildPlugin       (pkg/compiler/builder.go)     `go build -buildmode=plugin` into a .so
    │                                           (or reused from `pkg/plugin.Cache`)
    ▼
LoadAndExecute    (pkg/plugin/loader.go)        plugin.Open + call Execute(ctx), with
                                                 recover() so the kernel never crashes
```

`pkg/session` has no dependency on Jupyter or ZMQ, so it's driven identically by two
front-ends: the Jupyter kernel ([pkg/jupyter](pkg/jupyter)) and the standalone REPL
([cmd/gosk-repl](cmd/gosk-repl)).

The shared state lives in a [`runtime.Registry`](pkg/runtime/registry.go): every top-level
variable from a cell is stored as an `unsafe.Pointer` plus a `KeepAlive` reference (to
prevent the GC from freeing it). Subsequent cells that reference that variable re-declare a
local variable of the same Go type and hydrate it from that pointer at the start of their
`Execute()`, then write the modified value back at the end.

Types and functions declared in a cell are kept as source text in a
[`runtime.TypeRegistry`](pkg/runtime/types.go) and re-injected in full into every plugin
compiled afterward, which allows using (and redefining) them in subsequent cells.

## Notable features

- **Idempotent re-execution**: `AnalyzeCell` automatically turns `:=` into `=` when all
  left-hand-side variables already exist, so a cell can be re-run without hitting Go's "no
  new variables on left side of :=" error.
- **Auto-display of the last expression**: if a cell's last line is a bare expression that
  would not be a valid standalone Go statement (bare identifier, compound expression...), it
  is captured and published as an `execute_result` (the equivalent of Jupyter's `Out[n]`)
  instead of failing to compile.
- **Plugin cache**: the hash of the generated Go code (which fully captures the state it
  depends on) is used as a cache key; re-running a cell with no effect on session state does
  not trigger a recompile.
- **Resilience to panics**: a panic inside a cell (including a nil pointer dereference) is
  caught and reported as a normal execution error; the session keeps going.

## Installation

```sh
go build -o gosk-kernel ./cmd/gosk-kernel
go build -o gosk-install ./cmd/gosk-install
./gosk-install
```

`gosk-install` writes a `kernel.json` into Jupyter's kernels directory, with the
`GOSK_MODULE_ROOT` environment variable pointing at this repository — that's what lets the
kernel find the `gosk` module (and its Go version) no matter how Jupyter launches it.

For a REPL with no Jupyter involved at all:

```sh
go build -o gosk-repl ./cmd/gosk-repl
./gosk-repl
```

Cells are submitted as soon as braces balance out, so multi-line `func`/`type`/`if`/`for`
blocks can be typed naturally; a single-line cell runs as soon as you press Enter.

## Examples

- [examples/heavy-model.ipynb](examples/heavy-model.ipynb) — fits a small model once across
  several cells and proves it's the same live object throughout: identical memory address,
  and a mutation made in one cell is visible from a completely independent, later cell.
- [examples/live-goroutines.ipynb](examples/live-goroutines.ipynb) — starts a background
  worker goroutine in one cell and feeds it jobs from later, independent cells; the worker
  keeps running between executions because it's the same OS process the whole time.

## Known limitations

- **Memory never comes back down during a session.** The standard library's `plugin`
  package provides no way to unload a `.so` once it's loaded. A session with many distinct
  cells accumulates plugins loaded for the life of the process; only restarting the kernel
  frees that memory.
- **Go's `plugin` package does not work on Windows**, and requires the kernel and every
  cell plugin to be built with a strictly compatible Go toolchain (hence the automatic
  syncing of the cell go.mod's `go` directive with the host module's, see
  [pkg/compiler/builder.go](pkg/compiler/builder.go)).
- **`AnalyzeCell` identifies used symbols by name, syntactically** (an AST walk), not
  through real type resolution (`go/types`). It explicitly excludes composite literal field
  names and method/field selectors to avoid the most common collisions with a same-named
  global variable, but this is not as strong a guarantee as real type checking.
