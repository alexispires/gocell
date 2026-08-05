package compiler

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/ast/astutil"

	"gocell/pkg/runtime"
)

// AnalysisResult holds the results of analyzing a cell.
type AnalysisResult struct {
	UsedSymbols  map[string]*runtime.Symbol
	NewVariables []string
}

// analysisFuncName wraps the cell's statements in the throwaway source handed to go/types.
// That source mirrors the real generated Execute(): the Registry symbols are declared as
// locals at the top of this function, so Go's own scoping rules -- shadowing, `:=`
// redeclaration of an existing local, closure capture -- resolve exactly as they will in the
// compiled plugin.
const analysisFuncName = "__gocell_analysis"

// AnalyzeCell decides, for every symbol the cell touches, whether it refers to a variable
// already in the Registry (to hydrate, and for value types to write back) or declares a new
// top-level one (to export).
//
// It answers that by type-checking a throwaway copy of the cell rather than by matching
// identifier names: an identifier only counts as referring to a Registry symbol when
// go/types resolves it to that exact object. Name matching cannot make that call -- an
// identifier may coincide with a Registry symbol's name while referring to something else
// entirely (a range variable, a closure parameter, a `:=` nested in a block, a struct
// literal's field key, a label), and hydrating a symbol that the generated code never really
// references fails to compile ("declared and not used") for pointer-typed symbols, which
// have no write-back to otherwise reference them.
// It returns an error only if the cell could not be analyzed at all, which leaves no honest
// answer to give: guessing by name would resurrect exactly the ambiguity described above, and
// an empty analysis would silently drop the session's state from the generated code.
func AnalyzeCell(cell *CellContent, reg *runtime.Registry, importTracker *ImportTracker, typeRegistry *runtime.TypeRegistry) (*AnalysisResult, error) {
	existing := reg.AllSymbols()

	// Must run before type-checking: it changes how the cell's own statements resolve.
	rewriteTopLevelRedefinitions(cell, existing)

	return analyzeWithTypeChecker(cell, existing, importTracker, typeRegistry)
}

// rewriteTopLevelRedefinitions turns a top-level `:=` into `=` when every name on its left
// already exists in the Registry, so re-running a cell doesn't hit Go's "no new variables on
// left side of :=". A `:=` with at least one genuinely new name is left alone -- Go reuses
// the existing locals and declares only the new ones, which is exactly what we want.
func rewriteTopLevelRedefinitions(cell *CellContent, existing map[string]*runtime.Symbol) {
	for _, stmt := range cell.Stmts {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || assign.Tok != token.DEFINE {
			continue
		}

		allExist := true
		for _, lhs := range assign.Lhs {
			if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
				if _, exists := existing[ident.Name]; !exists {
					allExist = false
					break
				}
			}
		}
		if allExist {
			assign.Tok = token.ASSIGN
		}
	}
}

// analyzeWithTypeChecker builds and type-checks the throwaway source, then reads the answer
// out of go/types.
func analyzeWithTypeChecker(
	cell *CellContent,
	existing map[string]*runtime.Symbol,
	importTracker *ImportTracker,
	typeRegistry *runtime.TypeRegistry,
) (result *AnalysisResult, err error) {
	// go/types is being fed source synthesized from a partially-known session state; a panic
	// in the checker must surface as a failed cell, never take down the kernel.
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, fmt.Errorf("type-checking the cell panicked: %v", r)
		}
	}()

	candidates := sortedSymbolNames(existing)
	file, buildErr := buildAnalysisFile(cell, candidates, existing, importTracker, typeRegistry)
	if buildErr != nil {
		return nil, buildErr
	}

	info := &types.Info{
		Uses:   make(map[*ast.Ident]types.Object),
		Defs:   make(map[*ast.Ident]types.Object),
		Scopes: make(map[ast.Node]*types.Scope),
	}
	conf := types.Config{
		// Tolerant: the cell may well not type-check as a whole (an unresolvable third-party
		// import, a type error the user is about to see from the real compiler anyway).
		// Collecting errors instead of bailing keeps identifier resolution, which is all we
		// read here, available for the parts that are sound.
		Error:                    func(error) {},
		Importer:                 importer.Default(),
		DisableUnusedImportCheck: true,
	}
	conf.Check("main", cell.Fset, []*ast.File{file}, info)

	fn := findFuncDecl(file, analysisFuncName)
	if fn == nil || fn.Body == nil || len(fn.Body.List) < len(candidates) {
		return nil, fmt.Errorf("could not analyze the cell: %s is missing from the analyzed source", analysisFuncName)
	}
	// The function scope holds both the hydration candidates and the cell's own top-level
	// declarations -- go/types gives a function body no scope of its own (Scopes[fn.Body] is
	// nil), so Scopes[fn.Type] is what "top level of the generated Execute()" means here.
	funcScope := info.Scopes[fn.Type]
	if funcScope == nil {
		return nil, fmt.Errorf("could not analyze the cell: no scope was resolved for %s", analysisFuncName)
	}

	candidateObjs := make(map[types.Object]string, len(candidates))
	for _, name := range candidates {
		if obj := funcScope.Lookup(name); obj != nil {
			candidateObjs[obj] = name
		}
	}

	res := &AnalysisResult{
		UsedSymbols:  make(map[string]*runtime.Symbol),
		NewVariables: make([]string, 0),
	}
	exported := make(map[string]bool)

	// Skip the candidate declarations this analysis prepended; only the cell's own
	// statements say anything about what the cell actually uses.
	for _, stmt := range fn.Body.List[len(candidates):] {
		ast.Inspect(stmt, func(n ast.Node) bool {
			ident, isIdent := n.(*ast.Ident)
			if !isIdent {
				return true
			}

			if obj := info.Uses[ident]; obj != nil {
				if name, isCandidate := candidateObjs[obj]; isCandidate {
					res.UsedSymbols[name] = existing[name]
				}
				return true
			}

			// A declaration. Only a variable declared at the function's own top level
			// becomes a new Registry symbol: one nested in a block goes out of scope before
			// the export code runs, and a *types.Const cannot have its address taken at all.
			obj := info.Defs[ident]
			if obj == nil || ident.Name == "_" {
				return true
			}
			if _, isVar := obj.(*types.Var); !isVar {
				return true
			}
			if obj.Parent() != funcScope {
				return true
			}
			if _, isCandidate := candidateObjs[obj]; isCandidate {
				return true
			}
			if !exported[ident.Name] {
				exported[ident.Name] = true
				res.NewVariables = append(res.NewVariables, ident.Name)
			}
			return true
		})
	}

	return res, nil
}

// rewriteUsedSymbolAccess rewrites every occurrence of a used symbol `x` within stmts to
// `(*x_ptr)`, the read/write target once GeneratePluginCode declares `x_ptr := (*T)(ctx.GetPointer("x"))`
// instead of hydrating a local copy. It reuses the exact same info.Uses/candidateObjs matching
// the UsedSymbols-detection loop above already relies on, so shadowing, field selectors
// (`foo.Bar` -- `Bar` is never in info.Uses pointing at a candidate), and struct-literal keys
// are excluded by construction, with no special-casing: an identifier is only rewritten when
// go/types itself resolved it to the exact candidate object. go/printer's own precedence-aware
// parenthesization handles every syntactic context (selector, call, address-of, deref)
// automatically once the rewritten tree is printed -- `&x` becomes `&(*x_ptr)`, `x.Field`
// becomes `(*x_ptr).Field`, and so on, with no per-context logic needed here.
//
// Not yet called from analyzeWithTypeChecker (infrastructure only at this point) -- wiring it
// in requires GeneratePluginCode to also stop hydrating a copy and start declaring `x_ptr`,
// which is a later, single atomic change (both sides of the contract have to move together).
func rewriteUsedSymbolAccess(stmts []ast.Stmt, info *types.Info, candidateObjs map[types.Object]string) {
	pre := func(c *astutil.Cursor) bool {
		ident, ok := c.Node().(*ast.Ident)
		if !ok {
			return true
		}
		obj := info.Uses[ident]
		if obj == nil {
			return true // a Defs entry (declares something new), a struct-literal field key,
			// or resolves to something else entirely -- none of these are the candidate itself.
		}
		name, isCandidate := candidateObjs[obj]
		if !isCandidate {
			return true // not a Registry symbol (e.g. a shadow -- a different object)
		}
		c.Replace(&ast.StarExpr{X: ast.NewIdent(name + "_ptr")})
		return false
	}
	for i, stmt := range stmts {
		stmts[i] = astutil.Apply(stmt, pre, nil).(ast.Stmt)
	}
}

// buildAnalysisFile constructs the throwaway function go/types type-checks: the session's
// accumulated declarations, then the Registry symbols as locals, then -- spliced in directly
// as the cell's own real *ast.Stmt nodes, not reparsed from printed text -- the cell's
// statements. Splicing (rather than the print-then-reparse this replaced) means info.Uses/
// info.Defs, once Check runs, key off the exact same nodes GeneratePluginCode will later
// print, which the rewrite pass (rewriteUsedSymbolAccess) needs to mutate them in place.
//
// This is a model of the file GeneratePluginCode will emit, and only one property of that
// file has to be modelled faithfully: every name the cell references must resolve here to
// what it will resolve to there. That is why the Registry symbols are declared *inside* the
// function rather than at package level -- Go's `:=` only reuses a variable declared in the
// same block, so package-level candidates would make `x, fresh := 1, 2` shadow x here while
// reusing it in the real Execute(), and the analysis would describe a program nobody
// compiles.
//
// The two files deliberately differ everywhere else: this one has no hydration bodies, no
// write-back, no export, and declares every Registry symbol rather than just the used ones
// (harmless -- declaring names the cell never references cannot change how the ones it does
// reference resolve). So this is a semantic coupling, not shared code to factor out.
// Changing the scope structure of Execute() -- moving hydration into a nested block, wrapping
// the cell's statements -- breaks that coupling, and shadowing_test.go is what catches it:
// those cells only compile if both files agree on what shadows what.
//
// Candidate declarations are parsed from a source positioned, in cell.Fset, after cell.Stmts'
// own file (cell.Stmts was parsed first, back in ParseCell) -- verified empirically that this
// does not confuse go/types' resolution: unlike Scope.LookupParent called with an external
// position, Check's own internal resolution is driven by AST structural order within each
// block, not raw cross-file Pos() comparison, so no position-clearing workaround is needed.
func buildAnalysisFile(
	cell *CellContent,
	candidates []string,
	existing map[string]*runtime.Symbol,
	importTracker *ImportTracker,
	typeRegistry *runtime.TypeRegistry,
) (*ast.File, error) {
	// This cell's own declarations win over a same-named one from an earlier cell, matching
	// how GeneratePluginCode registers them moments later.
	decls := typeRegistry.AllTypes()
	for _, decl := range cellDeclarations(cell) {
		decls[decl.key] = decl.code
	}

	var declBody strings.Builder
	for _, key := range sortedKeys(decls) {
		declBody.WriteString(decls[key])
		declBody.WriteString("\n\n")
	}
	declBody.WriteString("func " + analysisFuncName + "() {}\n")

	// A private tracker so the cell's own imports are visible here without mutating session
	// state that GeneratePluginCode is about to update itself.
	tracker := NewImportTracker()
	for _, spec := range importTracker.AllImports() {
		tracker.AddImport(spec.Alias, spec.Path)
	}
	for _, imp := range cell.Imports {
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		tracker.AddImport(alias, imp.Path.Value)
	}

	var src strings.Builder
	src.WriteString("package main\n\n")
	src.WriteString(tracker.GenerateImportBlockForCode(declBody.String()))
	src.WriteString("\n")
	src.WriteString(declBody.String())

	file, err := parser.ParseFile(cell.Fset, "gocell_analysis.go", src.String(), 0)
	if err != nil {
		return nil, fmt.Errorf("could not analyze the cell: %w", err)
	}
	fn := findFuncDecl(file, analysisFuncName)
	if fn == nil {
		return nil, fmt.Errorf("could not analyze the cell: %s is missing from the analyzed source", analysisFuncName)
	}

	var candSrc strings.Builder
	candSrc.WriteString("package main\n\nfunc __gocell_candidates() {\n")
	for _, name := range candidates {
		fmt.Fprintf(&candSrc, "\tvar %s %s\n", name, registrySymbolType(existing[name]))
	}
	candSrc.WriteString("}\n")
	candFile, err := parser.ParseFile(cell.Fset, "gocell_candidates.go", candSrc.String(), 0)
	if err != nil {
		return nil, fmt.Errorf("could not analyze the cell: %w", err)
	}
	candFn := findFuncDecl(candFile, "__gocell_candidates")
	if candFn == nil || candFn.Body == nil {
		return nil, fmt.Errorf("could not analyze the cell: candidate declarations are missing from the analyzed source")
	}

	// Splice the cell's own real statement nodes after the candidate declarations. cell.Stmts
	// holds pointers, so this shares node identity with the original slice -- no copy.
	fn.Body.List = append(candFn.Body.List, cell.Stmts...)

	return file, nil
}

func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name != nil && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func sortedSymbolNames(symbols map[string]*runtime.Symbol) []string {
	names := make([]string, 0, len(symbols))
	for name := range symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
