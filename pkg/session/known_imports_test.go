package session

import (
	"strings"
	"testing"
	"time"

	"github.com/alexispires/gocell/pkg/workspace"
)

// A curated set of third-party packages resolves without an import line. goimports handles the
// standard library on its own but cannot guess that `plot` means `gonum.org/v1/plot`, so
// pkg/compiler carries a small table of hints -- see knownPackages.
//
// This runs end to end rather than as a unit test on the table because the interesting part is
// everything downstream of the hint: the import has to survive goimports, reach the generated
// go.mod, and be fetched by `go build -mod=mod`.
func TestKnownImportsResolveWithoutAnImportLine(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()
	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	t.Run("gocell's own display package", func(t *testing.T) {
		res, err := sess.Execute(`display.Show(display.HTML("<i>no import line</i>"))`)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if len(res.Displays) != 1 {
			t.Fatalf("expected the Show to land, got %+v", res.Displays)
		}
	})

	t.Run("a third-party package fetched on demand", func(t *testing.T) {
		res, err := sess.Execute(`p := plot.New(); p.Title.Text = "auto"; p.Title.Text`)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Result.Data["text/plain"] != `"auto"` {
			t.Fatalf("expected the plot title back, got %v", res.Result.Data)
		}
	})

	// The guess must never win over a real symbol: `mat` is a gonum package in the table, but here
	// it is a variable, and a field access on it is not a package reference.
	t.Run("a variable shadowing a known package", func(t *testing.T) {
		if _, err := sess.Execute(`mat := struct{ Rows int }{Rows: 3}`); err != nil {
			t.Fatalf("declare: %v", err)
		}
		res, err := sess.Execute(`mat.Rows`)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		if res.Result.Data["text/plain"] != "3" {
			t.Fatalf("expected 3, got %v", res.Result.Data)
		}
	})
}

// A cell asking for input where nothing can answer must fail, not hang. gocell-repl and any test
// harness install no input hook, so this is also the contract for every non-Jupyter caller.
func TestInputWithoutAFrontend(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()
	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	res, err := sess.Execute(`v, err := display.InputErr("name? "); fmt.Println("err:", err); v`)
	if err != nil {
		t.Fatalf("the cell itself must still run: %v", err)
	}
	if !strings.Contains(res.Stdout, "err:") || strings.Contains(res.Stdout, "err: <nil>") {
		t.Fatalf("expected an error from InputErr with no frontend, got stdout %q", res.Stdout)
	}
}

// With an input hook installed, a cell's direct read of os.Stdin gets EOF instead of blocking
// forever -- the hang reported as gonb#38. Without the fd redirect this test never returns.
func TestDirectStdinReadGetsEOFRatherThanHanging(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()
	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	sess.SetInputFunc(func(string, bool) (string, error) { return "unused", nil })

	done := make(chan string, 1)
	go func() {
		res, execErr := sess.Execute(`var s string
n, err := fmt.Scanln(&s)
fmt.Println("n:", n, "err:", err)`)
		if execErr != nil {
			done <- "exec error: " + execErr.Error()
			return
		}
		done <- res.Stdout
	}()

	select {
	case out := <-done:
		if !strings.Contains(out, "EOF") {
			t.Fatalf("expected EOF from a direct stdin read, got %q", out)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("fmt.Scanln blocked: the kernel would be hung")
	}
}
