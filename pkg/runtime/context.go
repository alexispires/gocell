package runtime

import "unsafe"

// Context is the object passed to the Execute(ctx) function of every plugin.
type Context struct {
	Registry *Registry
	Types    *TypeRegistry

	resultText string
	hasResult  bool
}

// NewContext initializes an execution Context.
func NewContext(reg *Registry, tr *TypeRegistry) *Context {
	return &Context{
		Registry: reg,
		Types:    tr,
	}
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
