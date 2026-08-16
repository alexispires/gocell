package compiler

import (
	"fmt"
	"github.com/alexispires/gocell/pkg/runtime"
	"go/ast"
	"go/printer"
	"go/token"
	"sort"
	"strings"
	"text/template"

	"golang.org/x/tools/imports"
)

// exportVarTemplate generates the block that exports a new variable to the shared Registry:
// the address of the cell's own local, with KeepAlive on that same address so the GC never
// frees the memory referenced by the raw pointer stored in the Registry. This must be x's own
// storage, not a copy -- a closure declared in the same cell that already captured &x (to
// mutate x) needs the Registry to keep pointing at that exact memory, or a later cell's read
// through the Registry and the closure's own mutation would silently diverge onto two
// different boxes holding two different values.
//
// The type name recorded is `%T` of &x, not of x itself: `%T` of x directly reports x's
// *dynamic* type, which is wrong whenever x's *static* declared type is an interface (its own
// storage is a two-word interface header, not shaped like whatever concrete value it holds) --
// reading it back through that dynamic type would reinterpret the header's bytes as if they
// were the dynamic value. `%T` of &x is always exactly "*" followed by x's own static type,
// spelled out, regardless of what x's dynamic value happens to be -- registrySymbolType strips
// that one leading "*" back off.
var exportVarTemplate = template.Must(template.New("exportVar").Parse(`	ptr_{{.}} := unsafe.Pointer(&{{.}})
	keepAlive_{{.}} := &{{.}}
	ctx.SetPointer({{printf "%q" .}}, fmt.Sprintf("%T", &{{.}}), ptr_{{.}}, keepAlive_{{.}})
`))

// LineMapping records that GeneratedLine, in the plugin source GeneratePluginCode produced,
// prints the start of the same statement that begins at OriginalLine in the cell's own source
// (cell.RawCode). Only top-level cell statements are covered -- not re-injected declarations
// from earlier cells, whose position in the generated file doesn't correspond to any single
// cell's line numbers at all.
type LineMapping struct {
	GeneratedLine int
	OriginalLine  int

	// InjectedAtOriginalLines holds the original (pre-injection) line, within this statement,
	// of every loop injectInterruptChecks added a check to -- sorted ascending. Each one added
	// exactly 3 generated lines that don't exist in the cell's own source, so
	// remapPanicError's otherwise-uniform generated->original interpolation needs to subtract
	// 3 for every entry at or before the line it's resolving.
	InjectedAtOriginalLines []int
}

// GeneratePluginCode generates the complete Go source of a cell plugin for compilation, along
// with a mapping from generated-file line numbers back to the cell's own source lines (used to
// report a panic's location in terms the user actually typed, not the generated file's).
func GeneratePluginCode(
	cell *CellContent,
	analysis *AnalysisResult,
	importTracker *ImportTracker,
	typeRegistry *runtime.TypeRegistry,
) (string, []LineMapping) {
	for _, imp := range cell.Imports {
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		importTracker.AddImport(alias, imp.Path.Value)
	}

	importTracker.AddImport("", `"reflect"`)

	// 1. Register type and function definitions
	fset := token.NewFileSet()
	for _, decl := range cellDeclarations(cell) {
		typeRegistry.RegisterType(decl.key, decl.code)
	}

	var bodySb strings.Builder

	// __gocell_ctx makes the Context reachable from any function in this generated file,
	// including ones the cell itself declared (re-injected into later cells as plain source
	// text, with no ctx parameter of their own) -- injectInterruptChecks relies on this to
	// place a working check inside a cell-declared function's loops, not just cell.Stmts'.
	bodySb.WriteString("var __gocell_ctx *runtime.Context\n\n")

	// Re-inject declarations. Sorted by key: map iteration order is randomized per run, and an
	// unsorted range here would make byte-identical cell semantics hash differently between
	// runs, defeating pkg/plugin.Cache with spurious misses.
	registeredDecls := typeRegistry.AllTypes()
	if len(registeredDecls) > 0 {
		declKeys := make([]string, 0, len(registeredDecls))
		for k := range registeredDecls {
			declKeys = append(declKeys, k)
		}
		sort.Strings(declKeys)

		bodySb.WriteString("// --- Re-injected declarations (types, functions, methods) ---\n")
		for _, k := range declKeys {
			bodySb.WriteString(registeredDecls[k])
			bodySb.WriteString("\n\n")
		}
	}

	// anchorRelLine records this comment's own line number within bodySb (same 1-indexed
	// scheme as relLine below), so it can be relocated in the goimports-formatted output
	// afterward -- see the comment above the imports.Process call near the end of this
	// function for why that relocation is necessary.
	anchorRelLine := strings.Count(bodySb.String(), "\n") + 1
	const anchorComment = "// --- Plugin Execute entry point ---"
	bodySb.WriteString(anchorComment + "\n")
	bodySb.WriteString("func Execute(ctx *runtime.Context) error {\n")
	bodySb.WriteString("\t__gocell_ctx = ctx\n")
	bodySb.WriteString("\t_ctx := ctx.StdContext()\n")
	bodySb.WriteString("\t_ = _ctx // available to the cell even if this cell doesn't reference it\n")

	if len(analysis.UsedSymbols) > 0 {
		usedNames := make([]string, 0, len(analysis.UsedSymbols))
		for name := range analysis.UsedSymbols {
			usedNames = append(usedNames, name)
		}
		sort.Strings(usedNames)

		bodySb.WriteString("\t// Point directly at existing symbols -- no copy, no write-back\n")
		for _, name := range usedNames {
			cleanTypeName := registrySymbolType(analysis.UsedSymbols[name])
			fmt.Fprintf(&bodySb, "\t%s_ptr := (*%s)(ctx.GetPointer(%q))\n", name, cleanTypeName, name)
		}
		bodySb.WriteString("\n")
	}

	bodySb.WriteString("\t// Cell statements (used symbols already rewritten to go through their _ptr)\n")

	// relLine tracks the current line number within bodySb (1-indexed), so each statement's
	// generated start line can be recorded before any of its own text is written.
	relLine := strings.Count(bodySb.String(), "\n") + 1
	var mappings []LineMapping

	for i, stmt := range cell.Stmts {
		isLast := i == len(cell.Stmts)-1

		if cell.Fset != nil {
			// stmt.Pos() is relative to the synthetic wrapper ParseCell parsed the cell's
			// statements from (see stmtWrapperPreamble) -- undo that preamble's line count to
			// get back to the line the user actually typed. Injected-interrupt-check lines
			// (recorded in the same raw coordinate system by injectInterruptChecks) need the
			// same adjustment to compare correctly against OriginalLine later.
			var injectedLines []int
			if i < len(analysis.InjectedInterruptLines) {
				for _, l := range analysis.InjectedInterruptLines[i] {
					injectedLines = append(injectedLines, l-stmtWrapperPreambleLines)
				}
			}
			mappings = append(mappings, LineMapping{
				GeneratedLine:           relLine,
				OriginalLine:            cell.Fset.Position(stmt.Pos()).Line - stmtWrapperPreambleLines,
				InjectedAtOriginalLines: injectedLines,
			})
		}

		// The last statement, if it is a bare expression that would not already be a valid
		// standalone Go statement (i.e. not a function/method call), is turned into a
		// REPL-style display instead of failing to compile with "evaluated but not used".
		if isLast {
			if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
				// A bare call is already a valid statement and is left alone; a conversion looks
				// identical in the AST but is not, so the analyzer's go/types verdict decides.
				_, isCall := exprStmt.X.(*ast.CallExpr)
				if !isCall || analysis.LastExprIsConversion {
					var exprBuf strings.Builder
					if err := printer.Fprint(&exprBuf, fset, exprStmt.X); err == nil {
						// The expression itself, not a rendering of it: SetAutoResult picks between a
						// rich representation and %#v at run time, keeping that choice out of codegen.
						line := fmt.Sprintf("\tctx.SetAutoResult(%s)\n", exprBuf.String())
						bodySb.WriteString(line)
						relLine += strings.Count(line, "\n")
						continue
					}
				}
			}
		}

		var buf strings.Builder
		if err := printer.Fprint(&buf, fset, stmt); err == nil {
			lines := strings.Split(buf.String(), "\n")
			for _, line := range lines {
				bodySb.WriteString("\t" + line + "\n")
				relLine++
			}
		}
	}

	if len(analysis.NewVariables) > 0 {
		bodySb.WriteString("\n\t// Export new symbols to the Registry (direct pointer vs heap copy)\n")
		for _, vName := range analysis.NewVariables {
			var buf strings.Builder
			if err := exportVarTemplate.Execute(&buf, vName); err == nil {
				bodySb.WriteString(buf.String())
			}
		}
	}

	bodySb.WriteString("\n\treturn nil\n")
	bodySb.WriteString("}\n")

	bodyCodeStr := bodySb.String()

	var sb strings.Builder
	sb.WriteString("package main\n\n")
	sb.WriteString(importTracker.GenerateImportBlockForCode(bodyCodeStr))
	sb.WriteString("\n")

	// Anti-unused import guards
	sb.WriteString("// Anti-unused import guards\n")
	sb.WriteString("var (\n")
	sb.WriteString("\t_ = fmt.Sprintf\n")
	sb.WriteString("\t_ = unsafe.Pointer(nil)\n")
	sb.WriteString("\t_ = strings.HasPrefix\n")
	sb.WriteString("\t_ = reflect.ValueOf\n")
	sb.WriteString(")\n\n")

	prefixLines := strings.Count(sb.String(), "\n")
	sb.WriteString(bodyCodeStr)

	for i := range mappings {
		mappings[i].GeneratedLine += prefixLines
	}

	rawSource := sb.String()

	// goimports groups a domain-shaped import path (github.com/...) away from the stdlib
	// imports above it, inserting a blank line that isn't present in rawSource -- silently
	// shifting every line below it by one in the file that actually gets compiled. Formatting
	// here, before returning, and correcting `mappings` by relocating anchorComment in the
	// formatted output keeps GeneratedLine accurate regardless of how much the import block
	// grows or shrinks; BuildPlugin then compiles this already-formatted source as-is.
	formatted, err := imports.Process("main.go", []byte(rawSource), &imports.Options{
		Comments:   true,
		TabIndent:  true,
		TabWidth:   8,
		FormatOnly: false,
	})
	if err != nil {
		return rawSource, mappings
	}

	finalSource := string(formatted)
	if idx := strings.Index(finalSource, anchorComment); idx >= 0 {
		finalAnchorLine := strings.Count(finalSource[:idx], "\n") + 1
		delta := finalAnchorLine - (anchorRelLine + prefixLines)
		for i := range mappings {
			mappings[i].GeneratedLine += delta
		}
	}

	return finalSource, mappings
}

// cellDecl is one type/function/method declaration from a cell, under the key it is
// registered as in the session's TypeRegistry (so redefining a name in a later cell replaces
// the earlier one rather than colliding with it).
type cellDecl struct {
	key  string
	code string
}

// cellDeclarations renders a cell's type and function declarations to source. Both the
// generator (which registers them for re-injection into every later cell) and the analyzer
// (which needs them for type-checking) go through this, so the two always agree on what a
// cell declares and under which key.
func cellDeclarations(cell *CellContent) []cellDecl {
	var decls []cellDecl
	fset := token.NewFileSet()

	for _, typeDecl := range cell.TypeDecls {
		var buf strings.Builder
		if err := printer.Fprint(&buf, fset, typeDecl); err != nil {
			continue
		}
		if genDecl, ok := typeDecl.(*ast.GenDecl); ok && genDecl.Tok == token.TYPE {
			for _, spec := range genDecl.Specs {
				if typeSpec, okSpec := spec.(*ast.TypeSpec); okSpec {
					decls = append(decls, cellDecl{key: "type_" + typeSpec.Name.Name, code: buf.String()})
				}
			}
		}
	}

	for _, funcDecl := range cell.FuncDecls {
		var buf strings.Builder
		if err := printer.Fprint(&buf, fset, funcDecl); err != nil {
			continue
		}
		fDecl, ok := funcDecl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fDecl.Name.Name
		if fDecl.Recv != nil && len(fDecl.Recv.List) > 0 {
			var recvBuf strings.Builder
			_ = printer.Fprint(&recvBuf, fset, fDecl.Recv.List[0].Type)
			name = fmt.Sprintf("method_%s_%s", recvBuf.String(), fDecl.Name.Name)
		}
		decls = append(decls, cellDecl{key: "func_" + name, code: buf.String()})
	}

	return decls
}

// registrySymbolType renders a Registry symbol's Go type as it should appear in generated
// source. The stored name comes from a plugin's `%T` of &x (see exportVarTemplate), which is
// always exactly x's own static type with one extra leading "*" -- stripped here -- and
// qualifies cell-declared types with the plugin's own "main" package -- a qualifier that means
// nothing in the next cell, which re-declares those types itself.
func registrySymbolType(sym *runtime.Symbol) string {
	typeName := sym.TypeName
	if typeName == "" {
		return "any"
	}
	typeName = strings.TrimPrefix(typeName, "*")
	typeName = strings.ReplaceAll(typeName, "*main.", "*")
	return strings.ReplaceAll(typeName, "main.", "")
}
