package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// CellContent holds the result of breaking a cell down into an AST.
type CellContent struct {
	Imports   []*ast.ImportSpec
	TypeDecls []ast.Decl
	FuncDecls []ast.Decl
	Stmts     []ast.Stmt
	RawCode   string
}

// ParseCell parses a cell's code, intelligently splitting imports, types, functions and statements.
func ParseCell(code string) (*CellContent, error) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return &CellContent{RawCode: code}, nil
	}

	res := &CellContent{
		RawCode: code,
	}

	// 1. Split lines to separate imports, top-level declarations and statements
	lines := strings.Split(trimmed, "\n")
	var importLines []string
	var declLines []string
	var stmtLines []string

	inImport := false
	inBlockDecl := false
	braceCount := 0

	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}

		// Import block
		if strings.HasPrefix(t, "import (") {
			inImport = true
			importLines = append(importLines, l)
			continue
		}
		if inImport {
			importLines = append(importLines, l)
			if t == ")" {
				inImport = false
			}
			continue
		}
		if strings.HasPrefix(t, "import ") {
			// If the line contains both an import and a statement separated by ';'
			if idx := strings.Index(t, ";"); idx != -1 {
				importLines = append(importLines, t[:idx])
				stmtLines = append(stmtLines, strings.TrimSpace(t[idx+1:]))
			} else {
				importLines = append(importLines, l)
			}
			continue
		}

		// Type or function block (e.g. type X struct, func foo())
		if strings.HasPrefix(t, "type ") || strings.HasPrefix(t, "func ") {
			inBlockDecl = true
		}

		if inBlockDecl {
			declLines = append(declLines, l)
			braceCount += strings.Count(l, "{") - strings.Count(l, "}")
			if braceCount <= 0 {
				inBlockDecl = false
				braceCount = 0
			}
		} else {
			stmtLines = append(stmtLines, l)
		}
	}

	// 2. Extract imports
	if len(importLines) > 0 {
		impSrc := "package main\n\n" + strings.Join(importLines, "\n")
		fsetImp := token.NewFileSet()
		if impNode, errImp := parser.ParseFile(fsetImp, "imp.go", impSrc, parser.ImportsOnly); errImp == nil {
			res.Imports = append(res.Imports, impNode.Imports...)
		}
	}

	// 3. Extract types and functions
	if len(declLines) > 0 {
		declSrc := "package main\n\n" + strings.Join(declLines, "\n")
		fsetDecl := token.NewFileSet()
		if declNode, errDecl := parser.ParseFile(fsetDecl, "decl.go", declSrc, parser.ParseComments); errDecl == nil {
			for _, decl := range declNode.Decls {
				switch d := decl.(type) {
				case *ast.GenDecl:
					if d.Tok == token.TYPE || d.Tok == token.VAR || d.Tok == token.CONST {
						res.TypeDecls = append(res.TypeDecls, d)
					}
				case *ast.FuncDecl:
					res.FuncDecls = append(res.FuncDecls, d)
				}
			}
		}
	}

	// 4. Extract statements
	if len(stmtLines) > 0 {
		stmtSrc := "package main\n\nfunc __gocell_wrapper() {\n" + strings.Join(stmtLines, "\n") + "\n}"
		fsetStmt := token.NewFileSet()
		if stmtNode, errStmt := parser.ParseFile(fsetStmt, "stmt.go", stmtSrc, parser.ParseComments); errStmt == nil {
			for _, decl := range stmtNode.Decls {
				if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "__gocell_wrapper" {
					if f.Body != nil {
						res.Stmts = append(res.Stmts, f.Body.List...)
					}
				}
			}
		} else {
			return nil, fmt.Errorf("statement syntax error: %w", errStmt)
		}
	}

	return res, nil
}
