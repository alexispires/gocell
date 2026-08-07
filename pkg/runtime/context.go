package runtime

import (
	"errors"
	"sync/atomic"
	"unsafe"
)

// ErrInterrupted is the error Err returns once Cancel has been called -- and the value every
// injected interrupt check panics with (see pkg/compiler's injectInterruptChecks), since Go
// has no way to forcibly stop a running goroutine: a loop can only be asked to notice and stop
// itself.
var ErrInterrupted = errors.New("interrupted")

// Context is the object passed to the Execute(ctx) function of every plugin.
type Context struct {
	Registry *Registry
	Types    *TypeRegistry

	resultText string
	hasResult  bool

	cancelled atomic.Bool
}

// NewContext initializes an execution Context.
func NewContext(reg *Registry, tr *TypeRegistry) *Context {
	return &Context{
		Registry: reg,
		Types:    tr,
	}
}

// Cancel flags the cell currently running against this Context as interrupted. Safe to call
// from a different goroutine than the one executing the cell (e.g. the ZMQ Control loop
// handling an interrupt_request while the Shell loop is blocked inside Execute).
func (c *Context) Cancel() {
	if c == nil {
		return
	}
	c.cancelled.Store(true)
}

// Err returns ErrInterrupted if Cancel has been called since the last ResetCancel, nil
// otherwise. Every injected loop check calls this.
func (c *Context) Err() error {
	if c == nil {
		return nil
	}
	if c.cancelled.Load() {
		return ErrInterrupted
	}
	return nil
}

// ResetCancel clears a previous interrupt before a new cell starts, so a resolved interrupt
// from one cell can never bleed into the next.
func (c *Context) ResetCancel() {
	if c == nil {
		return
	}
	c.cancelled.Store(false)
}

// SetResult records the textual representation of a cell's last expression,
// displayed by the kernel as an execute_result (equivalent to Jupyter's "Out[n]").
func (c *Context) SetResult(s string) {
	if c == nil {
		return
	}
	c.resultText = s
	c.hasResult = true
}

// TakeResult returns the current cell's displayed result and resets the state
// for the next execution. The kernel calls this method once per cell.
func (c *Context) TakeResult() (string, bool) {
	if c == nil {
		return "", false
	}
	text, ok := c.resultText, c.hasResult
	c.resultText, c.hasResult = "", false
	return text, ok
}

// GetPointer retrieves the raw memory address of a symbol by its name.
func (c *Context) GetPointer(name string) unsafe.Pointer {
	if c == nil || c.Registry == nil {
		return nil
	}
	return c.Registry.GetPointer(name)
}

// SetPointer records the raw memory address, type name, and a GC KeepAlive reference.
func (c *Context) SetPointer(name string, typeName string, ptr unsafe.Pointer, keepAlive any) {
	if c != nil && c.Registry != nil {
		c.Registry.SetPointer(name, typeName, ptr, keepAlive)
	}
}
