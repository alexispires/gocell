package compiler

import (
	"go/ast"
	"go/token"
	"gosk/pkg/runtime"
)

// AnalysisResult holds the results of analyzing a cell.
type AnalysisResult struct {
	UsedSymbols  map[string]*runtime.Symbol
	NewVariables []string
}

// AnalyzeCell analyzes declarations, mutations, and symbol usage within a cell.
func AnalyzeCell(cell *CellContent, reg *runtime.Registry) *AnalysisResult {
	res := &AnalysisResult{
		UsedSymbols:  make(map[string]*runtime.Symbol),
		NewVariables: make([]string, 0),
	}

	existingSymbols := reg.AllSymbols()
	exportedMap := make(map[string]bool)

	for _, stmt := range cell.Stmts {
		nonVariableIdents := collectNonVariableIdents(stmt)

		ast.Inspect(stmt, func(n ast.Node) bool {
			if n == nil {
				return true
			}

			switch node := n.(type) {
			case *ast.AssignStmt:
				// A statement is top-level if the AssignStmt is directly an element of cell.Stmts
				isTopLevel := (ast.Stmt(node) == stmt)

				// At the cell's top level, turn := into = if all LHS variables already exist in the Registry
				if isTopLevel && node.Tok == token.DEFINE {
					allExist := true
					for _, lhs := range node.Lhs {
						if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
							if _, exists := existingSymbols[ident.Name]; !exists {
								allExist = false
								break
							}
						}
					}
					if allExist {
						node.Tok = token.ASSIGN
					}
				}

				// A `:=` nested inside a block always declares a fresh, shadowed local:
				// Go's scoping never lets a nested `:=` reuse a binding from the function's
				// top scope (where hydration happens), so unlike a top-level `:=`, its LHS
				// names must never be linked back to an existing Registry symbol. (See
				// collectNonVariableIdents, which also excludes every later reference to a
				// shadowed name for the rest of its scope, not just this declaration site.)
				if node.Tok == token.DEFINE && !isTopLevel {
					return true
				}

				// Capture LHS identifiers
				for _, lhs := range node.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
						if sym, exists := existingSymbols[ident.Name]; exists {
							res.UsedSymbols[ident.Name] = sym
						} else if isTopLevel && !exportedMap[ident.Name] {
							exportedMap[ident.Name] = true
							res.NewVariables = append(res.NewVariables, ident.Name)
						}
					}
				}

			case *ast.DeclStmt:
				// A var/const declaration only needs Registry involvement (hydration,
				// export) when it is itself the cell's top-level statement. One nested
				// inside a block (e.g. `var sum float64` inside a for loop) declares an
				// ordinary function-local variable that Go's own scoping already handles
				// correctly; treating it as a new top-level symbol would make the
				// generator emit an export block referencing it outside of its scope,
				// which fails to compile ("undefined: sum").
				isTopLevel := (ast.Stmt(node) == stmt)
				if !isTopLevel {
					return true
				}

				genDecl, ok := node.Decl.(*ast.GenDecl)
				if !ok {
					return true
				}
				for _, spec := range genDecl.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, name := range valueSpec.Names {
						if name.Name == "_" {
							continue
						}
						if sym, exists := existingSymbols[name.Name]; exists {
							res.UsedSymbols[name.Name] = sym
						} else if !exportedMap[name.Name] {
							exportedMap[name.Name] = true
							res.NewVariables = append(res.NewVariables, name.Name)
						}
					}
				}

			case *ast.Ident:
				if nonVariableIdents[node] {
					return true
				}
				if sym, exists := existingSymbols[node.Name]; exists {
					res.UsedSymbols[node.Name] = sym
				}
			}

			return true
		})
	}

	return res
}

// collectNonVariableIdents finds every *ast.Ident node that the generic *ast.Ident case in
// AnalyzeCell must never resolve against an existing Registry symbol. Two distinct reasons
// land an identifier here:
//
//  1. It's never a variable reference at all, syntactically: a struct composite literal
//     field name, a selector (.Sel), a label.
//  2. It names a fresh, block-scoped binding -- a range variable, a closure parameter, a
//     `:=`/`var`/`const` nested inside a block -- that shadows any outer symbol of the same
//     name for the rest of its scope. This covers not just the declaration site but every
//     later reference to that name within the shadow's scope, via a proper (if intentionally
//     partial) sequential walk of Go's block scoping rules: a shadow only takes effect from
//     its declaration onward within its block, and a reference to the same name earlier in
//     that block still (correctly) resolves to the outer symbol.
//
// Without this, an identifier that happens to share its name with an existing symbol would
// be wrongly captured as "used", which can produce a hydrated variable that is never actually
// referenced anywhere in the generated code -- and fails to compile ("declared and not used")
// whenever that symbol is a pointer type, the one case with no write-back to reference it.
func collectNonVariableIdents(stmt ast.Stmt) map[*ast.Ident]bool {
	excluded := make(map[*ast.Ident]bool)
	shadowed := make(map[string]int)

	isShadowed := func(name string) bool {
		return shadowed[name] > 0
	}
	push := func(names []string) {
		for _, name := range names {
			if name != "" && name != "_" {
				shadowed[name]++
			}
		}
	}
	pop := func(names []string) {
		for _, name := range names {
			if name != "" && name != "_" {
				shadowed[name]--
			}
		}
	}
	excludeIdent := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok {
			excluded[id] = true
		}
	}
	identNames := func(exprs ...ast.Expr) []string {
		var names []string
		for _, e := range exprs {
			if id, ok := e.(*ast.Ident); ok {
				names = append(names, id.Name)
			}
		}
		return names
	}
	fieldListNames := func(fl *ast.FieldList) []string {
		var names []string
		if fl == nil {
			return names
		}
		for _, field := range fl.List {
			for _, name := range field.Names {
				names = append(names, name.Name)
				excluded[name] = true // the parameter/result name itself is a declaration, never a reference
			}
		}
		return names
	}

	// walk descends the AST in source order, pushing/popping shadowed names around exactly
	// the sub-nodes they're actually in scope for, instead of ast.Inspect's flat, order-blind
	// traversal -- which is what lets a `:=` mid-block correctly leave earlier references to
	// the same name alone while still catching every later one.
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}

		switch node := n.(type) {
		case *ast.Ident:
			if isShadowed(node.Name) {
				excluded[node] = true
			}

		case *ast.SelectorExpr:
			walk(node.X)
			excluded[node.Sel] = true

		case *ast.CompositeLit:
			walk(node.Type)
			structLit := isStructLiteralType(node.Type)
			for _, elt := range node.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if structLit {
						if key, ok := kv.Key.(*ast.Ident); ok {
							excluded[key] = true
						}
					} else {
						walk(kv.Key)
					}
					walk(kv.Value)
				} else {
					walk(elt)
				}
			}

		case *ast.LabeledStmt:
			excluded[node.Label] = true
			walk(node.Stmt)

		case *ast.BranchStmt:
			if node.Label != nil {
				excluded[node.Label] = true
			}

		case *ast.AssignStmt:
			// The RHS is evaluated in the surrounding (outer) scope, so it's walked
			// normally regardless of Tok -- this is what correctly leaves e.g. `x := x + 1`
			// alone on the right while the new `x` on the left shadows from here on.
			for _, rhs := range node.Rhs {
				walk(rhs)
			}
			// A nested `:=` (not the cell's own top-level statement) always declares a
			// fresh local: its LHS names are a declaration site, never a reference to the
			// outer Registry symbol they might share a name with, so they're excluded
			// outright rather than walked. A top-level `:=`, or any `=`, still needs the
			// ordinary Ident handling below (an `=` LHS is always a genuine reference).
			if node.Tok == token.DEFINE && ast.Stmt(node) != stmt {
				for _, lhs := range node.Lhs {
					excludeIdent(lhs)
				}
			} else {
				for _, lhs := range node.Lhs {
					walk(lhs)
				}
			}

		case *ast.DeclStmt:
			// Same reasoning as the nested `:=` case above, for `var`/`const`: only the
			// names are ever a declaration site; any initializer values are evaluated in
			// the outer scope.
			if genDecl, ok := node.Decl.(*ast.GenDecl); ok {
				for _, spec := range genDecl.Specs {
					if valueSpec, ok := spec.(*ast.ValueSpec); ok {
						for _, value := range valueSpec.Values {
							walk(value)
						}
						if ast.Stmt(node) != stmt {
							for _, name := range valueSpec.Names {
								excluded[name] = true
							}
						}
					}
				}
			}

		case *ast.BlockStmt:
			// Sequential: a `:=`/`var`/`const` only shadows from its own statement onward,
			// so process the list in order and push each new shadow only after handling the
			// statement that introduces it (its own RHS/values still see the outer scope).
			var introduced []string
			for _, s := range node.List {
				walk(s)
				names := namesIntroducedByStmt(s)
				push(names)
				introduced = append(introduced, names...)
			}
			pop(introduced)

		case *ast.FuncLit:
			names := append(fieldListNames(node.Type.Params), fieldListNames(node.Type.Results)...)
			push(names)
			walk(node.Body)
			pop(names)

		case *ast.ForStmt:
			names := namesIntroducedByStmt(node.Init)
			push(names)
			if node.Init != nil {
				walk(node.Init)
			}
			walk(node.Cond)
			walk(node.Post)
			walk(node.Body)
			pop(names)

		case *ast.RangeStmt:
			walk(node.X)
			var names []string
			if node.Tok == token.DEFINE {
				names = identNames(node.Key, node.Value)
				excludeIdent(node.Key)
				excludeIdent(node.Value)
			}
			push(names)
			walk(node.Body)
			pop(names)

		case *ast.IfStmt:
			names := namesIntroducedByStmt(node.Init)
			push(names)
			if node.Init != nil {
				walk(node.Init)
			}
			walk(node.Cond)
			walk(node.Body)
			walk(node.Else)
			pop(names)

		case *ast.SwitchStmt:
			names := namesIntroducedByStmt(node.Init)
			push(names)
			if node.Init != nil {
				walk(node.Init)
			}
			walk(node.Tag)
			walk(node.Body)
			pop(names)

		case *ast.TypeSwitchStmt:
			names := namesIntroducedByStmt(node.Init)
			push(names)
			if node.Init != nil {
				walk(node.Init)
			}
			walk(node.Assign)
			walk(node.Body)
			pop(names)

		case *ast.CommClause:
			names := namesIntroducedByStmt(node.Comm)
			push(names)
			if node.Comm != nil {
				walk(node.Comm)
			}
			for _, s := range node.Body {
				walk(s)
			}
			pop(names)

		default:
			genericWalkChildren(n, walk)
		}
	}

	walk(stmt)
	return excluded
}

// namesIntroducedByStmt returns the identifier names a `:=` (AssignStmt) or `var`/`const`
// (DeclStmt) statement introduces, without walking it -- the caller walks it separately (in
// the outer scope, before the names it introduces become visible) and pushes these names
// only afterward. Any other statement kind introduces nothing.
func namesIntroducedByStmt(s ast.Stmt) []string {
	var names []string
	switch node := s.(type) {
	case *ast.AssignStmt:
		if node.Tok == token.DEFINE {
			for _, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name != "_" {
					names = append(names, id.Name)
				}
			}
		}
	case *ast.DeclStmt:
		if genDecl, ok := node.Decl.(*ast.GenDecl); ok {
			for _, spec := range genDecl.Specs {
				if valueSpec, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range valueSpec.Names {
						if name.Name != "_" {
							names = append(names, name.Name)
						}
					}
				}
			}
		}
	}
	return names
}

// genericWalkChildren descends into the direct children of n using go/ast's own generic
// child enumeration, so every expression and statement type not given bespoke scoping
// treatment above (CallExpr, BinaryExpr, ReturnStmt, ...) is still covered without having to
// hand-list each one. Each direct child is then handed back to walk, which dispatches it
// through the same switch -- including this same default case again, one level down -- so
// nothing needs to be visited twice.
func genericWalkChildren(n ast.Node, walk func(ast.Node)) {
	ast.Inspect(n, func(child ast.Node) bool {
		if child == n {
			return true // descend from n into its direct children
		}
		walk(child)
		return false // walk(child) already handles child's own subtree; don't double-visit it
	})
}

// isStructLiteralType distinguishes a named struct composite literal (Point{...}, pkg.Point{...}),
// where keys are field names, from a map or array/slice literal, where keys are real
// expressions that may reference a variable.
func isStructLiteralType(t ast.Expr) bool {
	switch t.(type) {
	case nil, *ast.MapType, *ast.ArrayType:
		return false
	default:
		return true
	}
}
