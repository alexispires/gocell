// Package session owns the shared state of one gocell execution session (symbol registry,
// type registry, import tracker, plugin cache) and compiles/runs cells against it. Both the
// Jupyter kernel (pkg/jupyter) and the standalone REPL (cmd/gocell-repl) drive a Session the
// same way, so the compile-cache-execute pipeline lives in exactly one place.
package session

import (
	"errors"
	"fmt"

	"github.com/alexispires/gocell/pkg/compiler"
	"github.com/alexispires/gocell/pkg/output"
	"github.com/alexispires/gocell/pkg/plugin"
	"github.com/alexispires/gocell/pkg/runtime"
	"github.com/alexispires/gocell/pkg/workspace"
)

// Session holds everything needed to compile and run a sequence of cells that share state.
type Session struct {
	reg           *runtime.Registry
	typeReg       *runtime.TypeRegistry
	ctx           *runtime.Context
	importTracker *compiler.ImportTracker
	builder       *compiler.Builder
	loader        *plugin.Loader
	cache         *plugin.Cache
	wsMgr         *workspace.Manager
}

// New creates a new Session backed by the given workspace.
func New(wsMgr *workspace.Manager) (*Session, error) {
	reg := runtime.NewRegistry()
	typeReg := runtime.NewTypeRegistry()

	builder, err := compiler.NewBuilder("")
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	return &Session{
		reg:           reg,
		typeReg:       typeReg,
		ctx:           runtime.NewContext(reg, typeReg),
		importTracker: compiler.NewImportTracker(),
		builder:       builder,
		loader:        plugin.NewLoader(),
		cache:         plugin.NewCache(),
		wsMgr:         wsMgr,
	}, nil
}

// Result is the outcome of executing one cell.
type Result struct {
	Stdout      string
	Stderr      string
	DisplayText string
	HasDisplay  bool
}

// Interrupt asks the currently running cell (if any) to stop at its next cooperative check --
// see pkg/compiler's injectInterruptChecks. Safe to call from a different goroutine than the
// one calling Execute.
func (s *Session) Interrupt() {
	s.ctx.Cancel()
}

// GoVersion returns the Go toolchain version this session's cells are actually compiled with.
func (s *Session) GoVersion() string {
	return s.builder.GoVersion()
}

// Execute parses, compiles (or reuses a cached build of) and runs one cell of Go code
// against the session's shared state.
func (s *Session) Execute(code string) (Result, error) {
	// A previous cell's interrupt must never bleed into this one.
	s.ctx.ResetCancel()

	cell, err := compiler.ParseCell(code)
	if err != nil {
		return Result{}, err
	}

	analysis, err := compiler.AnalyzeCell(cell, s.reg, s.importTracker, s.typeReg)
	if err != nil {
		return Result{}, err
	}
	pluginSource, lineMappings := compiler.GeneratePluginCode(cell, analysis, s.importTracker, s.typeReg)

	// The generated source fully captures everything the compiled binary depends on, so
	// its hash alone safely identifies a reusable build.
	hash := plugin.ComputeHash(pluginSource, s.builder.ModuleRoot())

	soPath, ok := s.cache.Get(hash)
	if !ok {
		cellDir, _, err := s.wsMgr.CreateCellDir()
		if err != nil {
			return Result{}, fmt.Errorf("failed to create cell directory: %w", err)
		}

		soPath, err = s.builder.BuildPlugin(cellDir, pluginSource)
		if err != nil {
			return Result{}, err
		}
		s.cache.Put(hash, soPath)
	}

	capturer := output.NewCapturer()
	_ = capturer.Start()
	execErr := s.loader.LoadAndExecute(soPath, s.ctx)
	stdoutStr, stderrStr, _ := capturer.Stop()
	execErr = remapPanicError(execErr, lineMappings)

	displayText, hasDisplay := s.ctx.TakeResult()

	res := Result{
		Stdout:      stdoutStr,
		Stderr:      stderrStr,
		DisplayText: displayText,
		HasDisplay:  hasDisplay,
	}
	return res, execErr
}

// remapPanicError rewrites a *plugin.PanicError's generated-file line number back into the
// cell's own line number, using the mapping GeneratePluginCode produced for this specific
// cell -- session is the only place that has both the mapping and the error. Any other error
// is returned unchanged, as is a PanicError whose line couldn't be located in the mapping
// (e.g. because it happened inside a function declared by an earlier cell, re-injected into
// this one, rather than this cell's own top-level statements).
func remapPanicError(err error, mappings []compiler.LineMapping) error {
	var panicErr *plugin.PanicError
	if !errors.As(err, &panicErr) || panicErr.GeneratedLine == 0 {
		return err
	}

	// An interrupt is reported as a panic (see pkg/compiler's injectInterruptChecks) so it can
	// reuse this same recover() machinery, but it isn't a bug at a line the way a real panic
	// is -- and for a loop with no other body content (a bare `for {}`), the panic call is
	// literally the only thing in the loop's body, landing on the injected check's own
	// synthetic line, which has no original-cell-line equivalent to remap to at all. Report it
	// cleanly instead of attempting a line number that would be meaningless at best.
	if panicVal, ok := panicErr.Value.(error); ok && errors.Is(panicVal, runtime.ErrInterrupted) {
		return runtime.ErrInterrupted
	}

	var best *compiler.LineMapping
	for i := range mappings {
		m := &mappings[i]
		if m.GeneratedLine <= panicErr.GeneratedLine && (best == nil || m.GeneratedLine > best.GeneratedLine) {
			best = m
		}
	}
	if best == nil {
		return err
	}

	// Each injected interrupt check (pkg/compiler's injectInterruptChecks) added exactly 3
	// generated lines that don't exist in the cell's own source, for every loop at or before
	// the panic within this statement -- correcting for that needs each injection's own
	// generated-space position (not a fixed-point correction on the original-line estimate,
	// which oscillates right at an injection's boundary): InjectedAtOriginalLines is sorted,
	// so the i-th injection sits 3*i generated lines further along than a naive delta from
	// OriginalLine would suggest, since i earlier injections already pushed it down.
	// Known imprecision, not fixed here: a panic literally on a for-loop's own header line
	// (its init/cond, e.g. an out-of-bounds index checked on the first iteration) is
	// indistinguishable from one on the first line of that same loop's body, and gets the
	// correction applied when it shouldn't -- narrow edge case, off by exactly 3 when it hits.
	injectedBefore := 0
	for i, origLine := range best.InjectedAtOriginalLines {
		injectionGeneratedLine := best.GeneratedLine + (origLine - best.OriginalLine) + 3*i
		if injectionGeneratedLine <= panicErr.GeneratedLine {
			injectedBefore = i + 1
		}
	}

	originalLine := best.OriginalLine + (panicErr.GeneratedLine - best.GeneratedLine) - 3*injectedBefore
	return fmt.Errorf("panic in cell, line %d: %v", originalLine, panicErr.Value)
}
