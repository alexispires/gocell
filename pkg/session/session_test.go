package session

import (
	"os"
	"strings"
	"testing"
	"time"

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

// Regression test for a real (non-interrupt) panic occurring after an injected interrupt
// check within the same loop: the injected check adds 3 generated lines that don't exist in
// the cell's own source, and without remapPanicError's correction for that, this would
// silently report line 7 instead of line 4.
func TestExecutePanicAfterAnInjectedInterruptCheckReportsCorrectLine(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	code := "for i := 0; i < 3; i++ {\n\tif i == 2 {\n\t\tvar p *int\n\t\t_ = *p\n\t}\n}"
	_, err = sess.Execute(code)
	if err == nil {
		t.Fatalf("Expected the nil dereference to be caught as an error")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("Expected the error to report the cell's own line 4 (where '_ = *p' is), got: %v", err)
	}
}

// TestExecuteInterruptStopsAnInfiniteLoop is the core proof that Interrupt() actually works:
// a `for {}` pasted directly into a cell used to brick the kernel forever (see the backlog --
// both existing "interrupt" code paths were confirmed non-functional no-ops before this).
func TestExecuteInterruptStopsAnInfiniteLoop(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, execErr := sess.Execute("for {\n}")
		errCh <- execErr
	}()

	time.Sleep(200 * time.Millisecond) // give the loop time to actually start spinning
	sess.Interrupt()

	select {
	case execErr := <-errCh:
		if execErr == nil || !strings.Contains(execErr.Error(), "interrupted") {
			t.Fatalf("Expected an 'interrupted' error, got: %v", execErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not return after Interrupt() -- the injected check isn't working")
	}
}

// Same proof, but the infinite loop lives inside a function the cell itself declared and a
// later cell calls -- the harder half of "the most robust solution" the user asked for, since
// a declared function has no ctx parameter of its own to check (see injectInterruptChecks).
func TestExecuteInterruptStopsAnInfiniteLoopInsideADeclaredFunction(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	if _, err := sess.Execute("func slow() {\n\tfor {\n\t}\n}"); err != nil {
		t.Fatalf("Failed to declare slow: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, execErr := sess.Execute("slow()")
		errCh <- execErr
	}()

	time.Sleep(200 * time.Millisecond)
	sess.Interrupt()

	select {
	case execErr := <-errCh:
		if execErr == nil || !strings.Contains(execErr.Error(), "interrupted") {
			t.Fatalf("Expected an 'interrupted' error, got: %v", execErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not return after Interrupt() -- the declared-function case isn't working")
	}
}

// Negative case: a background goroutine (examples/live-goroutines.ipynb's own pattern) has no
// injected check at all -- injectInterruptChecks deliberately stops at any *ast.FuncLit -- so
// an interrupt aimed at some later, unrelated stuck cell must never reach it.
func TestInterruptDoesNotAffectABackgroundGoroutine(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	code := `jobs := make(chan int, 1)
results := make(chan int, 1)
go func() {
	for {
		n := <-jobs
		results <- n * 2
	}
}()`
	if _, err := sess.Execute(code); err != nil {
		t.Fatalf("Failed to start background goroutine: %v", err)
	}

	// Aimed at some later, unrelated stuck cell -- must not reach the goroutine above, which
	// has no injected check to ever see it.
	sess.Interrupt()

	if _, err := sess.Execute(`jobs <- 21`); err != nil {
		t.Fatalf("Failed to submit a job: %v", err)
	}

	res, err := sess.Execute(`n := <-results`)
	if err != nil {
		t.Fatalf("Expected the background goroutine to still be alive and respond: %v", err)
	}
	_ = res

	ptrN := sess.ctx.GetPointer("n")
	if ptrN == nil || *(*int)(ptrN) != 42 {
		t.Fatalf("Expected n = 42 from the still-alive background goroutine")
	}
}

// A panic on any goroutine other than the one running Execute is, by Go's own default
// behavior, fatal to the whole process -- unlike a normal cell error, which pkg/plugin/loader.go
// already recovers around Execute itself. Proof this doesn't apply here: if the injected
// recover (pkg/compiler's injectGoroutinePanicRecovery) weren't working, this whole test binary
// would crash, not report a clean test failure -- there is no way to catch a real process crash
// from within the same process, so this test's realistic pass/fail signal is the actual pass:
// the test process reaching its later assertions at all.
func TestGoroutinePanicDoesNotCrashTheSession(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	code := "go func() {\n\tvar p *int\n\t_ = *p\n}()"
	if _, err := sess.Execute(code); err != nil {
		t.Fatalf("Starting the goroutine should not itself error: %v", err)
	}

	time.Sleep(200 * time.Millisecond) // give the goroutine time to actually run and panic

	res, err := sess.Execute(`fmt.Println("still alive")`)
	if err != nil {
		t.Fatalf("Expected the session to still be usable after a background goroutine panicked: %v", err)
	}
	if !strings.Contains(res.Stdout, "still alive") {
		t.Fatalf("Expected stdout to contain 'still alive', got %q", res.Stdout)
	}
}

// Negative case for the same fix: a panic on the goroutine actually running Execute (i.e. a
// normal, synchronous cell panic, not one on a goroutine the cell itself started) must still
// be reported as a cell error, not silently swallowed -- injectGoroutinePanicRecovery only
// wraps `go func(){...}()` literals, never Execute's own call frame.
func TestSynchronousPanicIsStillReportedNotSwallowed(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}

	if _, err := sess.Execute("var p *int\n_ = *p"); err == nil {
		t.Fatalf("Expected a synchronous panic to still be reported as a cell error")
	}
}
