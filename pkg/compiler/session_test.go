package compiler_test

import "testing"

// Test re-executing cells with := declarations (idempotence)
func TestSessionCellReExecution(t *testing.T) {
	cells := []string{
		`a := 2`,
		`a := 2`, // Exact re-execution
		`a := 5`, // Value change with :=
		`res := a * 10`,
	}
	ctx := runNotebookSession(t, cells)

	ptrRes := ctx.GetPointer("res")
	if ptrRes == nil || *(*int)(ptrRes) != 50 {
		t.Fatalf("Expected value 50")
	}
}

// Test variable persistence across cells
func TestSessionVariablePersistence(t *testing.T) {
	cells := []string{
		`a := 10`,
		`b := a * 4`,
		`c := a + b`,
	}
	ctx := runNotebookSession(t, cells)

	ptrC := ctx.GetPointer("c")
	if ptrC == nil || *(*int)(ptrC) != 50 {
		t.Fatalf("Expected value 50")
	}
}

// Test variable mutations (=, +=, *= assignments)
func TestSessionVariableMutation(t *testing.T) {
	cells := []string{
		`count := 5`,
		`count = count + 10`,
		`count = count * 2`,
	}
	ctx := runNotebookSession(t, cells)

	ptrCount := ctx.GetPointer("count")
	if ptrCount == nil || *(*int)(ptrCount) != 30 {
		t.Fatalf("Expected value 30")
	}
}

// Test import retention and reuse
func TestSessionImportAccumulation(t *testing.T) {
	cells := []string{
		`import "math"`,
		`res := math.Sqrt(16.0)`,
	}
	ctx := runNotebookSession(t, cells)

	ptrRes := ctx.GetPointer("res")
	if ptrRes == nil || *(*float64)(ptrRes) != 4.0 {
		t.Fatalf("Expected value 4.0")
	}
}

// Test closures capturing the environment via a pointer
func TestSessionClosures(t *testing.T) {
	cells := []string{
		`factor := 10; factorPtr := &factor; multiplier := func(x int) int { return x * *factorPtr }`,
		`*factorPtr = 20`,
		`res := multiplier(5)`,
	}
	ctx := runNotebookSession(t, cells)

	ptrRes := ctx.GetPointer("res")
	if ptrRes == nil || *(*int)(ptrRes) != 100 {
		t.Fatalf("Expected value 100")
	}
}

// Test persistence of channels and goroutines
func TestSessionChannels(t *testing.T) {
	cells := []string{
		`ch := make(chan string, 1)`,
		`ch <- "hello gosk"`,
		`msg := <-ch`,
	}
	ctx := runNotebookSession(t, cells)

	ptrMsg := ctx.GetPointer("msg")
	if ptrMsg == nil || *(*string)(ptrMsg) != "hello gosk" {
		t.Fatalf("Expected value 'hello gosk'")
	}
}

// Test multi-level pointers (***int)
func TestSessionMultiPointers(t *testing.T) {
	cells := []string{
		`val := 42; p1 := &val; p2 := &p1; p3 := &p2`,
		`***p3 = 99`,
		`check := ***p3`,
	}
	ctx := runNotebookSession(t, cells)

	ptrCheck := ctx.GetPointer("check")
	if ptrCheck == nil || *(*int)(ptrCheck) != 99 {
		t.Fatalf("Expected value 99")
	}
}

// Test in-place mutation of maps and slices
func TestSessionMapAndSliceMutation(t *testing.T) {
	cells := []string{
		`m := map[string]int{"alpha": 10}`,
		`m["beta"] = 20`,
		`s := []int{1, 2}; s = append(s, 3)`,
		`valM := m["beta"]; lenS := len(s)`,
	}
	ctx := runNotebookSession(t, cells)

	ptrValM := ctx.GetPointer("valM")
	if ptrValM == nil || *(*int)(ptrValM) != 20 {
		t.Fatalf("Expected valM = 20")
	}

	ptrLenS := ctx.GetPointer("lenS")
	if ptrLenS == nil || *(*int)(ptrLenS) != 3 {
		t.Fatalf("Expected lenS = 3")
	}
}
