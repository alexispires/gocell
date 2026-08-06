// Package session owns the shared state of one gocell execution session (symbol registry,
// type registry, import tracker, plugin cache) and compiles/runs cells against it. Both the
// Jupyter kernel (pkg/jupyter) and the standalone REPL (cmd/gocell-repl) drive a Session the
// same way, so the compile-cache-execute pipeline lives in exactly one place.
package session

import (
	"errors"
	"fmt"

	"gocell/pkg/compiler"
	"gocell/pkg/output"
	"gocell/pkg/plugin"
	"gocell/pkg/runtime"
	"gocell/pkg/workspace"
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

// Execute parses, compiles (or reuses a cached build of) and runs one cell of Go code
// against the session's shared state.
func (s *Session) Execute(code string) (Result, error) {
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

	originalLine := best.OriginalLine + (panicErr.GeneratedLine - best.GeneratedLine)
	return fmt.Errorf("panic in cell, line %d: %v", originalLine, panicErr.Value)
}
