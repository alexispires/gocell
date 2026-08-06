package compiler_test

import "testing"

// Regression test for bug #4: pointer-typed symbols previously had no write-back path at
// all (an explicit `strings.HasPrefix(cleanTypeName, "*")` guard skipped them), so
// reassigning one never persisted -- and a cell whose only touch of it was that
// reassignment failed to even compile ("declared and not used", since assignment alone
// isn't a Go "use"). The storage-convention fix (Symbol.Ptr now uniformly means "address
// of a box holding the symbol's own value", including for pointer types) restores
// write-back for every type, fixing both symptoms.
//
// This milestone does not fix bug #2 (a closure mutating a captured value-typed symbol
// loses that mutation when called and read back within the same cell) -- that requires
// the pointer-indirection rewrite in a later milestone.
func TestPointerReassignmentPersistsAcrossCells(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Foo struct{ N int }
foo := &Foo{N: 1}`,
		// Pure reassignment, no read of foo at all: must compile now that write-back
		// gives it a use.
		`foo = &Foo{N: 2}`,
		`n := foo.N`,
	}
	ctx := runNotebookSession(t, cells)

	ptrN := ctx.GetPointer("n")
	if ptrN == nil {
		t.Fatalf("Variable 'n' not found")
	}
	if got := *(*int)(ptrN); got != 2 {
		t.Fatalf("Expected foo.N == 2 after reassignment in a prior cell, got %d", got)
	}
}

// Mutation *through* a pointer (as opposed to reassigning the pointer itself) already
// worked before this milestone and must keep working -- this is the sibling behavior
// that Milestone 2 must not regress.
func TestPointerFieldMutationStillPersistsAcrossCells(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Foo struct{ N int }
foo := &Foo{N: 1}`,
		`foo.N = 99`,
		`n := foo.N`,
	}
	ctx := runNotebookSession(t, cells)

	ptrN := ctx.GetPointer("n")
	if ptrN == nil {
		t.Fatalf("Variable 'n' not found")
	}
	if got := *(*int)(ptrN); got != 99 {
		t.Fatalf("Expected foo.N == 99 after in-place mutation in a prior cell, got %d", got)
	}
}
