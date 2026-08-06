package session

import (
	"os"
	"strings"
	"testing"

	"github.com/alexispires/gocell/pkg/workspace"
)

// countCellDirs counts the compilation subdirectories already created in the workspace;
// a reused cache entry must never create a new one (no new call to BuildPlugin), unlike
// an actual compilation.
func countCellDirs(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("Failed to read workspace %s: %v", root, err)
	}
	return len(entries)
}

// Test that the plugin cache is reused for two successive, identical executions of a cell
// with no effect on state (same symbols, same generated code): the second execution should
// be served from the cache, with no new compilation.
func TestExecuteReusesCacheWhenGeneratedSourceIsUnchanged(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	if _, err := sess.Execute(`x := 41`); err != nil {
		t.Fatalf("Initial execution failed: %v", err)
	}

	// A read-only cell (declares and mutates no variable): the generated code for two
	// successive executions with no cell in between is strictly identical.
	readOnlyCell := `fmt.Println(x)`

	if _, err := sess.Execute(readOnlyCell); err != nil {
		t.Fatalf("First read-only execution failed: %v", err)
	}
	countAfterFirst := countCellDirs(t, wsMgr.RootDir())

	if _, err := sess.Execute(readOnlyCell); err != nil {
		t.Fatalf("Second read-only execution failed: %v", err)
	}
	countAfterSecond := countCellDirs(t, wsMgr.RootDir())

	if countAfterSecond != countAfterFirst {
		t.Fatalf(
			"The second identical execution should have been served from the cache (no new cell directory); before=%d after=%d",
			countAfterFirst, countAfterSecond,
		)
	}
}

// Test that a cell's last bare expression is captured as a displayable result, and that the
// state resets between calls to Execute.
func TestExecuteCapturesDisplayResult(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	if res, err := sess.Execute(`x := 21`); err != nil || res.HasDisplay {
		t.Fatalf("Expected no display for a plain declaration, got res=%+v err=%v", res, err)
	}

	res, err := sess.Execute(`x * 2`)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}
	if !res.HasDisplay || res.DisplayText != "42" {
		t.Fatalf("Expected display '42', got %+v", res)
	}
}

// Regression test for a panic's reported line number: it must point at the cell's own
// source line, not the generated plugin file's (which has hydration/import/declaration
// boilerplate ahead of the cell's statements, so its own line numbers mean nothing to the
// user). The nil dereference below is deliberately not on line 1, so this also exercises the
// offset arithmetic, not just a coincidental 1:1 case.
func TestExecutePanicReportsOriginalCellLine(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	code := "x := 1\ny := 2\nvar p *int\n_ = *p\nz := 3"
	_, err = sess.Execute(code)
	if err == nil {
		t.Fatalf("Expected the nil dereference to be caught as an error")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("Expected the error to report the cell's own line 4 (where '_ = *p' is), got: %v", err)
	}
}
