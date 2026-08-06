package compiler_test

import "testing"

// Test a generic function, called both with type inference and with an explicit type
// argument.
func TestGenericFunction(t *testing.T) {
	t.Parallel()

	cells := []string{
		`func Max[T int | float64](a, b T) T {
	if a > b {
		return a
	}
	return b
}`,
		`m := Max(3, 7)`,
		`m2 := Max[float64](2.5, 1.1)`,
	}
	ctx := runNotebookSession(t, cells)

	ptrM := ctx.GetPointer("m")
	if ptrM == nil || *(*int)(ptrM) != 7 {
		t.Fatalf("Expected m=7 (inferred type argument)")
	}

	ptrM2 := ctx.GetPointer("m2")
	if ptrM2 == nil || *(*float64)(ptrM2) != 2.5 {
		t.Fatalf("Expected m2=2.5 (explicit type argument)")
	}
}

// Test a generic type with generic methods, instantiated and mutated across cells.
func TestGenericTypeWithMethods(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Stack[T any] struct {
	items []T
}`,
		`func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}`,
		`func (s *Stack[T]) Len() int {
	return len(s.items)
}`,
		`s := &Stack[int]{}`,
		`s.Push(1)`,
		`s.Push(2)`,
		`n := s.Len()`,
	}
	ctx := runNotebookSession(t, cells)

	ptrN := ctx.GetPointer("n")
	if ptrN == nil || *(*int)(ptrN) != 2 {
		t.Fatalf("Expected n=2")
	}
}

// A type parameter name (e.g. "T") used in a generic type's method receivers can coincide
// textually with an existing pointer-typed global -- exactly the class of collision that
// causes "declared and not used" everywhere else in this file (see shadowing_test.go and
// declarations_test.go). Verifies it's harmless here, for two structural reasons: generic
// method declarations are re-injected as opaque source text and never walked by AnalyzeCell
// at all, and a type argument position can never be a variable name in valid Go, since Go
// itself forbids a type and a variable sharing an identifier in the same scope.
func TestGenericTypeParamNameDoesNotCollideWithPointerGlobal(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Marker struct { N int }`,
		`T := &Marker{N: 999}`, // pointer-typed global literally named "T"
		`type Stack[T any] struct {
	items []T
}`,
		`func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}`,
		`s := &Stack[int]{}`,
		`s.Push(1)`,
		`result := T.N`,
	}
	ctx := runNotebookSession(t, cells)

	ptrResult := ctx.GetPointer("result")
	if ptrResult == nil || *(*int)(ptrResult) != 999 {
		t.Fatalf("Expected result=999, the pointer global 'T' must survive the generic type param collision")
	}
}
