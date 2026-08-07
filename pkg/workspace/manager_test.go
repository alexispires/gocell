package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexispires/gocell/pkg/workspace"
)

func TestNewManagerWithEmptyBaseDirCreatesATempDir(t *testing.T) {
	m, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() { _ = m.CleanUp() }()

	if m.RootDir() == "" {
		t.Fatalf("Expected a non-empty RootDir")
	}
	if info, err := os.Stat(m.RootDir()); err != nil || !info.IsDir() {
		t.Fatalf("Expected RootDir to exist as a directory, got err=%v", err)
	}
}

func TestNewManagerWithExplicitBaseDirCreatesIt(t *testing.T) {
	target := filepath.Join(t.TempDir(), "does", "not", "exist", "yet")

	m, err := workspace.NewManager(target)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() { _ = m.CleanUp() }()

	if m.RootDir() != target {
		t.Fatalf("Expected RootDir %q, got %q", target, m.RootDir())
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("Expected the explicit baseDir to have been created, got err=%v", err)
	}
}

func TestCreateCellDirCreatesDistinctIncrementingSubdirs(t *testing.T) {
	m, err := workspace.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer func() { _ = m.CleanUp() }()

	dir1, id1, err := m.CreateCellDir()
	if err != nil {
		t.Fatalf("First CreateCellDir failed: %v", err)
	}
	dir2, id2, err := m.CreateCellDir()
	if err != nil {
		t.Fatalf("Second CreateCellDir failed: %v", err)
	}

	if id1 != 1 || id2 != 2 {
		t.Fatalf("Expected ids 1 then 2, got %d then %d", id1, id2)
	}
	if dir1 == dir2 {
		t.Fatalf("Expected distinct cell directories, got the same path twice: %q", dir1)
	}
	for _, dir := range []string{dir1, dir2} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("Expected %q to exist as a directory, got err=%v", dir, err)
		}
	}
}

func TestCleanUpRemovesTheWholeTree(t *testing.T) {
	m, err := workspace.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	cellDir, _, err := m.CreateCellDir()
	if err != nil {
		t.Fatalf("CreateCellDir failed: %v", err)
	}

	if err := m.CleanUp(); err != nil {
		t.Fatalf("CleanUp failed: %v", err)
	}
	if _, err := os.Stat(m.RootDir()); !os.IsNotExist(err) {
		t.Fatalf("Expected RootDir to be gone after CleanUp, got err=%v", err)
	}
	if _, err := os.Stat(cellDir); !os.IsNotExist(err) {
		t.Fatalf("Expected the cell subdirectory to be gone after CleanUp too, got err=%v", err)
	}
}

func TestCleanUpOnAZeroValueManagerIsANoOp(t *testing.T) {
	var m workspace.Manager
	if err := m.CleanUp(); err != nil {
		t.Fatalf("Expected CleanUp on an empty rootDir to be a no-op, got: %v", err)
	}
}
