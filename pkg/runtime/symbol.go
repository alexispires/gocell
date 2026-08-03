package runtime

import "unsafe"

// Symbol represents a symbol exported by a cell in memory.
type Symbol struct {
	Name      string
	TypeName  string
	Ptr       unsafe.Pointer
	KeepAlive any
	CreatedAt uint64
}
