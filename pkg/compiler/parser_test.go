package compiler

import (
	"strings"
	"testing"
)

// Keeps stmtWrapperPreambleLines honest against stmtWrapperPreamble: GeneratePluginCode's
// line mapping (see LineMapping) relies on this constant to translate a panicking statement's
// position back to the cell's own source line, and a silent drift between the two would make
// every reported panic line wrong by a fixed, easy-to-miss offset.
func TestStmtWrapperPreambleLinesMatchesPreamble(t *testing.T) {
	t.Parallel()

	if got := strings.Count(stmtWrapperPreamble, "\n"); got != stmtWrapperPreambleLines {
		t.Fatalf("stmtWrapperPreambleLines = %d, but stmtWrapperPreamble actually has %d newlines -- keep them in sync", stmtWrapperPreambleLines, got)
	}
}

// Regression test for the brace-counting bug: a `{` sitting inside a string literal used to
// desync the naive `strings.Count(line, "{")` classifier, so `inBlockDecl` never turned back
// off -- the rest of the cell (here, the trailing `x := 5` statement) silently vanished, with
// FuncDecls and Stmts both empty and no error returned at all.
func TestParseCellHandlesBraceInsideStringLiteral(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("func weird() string {\n\treturn \"a { b\"\n}\n\nx := 5")
	if err != nil {
		t.Fatalf("ParseCell failed: %v", err)
	}
	if len(cell.FuncDecls) != 1 {
		t.Fatalf("Expected 1 FuncDecl, got %d", len(cell.FuncDecls))
	}
	if len(cell.Stmts) != 1 {
		t.Fatalf("Expected 1 Stmt ('x := 5'), got %d", len(cell.Stmts))
	}
}
