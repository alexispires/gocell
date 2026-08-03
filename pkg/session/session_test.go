package session

import (
	"os"
	"testing"

	"gosk/pkg/workspace"
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
	defer wsMgr.CleanUp()

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
	defer wsMgr.CleanUp()

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
