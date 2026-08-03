package runtime

import "sync"

// TypeRegistry stores type and function/method declarations that can be re-injected.
type TypeRegistry struct {
	mu    sync.RWMutex
	decls map[string]string // Key -> Full declaration source code
}

// NewTypeRegistry creates a new declaration registry.
func NewTypeRegistry() *TypeRegistry {
	return &TypeRegistry{
		decls: make(map[string]string),
	}
}

// RegisterType registers a type, method, or function declaration.
func (tr *TypeRegistry) RegisterType(name string, code string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.decls[name] = code
}

// AllTypes returns a copy of all registered declarations.
func (tr *TypeRegistry) AllTypes() map[string]string {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	res := make(map[string]string, len(tr.decls))
	for k, v := range tr.decls {
		res[k] = v
	}
	return res
}
