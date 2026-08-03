package runtime

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Registry manages the thread-safe global symbol table.
type Registry struct {
	mu      sync.RWMutex
	symbols map[string]*Symbol
	counter uint64
}

// NewRegistry creates a new symbol registry.
func NewRegistry() *Registry {
	return &Registry{
		symbols: make(map[string]*Symbol),
	}
}

// GetPointer returns the pointer to the data associated with the symbol `name`.
func (r *Registry) GetPointer(name string) unsafe.Pointer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sym, ok := r.symbols[name]; ok {
		return sym.Ptr
	}
	return nil
}

// SetPointer registers or updates a symbol in the registry.
func (r *Registry) SetPointer(name string, typeName string, ptr unsafe.Pointer, keepAlive any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := atomic.AddUint64(&r.counter, 1)
	r.symbols[name] = &Symbol{
		Name:      name,
		TypeName:  typeName,
		Ptr:       ptr,
		KeepAlive: keepAlive,
		CreatedAt: count,
	}
}

// GetSymbol returns the full symbol and whether it is present.
func (r *Registry) GetSymbol(name string) (*Symbol, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sym, ok := r.symbols[name]
	return sym, ok
}

// AllSymbols returns a copy of all registered symbols.
func (r *Registry) AllSymbols() map[string]*Symbol {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res := make(map[string]*Symbol, len(r.symbols))
	for k, v := range r.symbols {
		res[k] = v
	}
	return res
}
