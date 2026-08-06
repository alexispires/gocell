package compiler

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/types"
	"sort"

	"github.com/alexispires/gocell/pkg/runtime"
)

// ResolveMembers answers "what's valid after `foo.`" for a cell that's still being typed:
// codeBeforeDot is everything up to (not including) the dot, which must end in a bare
// expression statement naming the value being completed (typically a single identifier,
// e.g. "x := &Foo{}\nx" for completing "x."). It reuses AnalyzeCell's own machinery
// (buildAnalysisFile, rewriteTopLevelRedefinitions) so `foo` resolves exactly as it would in
// a real cell -- whether it's an existing Registry symbol from an earlier cell or one just
// declared earlier in this same, not-yet-submitted one -- then reads its go/types type off
// the trailing expression and lists that type's fields and methods.
//
// Returns (nil, err) if codeBeforeDot doesn't parse, has no statements, or its type can't be
// resolved -- callers should treat that as "no member completions available", not an error to
// surface to the user (the code is, by definition, still being typed).
func ResolveMembers(codeBeforeDot string, reg *runtime.Registry, importTracker *ImportTracker, typeRegistry *runtime.TypeRegistry) ([]string, error) {
	cell, err := ParseCell(codeBeforeDot)
	if err != nil {
		return nil, err
	}
	if len(cell.Stmts) == 0 {
		return nil, fmt.Errorf("no statements to resolve a type from")
	}
	exprStmt, ok := cell.Stmts[len(cell.Stmts)-1].(*ast.ExprStmt)
	if !ok {
		return nil, fmt.Errorf("expected a trailing bare expression, got %T", cell.Stmts[len(cell.Stmts)-1])
	}

	existing := reg.AllSymbols()
	rewriteTopLevelRedefinitions(cell, existing)

	candidates := sortedSymbolNames(existing)
	file, err := buildAnalysisFile(cell, candidates, existing, importTracker, typeRegistry)
	if err != nil {
		return nil, err
	}

	info := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Uses:  make(map[*ast.Ident]types.Object),
		Defs:  make(map[*ast.Ident]types.Object),
	}
	conf := types.Config{
		Error:                    func(error) {},
		Importer:                 importer.Default(),
		DisableUnusedImportCheck: true,
	}
	conf.Check("main", cell.Fset, []*ast.File{file}, info)

	tv, ok := info.Types[exprStmt.X]
	if !ok || tv.Type == nil {
		return nil, fmt.Errorf("could not resolve a type for the expression before '.'")
	}

	return membersOf(tv.Type), nil
}

// membersOf lists a type's field and method names -- both, and undiscriminated by
// exported/unexported, since every cell in a gocell session lives in the same "main"
// package, so Go's normal cross-package export rule never applies here.
func membersOf(t types.Type) []string {
	seen := make(map[string]bool)
	var names []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}

	// types.NewMethodSet already implements Go's own pointer-vs-value receiver rules (a
	// value type only has its value-receiver methods; *T additionally has T's), so this
	// also naturally includes promoted methods from embedded fields.
	ms := types.NewMethodSet(t)
	for i := 0; i < ms.Len(); i++ {
		add(ms.At(i).Obj().Name())
	}

	underlying := t.Underlying()
	if ptr, ok := underlying.(*types.Pointer); ok {
		underlying = ptr.Elem().Underlying()
	}
	if st, ok := underlying.(*types.Struct); ok {
		for i := 0; i < st.NumFields(); i++ {
			add(st.Field(i).Name())
		}
	}

	sort.Strings(names)
	return names
}
