package compiler_test

import "testing"

// Regression test for bug #2: a closure mutating a captured value-typed symbol used to lose
// that mutation. Root cause was copy-based hydrate/write-back: cell 2's hydrated local `count`
// is a stale snapshot taken *before* inc() mutates the real shared heap box through the
// closure's own captured reference, and the cell's own unconditional write-back then
// overwrote that box with the stale snapshot, erasing the mutation. The pointer-indirection
// rewrite removes the local copy entirely -- both the closure and this cell's own statements
// now read and write through the same shared pointer, so there's nothing to fall out of sync.
func TestClosureMutationVisibleWithinSameCellAsCall(t *testing.T) {
	cells := []string{
		`count := 0
inc := func() { count++ }`,
		// The exact co-occurrence that triggers the bug: the closure call and the read of
		// the mutated symbol in the *same* cell.
		`inc()
result := count`,
	}
	ctx := runNotebookSession(t, cells)

	ptrResult := ctx.GetPointer("result")
	if ptrResult == nil {
		t.Fatalf("Variable 'result' not found")
	}
	if got := *(*int)(ptrResult); got != 1 {
		t.Fatalf("Expected result == 1 (the closure's mutation must be visible), got %d", got)
	}
}

// Calling the closure multiple times across separate cells, each also reading the mutated
// value back in the same cell it calls from -- the general case, not just a single call.
func TestClosureMutationAccumulatesAcrossCells(t *testing.T) {
	cells := []string{
		`count := 0
inc := func() { count++ }`,
		`inc()
r1 := count`,
		`inc()
inc()
r2 := count`,
	}
	ctx := runNotebookSession(t, cells)

	ptrR1 := ctx.GetPointer("r1")
	if ptrR1 == nil || *(*int)(ptrR1) != 1 {
		t.Fatalf("Expected r1 == 1, got ptr=%v", ptrR1)
	}
	ptrR2 := ctx.GetPointer("r2")
	if ptrR2 == nil || *(*int)(ptrR2) != 3 {
		t.Fatalf("Expected r2 == 3, got ptr=%v", ptrR2)
	}
}
