package plugin

import (
	"fmt"
	pluginGo "plugin"

	"gocell/pkg/runtime"
)

// ExecutorFunc is the signature of the Execute function generated in each cell plugin.
type ExecutorFunc func(ctx *runtime.Context) error

// Loader dynamically loads a .so file and extracts the Execute symbol.
type Loader struct{}

// NewLoader creates a new plugin loader.
func NewLoader() *Loader {
	return &Loader{}
}

// LoadAndExecute opens the .so file, guards its execution with recover(), and calls Execute(ctx).
func (l *Loader) LoadAndExecute(soPath string, ctx *runtime.Context) (err error) {
	// Catch any panic that occurs while a cell runs, to avoid crashing the Kernel
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in cell: %v", r)
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
