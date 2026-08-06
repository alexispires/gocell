package plugin

import (
	"fmt"
	"path/filepath"
	pluginGo "plugin"
	goruntime "runtime"

	"github.com/alexispires/gocell/pkg/runtime"
)

// ExecutorFunc is the signature of the Execute function generated in each cell plugin.
type ExecutorFunc func(ctx *runtime.Context) error

// PanicError describes a panic recovered from a cell's Execute. GeneratedLine, when non-zero,
// is the line in the compiled plugin's own generated main.go where the panic actually
// occurred (the innermost stack frame whose file is that plugin's main.go) -- session-level
// code, which is the only place that knows how GeneratePluginCode mapped this specific cell's
// lines to that file, is responsible for translating it back to the cell's own line number.
type PanicError struct {
	Value         any
	GeneratedFile string
	GeneratedLine int
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("panic in cell: %v", e.Value)
}

// Loader dynamically loads a .so file and extracts the Execute symbol.
type Loader struct{}

// NewLoader creates a new plugin loader.
func NewLoader() *Loader {
	return &Loader{}
}

// LoadAndExecute opens the .so file, guards its execution with recover(), and calls Execute(ctx).
func (l *Loader) LoadAndExecute(soPath string, ctx *runtime.Context) (err error) {
	mainGoPath := filepath.Join(filepath.Dir(soPath), "main.go")

	// Catch any panic that occurs while a cell runs, to avoid crashing the Kernel
	defer func() {
		if r := recover(); r != nil {
			genFile, genLine := findPanicSite(mainGoPath)
			err = &PanicError{Value: r, GeneratedFile: genFile, GeneratedLine: genLine}
		}
	}()

	p, err := pluginGo.Open(soPath)
	if err != nil {
		return fmt.Errorf("failed to open plugin %s: %w", soPath, err)
	}

	sym, err := p.Lookup("Execute")
	if err != nil {
		return fmt.Errorf("Execute symbol not found in plugin %s: %w", soPath, err)
	}

	execFunc, ok := sym.(func(*runtime.Context) error)
	if !ok {
		if execFuncNoErr, okNoErr := sym.(func(*runtime.Context)); okNoErr {
			execFuncNoErr(ctx)
			return nil
		}
		return fmt.Errorf("invalid signature for the Execute symbol in plugin %s", soPath)
	}

	return execFunc(ctx)
}

// findPanicSite walks the current goroutine's stack (called from within the recovering
// deferred function, where the panicking frames are still present) looking for the innermost
// frame whose file is the cell's own generated main.go -- that is, the deepest point in the
// cell's own compiled code the panic passed through, as opposed to further-inner stdlib or
// runtime frames that caused it (e.g. inside a slice bounds check) but aren't the cell's own
// source. Returns ("", 0) if no such frame is found (e.g. the panic originated in an
// unrelated goroutine's stack, or the plugin's file layout ever changes).
func findPanicSite(mainGoPath string) (file string, line int) {
	pcs := make([]uintptr, 64)
	n := goruntime.Callers(3, pcs) // skip findPanicSite, the deferred func, and runtime.gopanic
	frames := goruntime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if frame.File == mainGoPath {
			return frame.File, frame.Line
		}
		if !more {
			break
		}
	}
	return "", 0
}
