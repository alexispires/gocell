package compiler

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"gosk/pkg/runtime"
	"strings"
	"text/template"
)

// exportVarTemplate generates the block that exports a new variable to the shared Registry:
// either the pointer value directly (KeepAlive on the pointee) or the address of the cell's
// own local (KeepAlive on that same address), so the GC never frees the memory referenced by
// the raw pointer stored in the Registry. The variable is never touched again after this
// block runs (it's the last thing before `return nil`), so taking its address directly is
// safe -- no intermediate heap copy is needed, which also avoids an unnecessary full-value
// copy for large value types (a big array or struct, as opposed to a slice header).
var exportVarTemplate = template.Must(template.New("exportVar").Parse(`	var ptr_{{.}} unsafe.Pointer
	var keepAlive_{{.}} any
	val_{{.}} := reflect.ValueOf({{.}})
	if val_{{.}}.IsValid() && val_{{.}}.Kind() == reflect.Pointer {
		ptr_{{.}} = unsafe.Pointer(val_{{.}}.Pointer())
		keepAlive_{{.}} = {{.}}
	} else {
		ptr_{{.}} = unsafe.Pointer(&{{.}})
		keepAlive_{{.}} = &{{.}}
	}
	ctx.SetPointer({{printf "%q" .}}, fmt.Sprintf("%T", {{.}}), ptr_{{.}}, keepAlive_{{.}})
`))

// GeneratePluginCode generates the complete Go source of a cell plugin for compilation.
func GeneratePluginCode(
	cell *CellContent,
	analysis *AnalysisResult,
	importTracker *ImportTracker,
	typeRegistry *runtime.TypeRegistry,
) string {
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
	for _, typeDecl := range cell.TypeDecls {
		var buf strings.Builder
		if err := printer.Fprint(&buf, fset, typeDecl); err == nil {
			if genDecl, ok := typeDecl.(*ast.GenDecl); ok && genDecl.Tok == token.TYPE {
				for _, spec := range genDecl.Specs {
					if typeSpec, okSpec := spec.(*ast.TypeSpec); okSpec {
						typeRegistry.RegisterType("type_"+typeSpec.Name.Name, buf.String())
					}
				}
			}
		}
	}

	for _, funcDecl := range cell.FuncDecls {
		var buf strings.Builder
		if err := printer.Fprint(&buf, fset, funcDecl); err == nil {
			if fDecl, ok := funcDecl.(*ast.FuncDecl); ok {
				name := fDecl.Name.Name
				if fDecl.Recv != nil && len(fDecl.Recv.List) > 0 {
					var recvBuf strings.Builder
					_ = printer.Fprint(&recvBuf, fset, fDecl.Recv.List[0].Type)
					name = fmt.Sprintf("method_%s_%s", recvBuf.String(), fDecl.Name.Name)
				}
				typeRegistry.RegisterType("func_"+name, buf.String())
			}
		}
	}

	var bodySb strings.Builder

	// Re-inject declarations
	registeredDecls := typeRegistry.AllTypes()
	if len(registeredDecls) > 0 {
		bodySb.WriteString("// --- Re-injected declarations (types, functions, methods) ---\n")
		for _, declCode := range registeredDecls {
			bodySb.WriteString(declCode)
			bodySb.WriteString("\n\n")
		}
	}

	bodySb.WriteString("// --- Plugin Execute entry point ---\n")
	bodySb.WriteString("func Execute(ctx *runtime.Context) error {\n")

	if len(analysis.UsedSymbols) > 0 {
		bodySb.WriteString("\t// Hydrate existing symbols\n")
		for name, sym := range analysis.UsedSymbols {
			typeName := sym.TypeName
			if typeName == "" {
				typeName = "any"
			}
			cleanTypeName := strings.ReplaceAll(typeName, "*main.", "*")
			cleanTypeName = strings.ReplaceAll(cleanTypeName, "main.", "")

			if strings.HasPrefix(cleanTypeName, "*") {
				bodySb.WriteString(fmt.Sprintf("\tvar %s %s\n", name, cleanTypeName))
				bodySb.WriteString(fmt.Sprintf("\tif ptr := ctx.GetPointer(%q); ptr != nil {\n", name))
				bodySb.WriteString(fmt.Sprintf("\t\t%s = (%s)(ptr)\n", name, cleanTypeName))
				bodySb.WriteString("\t}\n")
			} else {
				bodySb.WriteString(fmt.Sprintf("\tvar %s %s\n", name, cleanTypeName))
				bodySb.WriteString(fmt.Sprintf("\tif ptr := ctx.GetPointer(%q); ptr != nil {\n", name))
				bodySb.WriteString(fmt.Sprintf("\t\t%s = *(*%s)(ptr)\n", name, cleanTypeName))
				bodySb.WriteString("\t}\n")
			}
		}
		bodySb.WriteString("\n")
	}

	bodySb.WriteString("\t// Cell statements\n")
	for i, stmt := range cell.Stmts {
		isLast := i == len(cell.Stmts)-1

		// The last statement, if it is a bare expression that would not already be a valid
		// standalone Go statement (i.e. not a function/method call), is turned into a
		// REPL-style display instead of failing to compile with "evaluated but not used".
		if isLast {
			if exprStmt, ok := stmt.(*ast.ExprStmt); ok {
				if _, isCall := exprStmt.X.(*ast.CallExpr); !isCall {
					var exprBuf strings.Builder
					if err := printer.Fprint(&exprBuf, fset, exprStmt.X); err == nil {
						bodySb.WriteString(fmt.Sprintf("\tctx.SetResult(fmt.Sprintf(\"%%#v\", %s))\n", exprBuf.String()))
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
			}
		}
	}

	if len(analysis.UsedSymbols) > 0 {
		bodySb.WriteString("\n\t// Write modified symbols back to the Registry heap\n")
		for name, sym := range analysis.UsedSymbols {
			typeName := sym.TypeName
			if typeName == "" {
				typeName = "any"
			}
			cleanTypeName := strings.ReplaceAll(typeName, "*main.", "*")
			cleanTypeName = strings.ReplaceAll(cleanTypeName, "main.", "")

			if !strings.HasPrefix(cleanTypeName, "*") {
				bodySb.WriteString(fmt.Sprintf("\tif ptr := ctx.GetPointer(%q); ptr != nil {\n", name))
				bodySb.WriteString(fmt.Sprintf("\t\t*(*%s)(ptr) = %s\n", cleanTypeName, name))
				bodySb.WriteString("\t}\n")
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

	sb.WriteString(bodyCodeStr)

	return sb.String()
}
