package compiler_test

import "testing"

// Test declaring structs and their associated methods
func TestDeclarationStructAndMethod(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Counter struct { Count int }`,
		`func (c *Counter) Inc() { c.Count++ }`,
		`c := &Counter{Count: 10}; c.Inc()`,
		`total := c.Count`,
	}
	ctx := runNotebookSession(t, cells)

	ptrTotal := ctx.GetPointer("total")
	if ptrTotal == nil || *(*int)(ptrTotal) != 11 {
		t.Fatalf("Expected value 11")
	}
}

// Test declaring global functions
func TestDeclarationFunction(t *testing.T) {
	t.Parallel()

	cells := []string{
		`func multiply(a, b int) int { return a * b }`,
		`res := multiply(6, 7)`,
	}
	ctx := runNotebookSession(t, cells)

	ptrRes := ctx.GetPointer("res")
	if ptrRes == nil || *(*int)(ptrRes) != 42 {
		t.Fatalf("Expected value 42")
	}
}

// Test interfaces and polymorphism (duck typing)
func TestDeclarationInterface(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Runner interface { Run() string }`,
		`type Robot struct { Name string }`,
		`func (r *Robot) Run() string { return "Robot " + r.Name + " v1" }`,
		`var r Runner = &Robot{Name: "R2D2"}`,
		`output := r.Run()`,
	}
	ctx := runNotebookSession(t, cells)

	ptrOut := ctx.GetPointer("output")
	if ptrOut == nil || *(*string)(ptrOut) != "Robot R2D2 v1" {
		t.Fatalf("Expected value 'Robot R2D2 v1'")
	}
}

// Test nested pointers inside structs
func TestDeclarationNestedStructPointers(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Address struct { City string }`,
		`type Person struct { Name string; Addr *Address }`,
		`p := &Person{Name: "Alice", Addr: &Address{City: "Paris"}}`,
		`p.Addr.City = "Lyon"`,
		`resCity := p.Addr.City`,
	}
	ctx := runNotebookSession(t, cells)

	ptrCity := ctx.GetPointer("resCity")
	if ptrCity == nil || *(*string)(ptrCity) != "Lyon" {
		t.Fatalf("Expected value 'Lyon'")
	}
}

// Test variable shadowing inside a local block
func TestDeclarationVariableShadowing(t *testing.T) {
	t.Parallel()

	cells := []string{
		`x := 100`,
		`if true { x := 999; _ = x }`,
		`finalX := x`,
	}
	ctx := runNotebookSession(t, cells)

	ptrX := ctx.GetPointer("finalX")
	if ptrX == nil || *(*int)(ptrX) != 100 {
		t.Fatalf("Expected value 100")
	}
}

// Test recursive structs and functions
func TestDeclarationRecursiveStruct(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Node struct { Value int; Next *Node }`,
		`func sumList(n *Node) int { if n == nil { return 0 }; return n.Value + sumList(n.Next) }`,
		`head := &Node{Value: 10, Next: &Node{Value: 20, Next: &Node{Value: 30}}}`,
		`totalSum := sumList(head)`,
	}
	ctx := runNotebookSession(t, cells)

	ptrSum := ctx.GetPointer("totalSum")
	if ptrSum == nil || *(*int)(ptrSum) != 60 {
		t.Fatalf("Expected value 60")
	}
}

// Test type redefinition (overriding a previous declaration)
func TestDeclarationTypeRedefinition(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type Point struct { X int }`,
		`type Point struct { X, Y int }`,
		`p := &Point{X: 3, Y: 4}`,
		`dist := p.X + p.Y`,
	}
	ctx := runNotebookSession(t, cells)

	ptrDist := ctx.GetPointer("dist")
	if ptrDist == nil || *(*int)(ptrDist) != 7 {
		t.Fatalf("Expected value 7")
	}
}

// Test that a struct composite literal's field key does not collide with a global pointer
// variable of the same name. Before the AnalyzeCell fix, the "City" identifier in
// Address{City: "Lyon"} was wrongly captured as a reference to the global *CityInfo
// variable, producing a hydrated local variable that was never actually used -> Go
// compilation failure ("declared and not used").
func TestDeclarationStructFieldKeyDoesNotCollideWithPointerVariable(t *testing.T) {
	t.Parallel()

	cells := []string{
		`type CityInfo struct { Name string }`,
		`City := &CityInfo{Name: "Paris"}`,
		`type Address struct { City string }`,
		`addr := Address{City: "Lyon"}`,
		`resCity := addr.City`,
	}
	ctx := runNotebookSession(t, cells)

	ptrResCity := ctx.GetPointer("resCity")
	if ptrResCity == nil || *(*string)(ptrResCity) != "Lyon" {
		t.Fatalf("Expected value 'Lyon' for resCity")
	}
}

// Test that a var declaration nested inside a block (not the cell's top-level statement)
// is treated as an ordinary function-local variable, not exported to the Registry. Before
// this fix, AnalyzeCell added every var/const name to NewVariables regardless of nesting,
// so the generator emitted an export block referencing the variable outside of its scope
// -> Go compilation failure ("undefined: total").
func TestDeclarationNestedVarIsNotExported(t *testing.T) {
	t.Parallel()

	cells := []string{
		`sum := 0
for i := 0; i < 5; i++ {
	var total int
	total = total + i
	sum += total
}`,
	}
	ctx := runNotebookSession(t, cells)

	ptrSum := ctx.GetPointer("sum")
	if ptrSum == nil || *(*int)(ptrSum) != 10 {
		t.Fatalf("Expected value 10")
	}
}
