package compiler_test

import (
	"github.com/alexispires/gocell/pkg/compiler"
	"github.com/alexispires/gocell/pkg/plugin"
	"github.com/alexispires/gocell/pkg/runtime"
	"github.com/alexispires/gocell/pkg/session"
	"github.com/alexispires/gocell/pkg/workspace"
	"testing"
)

// Test auto-displaying a cell's last expression (equivalent to Jupyter's "Out[n]").
// A declaration or a bare function call (already a valid Go statement) should display
// nothing; a bare expression that would not compile as-is (bare identifier, compound
// expression) must be captured and reported via ctx.TakeResult().
func TestDisplayLastExpression(t *testing.T) {
	t.Parallel()

	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	reg := runtime.NewRegistry()
	tr := runtime.NewTypeRegistry()
	ctx := runtime.NewContext(reg, tr)

	importTracker := compiler.NewImportTracker()
	builder, err := compiler.NewBuilder("")
	if err != nil {
		t.Fatalf("Failed to create builder: %v", err)
	}
	loader := plugin.NewLoader()

	run := func(code string) {
		t.Helper()
		cellDir, _, err := wsMgr.CreateCellDir()
		if err != nil {
			t.Fatalf("Failed to create cellDir: %v", err)
		}
		parsed, err := compiler.ParseCell(code)
		if err != nil {
			t.Fatalf("Failed to parse: %v\nCode: %s", err, code)
		}
		analysis, err := compiler.AnalyzeCell(parsed, reg, importTracker, tr)
		if err != nil {
			t.Fatalf("Failed to analyze: %v", err)
		}
		generated, _ := compiler.GeneratePluginCode(parsed, analysis, importTracker, tr)
		soPath, err := builder.BuildPlugin(cellDir, generated)
		if err != nil {
			t.Fatalf("Failed to compile: %v\nGenerated code:\n%s", err, generated)
		}
		if err := loader.LoadAndExecute(soPath, ctx); err != nil {
			t.Fatalf("Failed to execute: %v", err)
		}
	}

	// Cell 1: simple declaration, no final expression -> no result
	run(`x := 21`)
	if _, ok := ctx.TakeResult(); ok {
		t.Fatalf("No result should be set for a simple declaration")
	}

	// Cell 2: bare identifier as the last line -> auto-displayed
	run(`x`)
	if out, ok := ctx.TakeResult(); !ok || out.Data["text/plain"] != "21" {
		t.Fatalf("Expected result '21', got %v (ok=%v)", out.Data, ok)
	}

	// Cell 3: compound expression as the last line -> auto-displayed
	run(`x * 2`)
	if out, ok := ctx.TakeResult(); !ok || out.Data["text/plain"] != "42" {
		t.Fatalf("Expected result '42', got %v (ok=%v)", out.Data, ok)
	}

	// Cell 4: bare function call -> already valid Go, no auto-display
	run(`fmt.Sprintf("%d", x)`)
	if out, ok := ctx.TakeResult(); ok {
		t.Fatalf("A bare function call should not trigger auto-display, got %v", out.Data)
	}
}

// A type conversion is an *ast.CallExpr just like a function call, but unlike a call it is not a
// valid statement on its own -- `int64(5)` alone fails to compile with "is not used". Auto-display
// used to skip every CallExpr, so any cell ending in a conversion was a compile error.
func TestDisplayTypeConversion(t *testing.T) {
	t.Parallel()

	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := session.New(wsMgr)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	cases := []struct {
		code string
		want string
	}{
		{`int64(5)`, "5"},
		{`float64(3)`, "3"},
		{`string([]byte("hi"))`, `"hi"`},
	}
	for _, c := range cases {
		res, err := sess.Execute(c.code)
		if err != nil {
			t.Errorf("%s: %v", c.code, err)
			continue
		}
		if !res.HasResult || res.Result.Data["text/plain"] != c.want {
			t.Errorf("%s: expected %q, got %v", c.code, c.want, res.Result.Data)
		}
	}

	// A conversion to a type the cell itself declared must work the same way.
	if _, err := sess.Execute(`type Celsius float64`); err != nil {
		t.Fatalf("declaring the type failed: %v", err)
	}
	res, err := sess.Execute(`Celsius(21.5)`)
	if err != nil {
		t.Fatalf("Celsius(21.5): %v", err)
	}
	if !res.HasResult {
		t.Fatalf("expected a displayed result, got %+v", res)
	}

	// The other half of the rule must not regress: a bare call is already a valid statement and
	// still displays nothing.
	res, err = sess.Execute(`fmt.Sprintf("%d", 1)`)
	if err != nil {
		t.Fatalf("bare call: %v", err)
	}
	if res.HasResult {
		t.Fatalf("a bare function call must not auto-display, got %v", res.Result.Data)
	}
}
