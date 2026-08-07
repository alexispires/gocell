package compiler

import (
	"go/printer"
	"strings"
	"testing"
)

func printStmts(t *testing.T, cell *CellContent) string {
	t.Helper()
	var sb strings.Builder
	for _, stmt := range cell.Stmts {
		if err := printer.Fprint(&sb, cell.Fset, stmt); err != nil {
			t.Fatalf("Failed to print stmt: %v", err)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func TestInjectInterruptChecksInsertsIntoForAndRange(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("for i := 0; i < 10; i++ {\n\t_ = i\n}\nfor range []int{1, 2} {\n}")
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injectInterruptChecks(cell)
	got := printStmts(t, cell)

	if n := strings.Count(got, "panic(__gocell_ctx.Err())"); n != 2 {
		t.Fatalf("Expected 2 injected checks (one per loop), got %d:\n%s", n, got)
	}
}

func TestInjectInterruptChecksSkipsFuncLit(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("go func() {\n\tfor {\n\t\t_ = 1\n\t}\n}()")
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injectInterruptChecks(cell)
	got := printStmts(t, cell)

	if strings.Contains(got, "__gocell_ctx") {
		t.Fatalf("Expected no injected check inside a goroutine's loop, got:\n%s", got)
	}
}

func TestInjectInterruptChecksCoversNestedLoops(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("for i := 0; i < 10; i++ {\n\tfor j := 0; j < 10; j++ {\n\t\t_ = j\n\t}\n}")
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injectInterruptChecks(cell)
	got := printStmts(t, cell)

	if n := strings.Count(got, "panic(__gocell_ctx.Err())"); n != 2 {
		t.Fatalf("Expected 2 injected checks (outer + inner loop), got %d:\n%s", n, got)
	}
}

func TestInjectInterruptChecksCoversFuncDecls(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("func slow(n int) int {\n\tsum := 0\n\tfor i := 0; i < n; i++ {\n\t\tsum += i\n\t}\n\treturn sum\n}")
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injectInterruptChecks(cell)

	var sb strings.Builder
	for _, decl := range cellDeclarations(cell) {
		sb.WriteString(decl.code)
	}
	got := sb.String()

	if !strings.Contains(got, "panic(__gocell_ctx.Err())") {
		t.Fatalf("Expected the injected check inside the declared function's loop, got:\n%s", got)
	}
}

func TestInjectInterruptChecksRecordsOriginalLines(t *testing.T) {
	t.Parallel()

	// Two loops inside a single top-level if-block, so both land in cell.Stmts[0]'s record.
	code := "if true {\n\tfor i := 0; i < 1; i++ {\n\t\t_ = i\n\t}\n\tfor j := 0; j < 1; j++ {\n\t\t_ = j\n\t}\n}"
	cell, err := ParseCell(code)
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injected := injectInterruptChecks(cell)
	if len(injected) != 1 || len(injected[0]) != 2 {
		t.Fatalf("Expected 1 statement with 2 recorded injections, got %v", injected)
	}
	if injected[0][0] >= injected[0][1] {
		t.Fatalf("Expected ascending sorted lines, got %v", injected[0])
	}
}
