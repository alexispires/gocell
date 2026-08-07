package compiler_test

import (
	"testing"

	"github.com/alexispires/gocell/pkg/compiler"
	"github.com/alexispires/gocell/pkg/plugin"
	"github.com/alexispires/gocell/pkg/runtime"
	"github.com/alexispires/gocell/pkg/workspace"
)

// Regression test for spurious plugin-cache misses: GeneratePluginCode used to range directly
// over two maps (TypeRegistry.AllTypes(), AnalysisResult.UsedSymbols), and Go's map iteration
// order is randomized per range -- so byte-identical cell semantics could hash differently
// between runs, defeating pkg/plugin.Cache. Seeds both maps with several entries (order
// sensitivity needs more than one to ever show up) and calls GeneratePluginCode repeatedly on
// the same inputs, asserting every call produces byte-identical output.
func TestGeneratePluginCodeIsDeterministic(t *testing.T) {
	t.Parallel()

	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	t.Cleanup(func() { _ = wsMgr.CleanUp() })

	reg := runtime.NewRegistry()
	tr := runtime.NewTypeRegistry()
	ctx := runtime.NewContext(reg, tr)
	importTracker := compiler.NewImportTracker()
	builder, err := compiler.NewBuilder("")
	if err != nil {
		t.Fatalf("Failed to create builder: %v", err)
	}
	loader := plugin.NewLoader()

	// Seed several existing Registry symbols (exercises the UsedSymbols sort) and several
	// declared types/funcs (exercises the TypeRegistry sort) -- a single entry in either map
	// wouldn't exercise ordering at all.
	seedCode := "aaa := 1\nbbb := 2\nccc := 3\nddd := 4\n\n" +
		"type Foo struct{}\ntype Bar struct{}\n\n" +
		"func Baz() int { return 1 }\nfunc Qux() int { return 2 }"

	seedCell, err := compiler.ParseCell(seedCode)
	if err != nil {
		t.Fatalf("Failed to parse seed cell: %v", err)
	}
	seedAnalysis, err := compiler.AnalyzeCell(seedCell, reg, importTracker, tr)
	if err != nil {
		t.Fatalf("Failed to analyze seed cell: %v", err)
	}
	seedGenerated, _ := compiler.GeneratePluginCode(seedCell, seedAnalysis, importTracker, tr)

	cellDir, _, err := wsMgr.CreateCellDir()
	if err != nil {
		t.Fatalf("Failed to create cellDir: %v", err)
	}
	seedSoPath, err := builder.BuildPlugin(cellDir, seedGenerated)
	if err != nil {
		t.Fatalf("Failed to build seed cell: %v\n%s", err, seedGenerated)
	}
	if err := loader.LoadAndExecute(seedSoPath, ctx); err != nil {
		t.Fatalf("Failed to execute seed cell: %v", err)
	}

	// A second cell using every seeded symbol: AnalysisResult.UsedSymbols ends up with 4
	// entries, and TypeRegistry.AllTypes() has 4 (2 types, 2 funcs).
	useCell, err := compiler.ParseCell("sum := aaa + bbb + ccc + ddd + Baz() + Qux()")
	if err != nil {
		t.Fatalf("Failed to parse use cell: %v", err)
	}
	useAnalysis, err := compiler.AnalyzeCell(useCell, reg, importTracker, tr)
	if err != nil {
		t.Fatalf("Failed to analyze use cell: %v", err)
	}
	if len(useAnalysis.UsedSymbols) < 4 {
		t.Fatalf("Expected at least 4 used symbols to exercise the sort, got %d", len(useAnalysis.UsedSymbols))
	}

	first, _ := compiler.GeneratePluginCode(useCell, useAnalysis, importTracker, tr)
	for i := 0; i < 25; i++ {
		got, _ := compiler.GeneratePluginCode(useCell, useAnalysis, importTracker, tr)
		if got != first {
			t.Fatalf("GeneratePluginCode produced non-deterministic output on iteration %d:\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}
}
