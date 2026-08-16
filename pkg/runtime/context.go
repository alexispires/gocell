package runtime

import (
	"context"
	"errors"
	"sync"
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

	// mu guards the display state below. The cell's own statements run on one goroutine, but a
	// goroutine started by an earlier cell and still running -- gocell's whole point, see
	// examples/live-goroutines.ipynb -- can call Display at the moment the kernel is draining, so
	// this is a real race and not a theoretical one.
	mu        sync.Mutex
	result    Output
	hasResult bool
	displays  []Output

	// publish sends an Output straight to the frontend instead of queueing it. Installed by the
	// Jupyter layer, so pkg/runtime still knows nothing about ZMQ. Without it -- gocell-repl,
	// tests -- Display falls back to the queue that Execute drains at the end of the cell.
	publish func(o Output, displayID string, update bool)

	// inputFn asks the frontend for a line. Installed by the Jupyter layer so pkg/runtime never
	// learns about ZMQ; nil under gocell-repl and in tests, where there is no one to ask.
	inputFn func(prompt string, password bool) (string, error)

	stdCtx   context.Context
	cancelFn context.CancelFunc
}

// current is the Context that display.Show writes to, mirroring CPython's get_ipython() singleton.
// This works for the same reason that one does: exactly one session exists per kernel process, and
// -buildmode=plugin does not duplicate packages shared between host and plugin, so a plugin calling
// into this package reaches the kernel's own state.
var current *Context

// Current returns the Context of the running session, or nil if none has been created. pkg/display
// is a separate package and has no other way to reach it.
func Current() *Context { return current }

// NewContext initializes an execution Context and binds it as the current one.
//
// The binding lives here rather than behind an exported setter for two reasons: session.New already
// calls this exactly once, so it cannot be forgotten, and nothing new is exported for cell code to
// misuse.
func NewContext(reg *Registry, tr *TypeRegistry) *Context {
	c := &Context{
		Registry: reg,
		Types:    tr,
	}
	c.stdCtx, c.cancelFn = context.WithCancel(context.Background())
	current = c
	return c
}

// Cancel flags the cell currently running against this Context as interrupted. Safe to call
// from a different goroutine than the one executing the cell (e.g. the ZMQ Control loop
// handling an interrupt_request while the Shell loop is blocked inside Execute).
func (c *Context) Cancel() {
	if c == nil {
		return
	}
	c.cancelFn()
}

// Err returns ErrInterrupted if Cancel has been called since the last ResetCancel, nil
// otherwise. Every injected loop check calls this.
func (c *Context) Err() error {
	if c == nil || c.stdCtx.Err() == nil {
		return nil
	}
	return ErrInterrupted
}

// ResetCancel clears a previous interrupt before a new cell starts, so a resolved interrupt
// from one cell can never bleed into the next.
func (c *Context) ResetCancel() {
	if c == nil {
		return
	}
	c.cancelFn() // release the outgoing context's resources before replacing it
	c.stdCtx, c.cancelFn = context.WithCancel(context.Background())
}

// StdContext returns a standard context.Context, cancelled the moment Cancel is called. Every
// generated Execute() declares it as the cell-local `_ctx`, so user code can hand it to any
// idiomatic Go API that accepts one (net/http, os/exec, database/sql, a raw `select` on
// Done()) -- reaching blocking calls the AST-injected loop checks cannot: those only ever run
// between iterations of a `for` loop in the cell's own code, never inside a call Go has already
// descended into.
func (c *Context) StdContext() context.Context {
	if c == nil {
		return context.Background()
	}
	return c.stdCtx
}

// SetAutoResult records a cell's last expression, displayed by the kernel as an execute_result
// (equivalent to Jupyter's "Out[n]"). Generated code passes the expression itself rather than a
// pre-rendered string, so the choice between a rich representation and %#v is made here, in one
// testable place, instead of in the code generator.
func (c *Context) SetAutoResult(v any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.result, c.hasResult = autoOutput(v), true
}

// TakeResult returns the current cell's displayed result and resets the state
// for the next execution. The kernel calls this method once per cell.
func (c *Context) TakeResult() (Output, bool) {
	if c == nil {
		return Output{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out, ok := c.result, c.hasResult
	c.result, c.hasResult = Output{}, false
	return out, ok
}

// Display queues an Output to be published as a display_data message. Unlike the single
// SetAutoResult slot, this appends: a cell may show as many things as it likes.
func (c *Context) Display(o Output) {
	c.DisplayWithID(o, "", false)
}

// SetDisplayHook installs live publishing. Once set, Display goes out as it is called rather than
// at the end of the cell -- which is what makes an in-place update mean anything: a progress bar
// cannot progress if every frame is flushed after the cell has already finished.
func (c *Context) SetDisplayHook(fn func(o Output, displayID string, update bool)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.publish = fn
}

// DisplayWithID shows an Output under a handle the frontend can find again. update=false creates
// the output, update=true replaces whatever is already showing under that id -- including outputs
// from earlier cells, which is how a single progress bar can be driven from several of them.
func (c *Context) DisplayWithID(o Output, displayID string, update bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	fn := c.publish
	if fn == nil {
		// Nothing live to publish to: queue it, and drop updates, which have no meaning in a
		// batch drained once at the end.
		if !update {
			c.displays = append(c.displays, o)
		}
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// Called outside the lock: publishing reaches ZMQ, and a cell goroutine calling Display
	// while that is in flight must not be blocked behind a socket.
	fn(o, displayID, update)
}

// TakeDisplays returns everything queued since the last call and clears the queue.
//
// Known limitation, the display twin of the captured-stdout bleed-through already documented in the
// README: draining happens per cell, so an Output queued by a background goroutine between cells is
// published under whichever cell runs next. The print is never lost, only misattributed.
func (c *Context) TakeDisplays() []Output {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.displays
	c.displays = nil
	return out
}

// SetInputFunc installs the hook that Input uses. Called once at kernel start.
func (c *Context) SetInputFunc(fn func(prompt string, password bool) (string, error)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputFn = fn
}

// HasInput reports whether anything can be prompted. The session uses it to decide whether the
// cell is running somewhere with a frontend attached.
func (c *Context) HasInput() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inputFn != nil
}

// Input prompts the user and blocks until they answer.
func (c *Context) Input(prompt string, password bool) (string, error) {
	if c == nil {
		return "", errors.New("no execution context")
	}
	c.mu.Lock()
	fn := c.inputFn
	c.mu.Unlock()

	if fn == nil {
		return "", errors.New("no frontend to prompt: input is only available under a Jupyter kernel")
	}
	return fn(prompt, password)
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
