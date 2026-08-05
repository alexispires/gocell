package compiler_test

import (
	"testing"

	"gosk/pkg/compiler"
	"gosk/pkg/runtime"
)

// analyzeCell seeds a Registry the way earlier cells would have and analyzes one cell,
// without compiling anything -- these assert the analyzer's contract directly, which the
// compile-based tests elsewhere can only check indirectly through whether the cell builds.
func analyzeCell(t *testing.T, code string, seed map[string]string) *compiler.AnalysisResult {
	t.Helper()

	reg := runtime.NewRegistry()
	for name, typeName := range seed {
		var box int
		reg.SetPointer(name, typeName, nil, &box)
	}

	cell, err := compiler.ParseCell(code)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}
	res, err := compiler.AnalyzeCell(cell, reg, compiler.NewImportTracker(), runtime.NewTypeRegistry())
	if err != nil {
		t.Fatalf("Failed to analyze: %v", err)
	}
	return res
}

// A top-level `const` is not a variable: its address cannot be taken, so exporting it to the
// Registry emits `unsafe.Pointer(&c)` and the cell fails to compile with "cannot take address
// of c (untyped int constant)". Resolving through go/types tells constants and variables
// apart (*types.Const vs *types.Var), which name matching could not.
func TestAnalyzeTopLevelConstIsNotExported(t *testing.T) {
	res := analyzeCell(t, "const factor = 7\nscaled := factor * 6", nil)

	for _, name := range res.NewVariables {
		if name == "factor" {
			t.Fatalf("A const must not be exported to the Registry, got NewVariables=%v", res.NewVariables)
		}
	}
	if len(res.NewVariables) != 1 || res.NewVariables[0] != "scaled" {
		t.Fatalf("Expected NewVariables=[scaled], got %v", res.NewVariables)
	}
}

// `x, fresh := 1, 2` where only x exists: Go reuses x and declares only fresh, so x must be
// hydrated (not re-exported) and fresh exported. The redefinition rewrite deliberately leaves
// this `:=` alone -- rewriting it to `=` would be a compile error, since fresh is new.
func TestAnalyzeMixedRedeclarationReusesExistingSymbol(t *testing.T) {
	res := analyzeCell(t, "x, fresh := 1, 2", map[string]string{"x": "int"})

	if _, used := res.UsedSymbols["x"]; !used {
		t.Fatalf("Expected the existing 'x' to be hydrated, got UsedSymbols=%v", res.UsedSymbols)
	}
	if len(res.NewVariables) != 1 || res.NewVariables[0] != "fresh" {
		t.Fatalf("Expected NewVariables=[fresh], got %v", res.NewVariables)
	}
}
