package compiler_test

import (
	"testing"

	"github.com/alexispires/gocell/pkg/compiler"
	"github.com/alexispires/gocell/pkg/runtime"
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
	t.Parallel()

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
	t.Parallel()

	res := analyzeCell(t, "x, fresh := 1, 2", map[string]string{"x": "*int"})

	if _, used := res.UsedSymbols["x"]; !used {
		t.Fatalf("Expected the existing 'x' to be hydrated, got UsedSymbols=%v", res.UsedSymbols)
	}
	if len(res.NewVariables) != 1 || res.NewVariables[0] != "fresh" {
		t.Fatalf("Expected NewVariables=[fresh], got %v", res.NewVariables)
	}
}

// AnalyzeCell splices the cell's own statements into the analysis function *after* the
// candidate declarations, both parsed into the shared CellContent.Fset -- meaning the
// candidates' positions are numerically higher than the cell's own statements' positions.
// This regression-tests that go/types' resolution isn't confused by that ordering for the one
// scenario where position genuinely matters: a reference before a same-block shadowing `:=`
// must still resolve to the outer (candidate) symbol, and a reference after it must resolve to
// the new inner one -- not the other way around, and not a resolution failure.
func TestAnalyzeShadowResolvesCorrectlyDespiteCandidateFilePosition(t *testing.T) {
	t.Parallel()

	res := analyzeCell(t, `
before := count
if true {
	count := "shadow"
	_ = count
}
after := count
`, map[string]string{"count": "*int"})

	if _, used := res.UsedSymbols["count"]; !used {
		t.Fatalf("Expected the outer candidate 'count' to be hydrated (referenced before and after the shadow), got UsedSymbols=%v", res.UsedSymbols)
	}
	wantNew := map[string]bool{"before": true, "after": true}
	if len(res.NewVariables) != len(wantNew) {
		t.Fatalf("Expected NewVariables=%v (the inner shadow must not leak out as a new top-level symbol), got %v", wantNew, res.NewVariables)
	}
	for _, name := range res.NewVariables {
		if !wantNew[name] {
			t.Fatalf("Unexpected new variable %q, got NewVariables=%v", name, res.NewVariables)
		}
	}
}
