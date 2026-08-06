package compiler_test

import (
	"gocell/pkg/compiler"
	"gocell/pkg/plugin"
	"gocell/pkg/runtime"
	"gocell/pkg/workspace"
	"strings"
	"testing"
)

// Test catching an explicit panic and resuming the session
func TestResilienceExplicitPanic(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer wsMgr.CleanUp()

	reg := runtime.NewRegistry()
	tr := runtime.NewTypeRegistry()
	ctx := runtime.NewContext(reg, tr)

	importTracker := compiler.NewImportTracker()
	builder, _ := compiler.NewBuilder("")
	loader := plugin.NewLoader()

	// Cell 1: Initialization
	cellDir1, _, _ := wsMgr.CreateCellDir()
	p1, _ := compiler.ParseCell(`a := 10`)
	an1, _ := compiler.AnalyzeCell(p1, reg, importTracker, tr)
	g1, _ := compiler.GeneratePluginCode(p1, an1, importTracker, tr)
	so1, _ := builder.BuildPlugin(cellDir1, g1)
	if err := loader.LoadAndExecute(so1, ctx); err != nil {
		t.Fatalf("Cell 1 failed: %v", err)
	}

	// Cell 2: Explicit panic
	cellDir2, _, _ := wsMgr.CreateCellDir()
	p2, _ := compiler.ParseCell(`panic("simulated fatal error")`)
	an2, _ := compiler.AnalyzeCell(p2, reg, importTracker, tr)
	g2, _ := compiler.GeneratePluginCode(p2, an2, importTracker, tr)
	so2, _ := builder.BuildPlugin(cellDir2, g2)

	errPanic := loader.LoadAndExecute(so2, ctx)
	if errPanic == nil {
		t.Fatalf("A panic should have been caught for cell 2")
	}

	if !strings.Contains(errPanic.Error(), "simulated fatal error") {
		t.Fatalf("Unexpected panic message: %v", errPanic)
	}

	// Cell 3: Normal execution after the panic
	cellDir3, _, _ := wsMgr.CreateCellDir()
	p3, _ := compiler.ParseCell(`b := a + 20`)
	an3, _ := compiler.AnalyzeCell(p3, reg, importTracker, tr)
	g3, _ := compiler.GeneratePluginCode(p3, an3, importTracker, tr)
	so3, _ := builder.BuildPlugin(cellDir3, g3)
	if err := loader.LoadAndExecute(so3, ctx); err != nil {
		t.Fatalf("Cell 3 failed after panic: %v", err)
	}

	ptrB := ctx.GetPointer("b")
	if ptrB == nil || *(*int)(ptrB) != 30 {
		t.Fatalf("Expected value of 'b' to be 30")
	}
}

// Test catching a nil pointer dereference
func TestResilienceNilPointerPanic(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	defer wsMgr.CleanUp()

	reg := runtime.NewRegistry()
	tr := runtime.NewTypeRegistry()
	ctx := runtime.NewContext(reg, tr)

	importTracker := compiler.NewImportTracker()
	builder, _ := compiler.NewBuilder("")
	loader := plugin.NewLoader()

	// Cell 1: Nil pointer dereference
	cellDir1, _, _ := wsMgr.CreateCellDir()
	p1, _ := compiler.ParseCell(`var ptr *int; *ptr = 100`)
	an1, _ := compiler.AnalyzeCell(p1, reg, importTracker, tr)
	g1, _ := compiler.GeneratePluginCode(p1, an1, importTracker, tr)
	so1, _ := builder.BuildPlugin(cellDir1, g1)

	errPanic := loader.LoadAndExecute(so1, ctx)
	if errPanic == nil {
		t.Fatalf("A panic should have been caught")
	}

	// Cell 2: Normal execution after the panic
	cellDir2, _, _ := wsMgr.CreateCellDir()
	p2, _ := compiler.ParseCell(`recoveredVar := 99`)
	an2, _ := compiler.AnalyzeCell(p2, reg, importTracker, tr)
	g2, _ := compiler.GeneratePluginCode(p2, an2, importTracker, tr)
	so2, _ := builder.BuildPlugin(cellDir2, g2)
	if err := loader.LoadAndExecute(so2, ctx); err != nil {
		t.Fatalf("Cell 2 failed: %v", err)
	}

	ptrRec := ctx.GetPointer("recoveredVar")
	if ptrRec == nil || *(*int)(ptrRec) != 99 {
		t.Fatalf("Incorrect 'recoveredVar' value")
	}
}
