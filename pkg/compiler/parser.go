package compiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// stmtWrapperPreamble is prepended to a cell's own statement lines before parsing them (see
// ParseCell step 4), so a cell's statements can be extracted from a real, valid Go file. Every
// line here before the cell's own first statement line shifts that statement's parsed
// position accordingly -- stmtWrapperPreambleLines is that shift, in lines, kept in sync with
// this string by the test in parser_test.go. Used by GeneratePluginCode's line mapping (see
// LineMapping) to translate a cell.Stmts node's own Pos() back to the line the user actually
// typed, undoing this preamble.
const stmtWrapperPreamble = "package main\n\nfunc __gocell_wrapper() {\n"

// stmtWrapperPreambleLines is strings.Count(stmtWrapperPreamble, "\n") -- a literal since Go
// constants can't call strings.Count, kept honest by TestStmtWrapperPreambleLinesMatchesPreamble.
const stmtWrapperPreambleLines = 3

// CellContent holds the result of breaking a cell down into an AST.
type CellContent struct {
	Imports   []*ast.ImportSpec
	TypeDecls []ast.Decl
	FuncDecls []ast.Decl
	Stmts     []ast.Stmt
	RawCode   string
	// Fset is the FileSet that Imports/TypeDecls/FuncDecls/Stmts' positions are
	// relative to. AnalyzeCell reuses it (rather than reparsing the cell) so that
	// go/types resolves identifiers against these same node objects, not disconnected
	// copies -- required for AnalyzeCell's rewrite pass to mutate the real nodes that
	// GeneratePluginCode will later print.
	Fset *token.FileSet
}

// ParseCell parses a cell's code, intelligently splitting imports, types, functions and statements.
func ParseCell(code string) (*CellContent, error) {
	fset := token.NewFileSet()

	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return &CellContent{RawCode: code, Fset: fset}, nil
	}

	res := &CellContent{
		RawCode: code,
		Fset:    fset,
	}

	// 1. Split lines to separate imports, top-level declarations and statements
	lines := strings.Split(trimmed, "\n")
	var importLines []string
	var declLines []string
	var stmtLines []string

	inImport := false
	inBlockDecl := false
	var blockLines []string // lines accumulated so far for the current type/func block

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
		if !inBlockDecl && (strings.HasPrefix(t, "type ") || strings.HasPrefix(t, "func ")) {
			inBlockDecl = true
			blockLines = nil
		}

		if inBlockDecl {
			declLines = append(declLines, l)
			blockLines = append(blockLines, l)
			// Token-aware, not a raw character count: a `{`/`}` sitting inside a string,
			// rune literal, or comment on this line must not be mistaken for a real one.
			if BraceDepth(strings.Join(blockLines, "\n")) <= 0 {
				inBlockDecl = false
			}
		} else {
			stmtLines = append(stmtLines, l)
		}
	}

	// 2. Extract imports
	if len(importLines) > 0 {
		impSrc := "package main\n\n" + strings.Join(importLines, "\n")
		if impNode, errImp := parser.ParseFile(fset, "imp.go", impSrc, parser.ImportsOnly); errImp == nil {
			res.Imports = append(res.Imports, impNode.Imports...)
		}
	}

	// 3. Extract types and functions
	if len(declLines) > 0 {
		declSrc := "package main\n\n" + strings.Join(declLines, "\n")
		if declNode, errDecl := parser.ParseFile(fset, "decl.go", declSrc, parser.ParseComments); errDecl == nil {
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
		stmtSrc := stmtWrapperPreamble + strings.Join(stmtLines, "\n") + "\n}"
		if stmtNode, errStmt := parser.ParseFile(fset, "stmt.go", stmtSrc, parser.ParseComments); errStmt == nil {
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
