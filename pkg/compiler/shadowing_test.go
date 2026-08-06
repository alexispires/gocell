package compiler_test

import "testing"

// These tests cover identifiers that shadow an existing Registry symbol of the same name
// without ever intending to reference it -- a range variable, a closure parameter, a `:=`
// or `var`/`const` nested inside a block, a label. Before AnalyzeCell's scope-aware fix,
// each of these was captured as if it referenced the outer symbol; whenever that outer
// symbol was a pointer type (the one case with no write-back to otherwise reference it),
// the generator emitted a hydration that was never actually used anywhere in the generated
// function, which fails to compile ("declared and not used").

func TestShadowingRangeVariable(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Foo struct { N int }`,
		`result := &Foo{N: 1}`,
		`
xs := []int{10, 20, 30}
sum := 0
for result := range xs {
	sum += result
}
`,
	}
	ctx := runNotebookSession(t, cells)

	ptrSum := ctx.GetPointer("sum")
	if ptrSum == nil || *(*int)(ptrSum) != 3 { // 0+1+2 (range over a slice yields indices)
		t.Fatalf("Expected sum 3, the range variable must shadow the outer *Foo, not reference it")
	}
}

func TestShadowingNestedShortVarDecl(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Foo struct { N int }`,
		`foo := &Foo{N: 1}`,
		`
total := 0
for i := 0; i < 3; i++ {
	foo := i * 10
	total += foo
}
`,
	}
	ctx := runNotebookSession(t, cells)

	ptrTotal := ctx.GetPointer("total")
	if ptrTotal == nil || *(*int)(ptrTotal) != 30 { // 0 + 10 + 20
		t.Fatalf("Expected total 30, the nested := must shadow the outer *Foo, not reference it")
	}
}

func TestShadowingClosureParameter(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Foo struct { N int }`,
		`result := &Foo{N: 1}`,
		`
double := func(result int) int {
	return result * 2
}
out := double(21)
`,
	}
	ctx := runNotebookSession(t, cells)

	ptrOut := ctx.GetPointer("out")
	if ptrOut == nil || *(*int)(ptrOut) != 42 {
		t.Fatalf("Expected out 42, the closure parameter must shadow the outer *Foo, not reference it")
	}
}

func TestShadowingLabel(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Foo struct { N int }`,
		`result := &Foo{N: 1}`,
		`
found := -1
result:
	for i := 0; i < 5; i++ {
		if i == 3 {
			found = i
			break result
		}
	}
`,
	}
	ctx := runNotebookSession(t, cells)

	ptrFound := ctx.GetPointer("found")
	if ptrFound == nil || *(*int)(ptrFound) != 3 {
		t.Fatalf("Expected found 3, the label must not be resolved as a reference to the outer *Foo")
	}
}

// A reference to a name earlier in the same block than its shadowing declaration must still
// resolve to the *outer* symbol -- Go's own scoping rule (a declaration's scope starts after
// it, not at the top of the block) -- and the shadow's own mutations must never leak back
// into the outer, hydrated value.
func TestShadowingDoesNotApplyBeforeItsDeclaration(t *testing.T) {
	t.Parallel()

	cells := []string{
		`x := 100`,
		`
var before, after int
if true {
	before = x
	x := 999
	after = x
}
`,
		`finalX := x`,
	}
	ctx := runNotebookSession(t, cells)

	ptrBefore := ctx.GetPointer("before")
	if ptrBefore == nil || *(*int)(ptrBefore) != 100 {
		t.Fatalf("Expected before=100 (reference precedes the shadow, so it's the outer x)")
	}
	ptrAfter := ctx.GetPointer("after")
	if ptrAfter == nil || *(*int)(ptrAfter) != 999 {
		t.Fatalf("Expected after=999 (reference follows the shadow's declaration)")
	}
	ptrFinalX := ctx.GetPointer("finalX")
	if ptrFinalX == nil || *(*int)(ptrFinalX) != 100 {
		t.Fatalf("Expected finalX=100, the outer x must be untouched by the shadow")
	}
}

// A plain `=` (never a shadow, always a genuine reference) nested inside a block must keep
// mutating the outer symbol -- this is the mirror image of the shadowing tests above, and
// matters most for pointer-typed symbols, which rely on it since they have no write-back.
func TestNestedPlainAssignStillMutatesOuterPointer(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Foo struct { N int }`,
		`foo := &Foo{N: 1}`,
		`
if true {
	foo.N = 42
}
`,
		`result := foo.N`,
	}
	ctx := runNotebookSession(t, cells)

	ptrResult := ctx.GetPointer("result")
	if ptrResult == nil || *(*int)(ptrResult) != 42 {
		t.Fatalf("Expected result 42, a nested plain assignment must still mutate the outer pointer's target")
	}
}
