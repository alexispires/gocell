package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Cache manages the registry of compiled plugins identified by a SHA256 hash.
type Cache struct {
	mu    sync.RWMutex
	items map[string]string // hash -> soPath
}

// NewCache creates a new plugin cache.
func NewCache() *Cache {
	return &Cache{
		items: make(map[string]string),
	}
}

// ComputeHash computes the SHA256 hash of the user source code and the visible symbols/types.
func ComputeHash(code string, stateSig string) string {
	h := sha256.New()
	h.Write([]byte(code))
	h.Write([]byte("::"))
	h.Write([]byte(stateSig))
	return hex.EncodeToString(h.Sum(nil))
}

// Get looks up a compiled .so file path for a given hash.
func (c *Cache) Get(hash string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	soPath, ok := c.items[hash]
	return soPath, ok
}

// Put associates a hash with the compiled .so file path.
func (c *Cache) Put(hash string, soPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[hash] = soPath
}
