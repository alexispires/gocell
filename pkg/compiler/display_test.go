package compiler_test

import (
	"gocell/pkg/compiler"
	"gocell/pkg/plugin"
	"gocell/pkg/runtime"
	"gocell/pkg/workspace"
	"testing"
)

// Test auto-displaying a cell's last expression (equivalent to Jupyter's "Out[n]").
// A declaration or a bare function call (already a valid Go statement) should display
// nothing; a bare expression that would not compile as-is (bare identifier, compound
// expression) must be captured and reported via ctx.TakeResult().
func TestDisplayLastExpression(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer wsMgr.CleanUp()

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
		generated := compiler.GeneratePluginCode(parsed, analysis, importTracker, tr)
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
	if text, ok := ctx.TakeResult(); !ok || text != "21" {
		t.Fatalf("Expected result '21', got %q (ok=%v)", text, ok)
	}

	// Cell 3: compound expression as the last line -> auto-displayed
	run(`x * 2`)
	if text, ok := ctx.TakeResult(); !ok || text != "42" {
		t.Fatalf("Expected result '42', got %q (ok=%v)", text, ok)
	}

	// Cell 4: bare function call -> already valid Go, no auto-display
	run(`fmt.Sprintf("%d", x)`)
	if text, ok := ctx.TakeResult(); ok {
		t.Fatalf("A bare function call should not trigger auto-display, got %q", text)
	}
}
