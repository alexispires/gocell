package compiler

import (
	"go/ast"
	"go/importer"
	"go/printer"
	"go/types"
	"strings"
	"testing"

	"gocell/pkg/runtime"
)

// rewriteForTest parses code, seeds a Registry-shaped `existing` map from seed (name -> Go
// type string), type-checks it exactly as analyzeWithTypeChecker would, and applies
// rewriteUsedSymbolAccess directly -- bypassing AnalyzeCell, since the rewrite isn't wired
// into the real pipeline yet (that happens in a later milestone, alongside the matching
// GeneratePluginCode change). Returns the rewritten cell.Stmts printed back to source.
func rewriteForTest(t *testing.T, code string, seed map[string]string) string {
	t.Helper()

	existing := make(map[string]*runtime.Symbol, len(seed))
	for name, typeName := range seed {
		existing[name] = &runtime.Symbol{Name: name, TypeName: typeName}
	}

	cell, err := ParseCell(code)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	candidates := sortedSymbolNames(existing)
	file, err := buildAnalysisFile(cell, candidates, existing, NewImportTracker(), runtime.NewTypeRegistry())
	if err != nil {
		t.Fatalf("Failed to build analysis file: %v", err)
	}

	info := &types.Info{
		Uses:   make(map[*ast.Ident]types.Object),
		Defs:   make(map[*ast.Ident]types.Object),
		Scopes: make(map[ast.Node]*types.Scope),
	}
	conf := types.Config{Error: func(error) {}, Importer: importer.Default(), DisableUnusedImportCheck: true}
	conf.Check("main", cell.Fset, []*ast.File{file}, info)

	fn := findFuncDecl(file, analysisFuncName)
	if fn == nil {
		t.Fatalf("analysis function missing from parsed source")
	}
	funcScope := info.Scopes[fn.Type]
	if funcScope == nil {
		t.Fatalf("no scope resolved for the analysis function")
	}

	candidateObjs := make(map[types.Object]string, len(candidates))
	for _, name := range candidates {
		if obj := funcScope.Lookup(name); obj != nil {
			candidateObjs[obj] = name
		}
	}

	rewriteUsedSymbolAccess(cell.Stmts, info, candidateObjs)

	var sb strings.Builder
	for _, stmt := range cell.Stmts {
		var buf strings.Builder
		if err := printer.Fprint(&buf, cell.Fset, stmt); err != nil {
			t.Fatalf("Failed to print rewritten stmt: %v", err)
		}
		sb.WriteString(buf.String())
		sb.WriteString("\n")
	}
	return sb.String()
}

// Covers the 5 syntactic forms already verified (standalone, outside this codebase) to
// compose correctly under Go's own semantics -- deref, reassignment, selector, function call,
// address-of -- plus 3 forms flagged as reasoned-through-but-untested in that design work:
// compound assignment, increment/decrement, and range. Every expected output below was
// additionally compiled and run standalone (not just printed) to confirm it's not just
// syntactically plausible but semantically correct Go.
func TestRewriteUsedSymbolAccess(t *testing.T) {
	cases := []struct {
		name string
		code string
		seed map[string]string
		want string
	}{
		{"deref", "*foo = 5", map[string]string{"foo": "*int"}, "**foo_ptr = 5\n"},
		{"reassignment", "foo = &Bar{N: 1}", map[string]string{"foo": "*Bar"}, "*foo_ptr = &Bar{N: 1}\n"},
		{"selector", "n := foo.N", map[string]string{"foo": "*Bar"}, "n := (*foo_ptr).N\n"},
		{"call", "foo.Method()", map[string]string{"foo": "*Bar"}, "(*foo_ptr).\n\tMethod()\n"},
		{"addressOf", "bar := &foo", map[string]string{"foo": "int"}, "bar := &*foo_ptr\n"},
		{"compoundAssign", "x += 1", map[string]string{"x": "int"}, "*x_ptr += 1\n"},
		{"incDec", "x++", map[string]string{"x": "int"}, "*x_ptr++\n"},
		{
			"rangeOver",
			"for i, v := range xs {\n_ = i\n_ = v\n}",
			map[string]string{"xs": "[]int"},
			"for i, v := range *xs_ptr {\n\t_ = i\n\t_ = v\n}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteForTest(t, c.code, c.seed)
			if got != c.want {
				t.Fatalf("rewrite of %q:\n got:  %q\n want: %q", c.code, got, c.want)
			}
		})
	}
}

// Shadowed occurrences must be left untouched: the rewrite only fires when go/types itself
// resolved an identifier to the exact candidate object, so a nested `:=` that shadows a
// Registry symbol is a different object and its own occurrences are never rewritten -- while
// occurrences of the real outer candidate, before and after the shadow, still are.
func TestRewriteDoesNotTouchShadowedOccurrences(t *testing.T) {
	code := `before := count
if true {
	count := "shadow"
	_ = count
}
after := count
`
	got := rewriteForTest(t, code, map[string]string{"count": "int"})
	if strings.Contains(got, `"shadow"`) == false {
		t.Fatalf("expected the shadow's own string literal to survive untouched, got:\n%s", got)
	}
	if strings.Contains(got, "count_ptr") == false {
		t.Fatalf("expected the outer candidate's occurrences to be rewritten to count_ptr, got:\n%s", got)
	}
	if strings.Count(got, "*count_ptr") != 2 {
		t.Fatalf("expected exactly 2 rewritten occurrences (before and after the shadow), got:\n%s", got)
	}
	// The shadow's own declaration and its inner `_ = count` use must be untouched --
	// neither should ever have been turned into count_ptr.
	if strings.Contains(got, `count := "shadow"`) == false {
		t.Fatalf("expected the shadow's own declaration to survive untouched, got:\n%s", got)
	}
}
