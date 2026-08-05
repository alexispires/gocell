package compiler

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"gosk/pkg/runtime"
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
const analysisFuncName = "__gosk_analysis"

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
func AnalyzeCell(cell *CellContent, reg *runtime.Registry, importTracker *ImportTracker, typeRegistry *runtime.TypeRegistry) *AnalysisResult {
	existing := reg.AllSymbols()

	// Must run before type-checking: it changes how the cell's own statements resolve.
	rewriteTopLevelRedefinitions(cell, existing)

	if res, ok := analyzeWithTypeChecker(cell, existing, importTracker, typeRegistry); ok {
		return res
	}
	return analyzeByName(cell, existing)
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
// out of go/types. It reports false if that source could not even be parsed, so the caller
// can fall back to name matching rather than return a wrong (empty) analysis.
func analyzeWithTypeChecker(
	cell *CellContent,
	existing map[string]*runtime.Symbol,
	importTracker *ImportTracker,
	typeRegistry *runtime.TypeRegistry,
) (result *AnalysisResult, ok bool) {
	// go/types is being fed synthesized source built from a partially-known state; a panic
	// in the checker must degrade to the name-matching fallback, never take down the kernel.
	defer func() {
		if r := recover(); r != nil {
			result, ok = nil, false
		}
	}()

	candidates := sortedSymbolNames(existing)
	src := buildAnalysisSource(cell, candidates, existing, importTracker, typeRegistry)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gosk_analysis.go", src, 0)
	if err != nil {
		return nil, false
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
	conf.Check("main", fset, []*ast.File{file}, info)

	fn := findFuncDecl(file, analysisFuncName)
	if fn == nil || fn.Body == nil || len(fn.Body.List) < len(candidates) {
		return nil, false
	}
	// The function scope holds both the hydration candidates and the cell's own top-level
	// declarations -- go/types gives a function body no scope of its own (Scopes[fn.Body] is
	// nil), so Scopes[fn.Type] is what "top level of the generated Execute()" means here.
	funcScope := info.Scopes[fn.Type]
	if funcScope == nil {
		return nil, false
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

	return res, true
}

// buildAnalysisSource renders the cell as a self-contained file for type-checking: the
// session's accumulated declarations, then the Registry symbols as locals, then the cell's
// statements -- the same shape GeneratePluginCode will emit, minus the Registry plumbing.
func buildAnalysisSource(
	cell *CellContent,
	candidates []string,
	existing map[string]*runtime.Symbol,
	importTracker *ImportTracker,
	typeRegistry *runtime.TypeRegistry,
) string {
	fset := token.NewFileSet()

	// This cell's own declarations win over a same-named one from an earlier cell, matching
	// how GeneratePluginCode registers them moments later.
	decls := typeRegistry.AllTypes()
	for _, decl := range cellDeclarations(cell) {
		decls[decl.key] = decl.code
	}

	var body strings.Builder
	for _, key := range sortedKeys(decls) {
		body.WriteString(decls[key])
		body.WriteString("\n\n")
	}

	body.WriteString("func " + analysisFuncName + "() {\n")
	for _, name := range candidates {
		fmt.Fprintf(&body, "\tvar %s %s\n", name, registrySymbolType(existing[name]))
	}
	for _, stmt := range cell.Stmts {
		var buf strings.Builder
		if err := printer.Fprint(&buf, fset, stmt); err != nil {
			continue
		}
		for _, line := range strings.Split(buf.String(), "\n") {
			body.WriteString("\t" + line + "\n")
		}
	}
	body.WriteString("}\n")

	// A private tracker so the cell's own imports are visible here without mutating session
	// state that GeneratePluginCode is about to update itself.
	tracker := NewImportTracker()
	for path, spec := range importTracker.AllImports() {
		tracker.AddImport(spec.Alias, spec.Path)
		_ = path
	}
	for _, imp := range cell.Imports {
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		tracker.AddImport(alias, imp.Path.Value)
	}

	var sb strings.Builder
	sb.WriteString("package main\n\n")
	sb.WriteString(tracker.GenerateImportBlockForCode(body.String()))
	sb.WriteString("\n")
	sb.WriteString(body.String())
	return sb.String()
}

// analyzeByName is the degraded path used only when the throwaway source cannot be parsed or
// the type-checker panics. It resolves by name alone, so it inherits the ambiguity described
// on AnalyzeCell; it errs toward hydrating, which keeps a cell that would otherwise lose its
// state compiling in the common case.
func analyzeByName(cell *CellContent, existing map[string]*runtime.Symbol) *AnalysisResult {
	res := &AnalysisResult{
		UsedSymbols:  make(map[string]*runtime.Symbol),
		NewVariables: make([]string, 0),
	}
	exported := make(map[string]bool)

	declareName := func(name string) {
		if name == "_" {
			return
		}
		if sym, exists := existing[name]; exists {
			res.UsedSymbols[name] = sym
		} else if !exported[name] {
			exported[name] = true
			res.NewVariables = append(res.NewVariables, name)
		}
	}

	for _, stmt := range cell.Stmts {
		switch node := stmt.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					declareName(ident.Name)
				}
			}
		case *ast.DeclStmt:
			if genDecl, ok := node.Decl.(*ast.GenDecl); ok && genDecl.Tok == token.VAR {
				for _, spec := range genDecl.Specs {
					if valueSpec, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range valueSpec.Names {
							declareName(name.Name)
						}
					}
				}
			}
		}

		ast.Inspect(stmt, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				if sym, exists := existing[ident.Name]; exists {
					res.UsedSymbols[ident.Name] = sym
				}
			}
			return true
		})
	}

	return res
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
