package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// Manager manages the temporary directory where cell .go and .so source files are
// written and compiled.
type Manager struct {
	rootDir string
	counter uint64
}

// NewManager creates a workspace manager in the specified directory.
func NewManager(baseDir string) (*Manager, error) {
	if baseDir == "" {
		tmpDir, err := os.MkdirTemp("", "gocell-workspace-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create temporary directory: %w", err)
		}
		baseDir = tmpDir
	} else {
		if err := os.MkdirAll(baseDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create baseDir directory %s: %w", baseDir, err)
		}
	}

	return &Manager{rootDir: baseDir}, nil
}

// RootDir returns the absolute path of the workspace's root directory.
func (m *Manager) RootDir() string {
	return m.rootDir
}

// CreateCellDir creates a dedicated subdirectory for compiling a given cell.
func (m *Manager) CreateCellDir() (string, uint64, error) {
	id := atomic.AddUint64(&m.counter, 1)
	cellDir := filepath.Join(m.rootDir, fmt.Sprintf("cell_%d", id))
	if err := os.MkdirAll(cellDir, 0755); err != nil {
		return "", id, fmt.Errorf("failed to create cell directory %s: %w", cellDir, err)
	}
	return cellDir, id, nil
}

// CleanUp removes the workspace's temporary directory tree.
func (m *Manager) CleanUp() error {
	if m.rootDir != "" {
		return os.RemoveAll(m.rootDir)
	}
	return nil
}
