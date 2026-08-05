package compiler_test

import (
	"gocell/pkg/compiler"
	"gocell/pkg/plugin"
	"gocell/pkg/runtime"
	"gocell/pkg/workspace"
	"testing"
)

// Shared helper to run a sequence of cells and return the final Context
func runNotebookSession(t *testing.T, cells []string) *runtime.Context {
	t.Helper()

	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	t.Cleanup(func() { wsMgr.CleanUp() })

	reg := runtime.NewRegistry()
	tr := runtime.NewTypeRegistry()
	ctx := runtime.NewContext(reg, tr)

	importTracker := compiler.NewImportTracker()
	builder, err := compiler.NewBuilder("")
	if err != nil {
		t.Fatalf("Failed to create builder: %v", err)
	}
	loader := plugin.NewLoader()

	for i, code := range cells {
		cellDir, _, err := wsMgr.CreateCellDir()
		if err != nil {
			t.Fatalf("Failed to create cellDir for cell %d: %v", i+1, err)
		}

		parsed, err := compiler.ParseCell(code)
		if err != nil {
			t.Fatalf("Failed to parse cell %d: %v\nCode: %s", i+1, err, code)
		}

		analysis, err := compiler.AnalyzeCell(parsed, reg, importTracker, tr)
		if err != nil {
			t.Fatalf("Failed to analyze: %v", err)
		}
		generatedCode := compiler.GeneratePluginCode(parsed, analysis, importTracker, tr)

		soPath, err := builder.BuildPlugin(cellDir, generatedCode)
		if err != nil {
			t.Fatalf("Failed to compile cell %d: %v\nGenerated code:\n%s", i+1, err, generatedCode)
		}

		if err := loader.LoadAndExecute(soPath, ctx); err != nil {
			t.Fatalf("Failed to execute cell %d: %v", i+1, err)
		}
	}

	return ctx
}

func TestCompilerPipeline(t *testing.T) {
	cells := []string{
		`import "fmt"; a := 42; fmt.Println("Cell 1 OK, a =", a)`,
		`b := a + 8`,
	}
	ctx := runNotebookSession(t, cells)

	ptrB := ctx.GetPointer("b")
	if ptrB == nil {
		t.Fatalf("Variable 'b' not found")
	}
	if *(*int)(ptrB) != 50 {
		t.Fatalf("Expected value of 'b' to be 50, got: %d", *(*int)(ptrB))
	}
}
