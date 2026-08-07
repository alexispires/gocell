package compiler

import (
	"strings"
	"testing"
)

func TestInjectGoroutinePanicRecoveryWrapsGoFuncLit(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("go func() {\n\t_ = 1\n}()")
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injectGoroutinePanicRecovery(cell)
	got := printStmts(t, cell)

	if !strings.Contains(got, "recover()") {
		t.Fatalf("Expected a recover() injected into the goroutine literal, got:\n%s", got)
	}
}

// Regression guard for the documented scope limit: a `go someFunc()` call by name isn't
// wrapped, since the same function might also be called synchronously elsewhere, where a
// panic must still reach Execute()'s own recover rather than being silently swallowed here.
func TestInjectGoroutinePanicRecoveryDoesNotWrapNamedFunctionCalls(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("func worker() {\n\t_ = 1\n}\ngo worker()")
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injectGoroutinePanicRecovery(cell)

	if strings.Contains(printStmts(t, cell), "recover()") {
		t.Fatalf("Expected no injected recover for a named function call")
	}
	var sb strings.Builder
	for _, decl := range cellDeclarations(cell) {
		sb.WriteString(decl.code)
	}
	if strings.Contains(sb.String(), "recover()") {
		t.Fatalf("Expected the declared function's own body to stay untouched, got:\n%s", sb.String())
	}
}

func TestInjectGoroutinePanicRecoveryCoversNestedGoroutines(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("go func() {\n\tgo func() {\n\t\t_ = 1\n\t}()\n}()")
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injectGoroutinePanicRecovery(cell)
	got := printStmts(t, cell)

	if n := strings.Count(got, "recover()"); n != 2 {
		t.Fatalf("Expected 2 injected recovers (outer + inner goroutine), got %d:\n%s", n, got)
	}
}

func TestInjectGoroutinePanicRecoveryCoversFuncDecls(t *testing.T) {
	t.Parallel()

	cell, err := ParseCell("func start() {\n\tgo func() {\n\t\t_ = 1\n\t}()\n}")
	if err != nil {
		t.Fatalf("Failed to parse: %v", err)
	}

	injectGoroutinePanicRecovery(cell)

	var sb strings.Builder
	for _, decl := range cellDeclarations(cell) {
		sb.WriteString(decl.code)
	}
	if !strings.Contains(sb.String(), "recover()") {
		t.Fatalf("Expected the injected recover inside the declared function's goroutine, got:\n%s", sb.String())
	}
}
