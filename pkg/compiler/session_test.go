package compiler_test

import (
	"testing"

	"github.com/alexispires/gocell/pkg/runtime"
)

func TestSession(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cells []string
		check func(t *testing.T, ctx *runtime.Context)
	}{
		{
			name: "CellReExecution", // idempotence of := on re-execution
			cells: []string{
				`a := 2`,
				`a := 2`, // exact re-execution
				`a := 5`, // value change with :=
				`res := a * 10`,
			},
			check: func(t *testing.T, ctx *runtime.Context) {
				ptrRes := ctx.GetPointer("res")
				if ptrRes == nil || *(*int)(ptrRes) != 50 {
					t.Fatalf("Expected value 50")
				}
			},
		},
		{
			name: "VariablePersistence",
			cells: []string{
				`a := 10`,
				`b := a * 4`,
				`c := a + b`,
			},
			check: func(t *testing.T, ctx *runtime.Context) {
				ptrC := ctx.GetPointer("c")
				if ptrC == nil || *(*int)(ptrC) != 50 {
					t.Fatalf("Expected value 50")
				}
			},
		},
		{
			name: "VariableMutation", // =, +=, *= assignments
			cells: []string{
				`count := 5`,
				`count = count + 10`,
				`count = count * 2`,
			},
			check: func(t *testing.T, ctx *runtime.Context) {
				ptrCount := ctx.GetPointer("count")
				if ptrCount == nil || *(*int)(ptrCount) != 30 {
					t.Fatalf("Expected value 30")
				}
			},
		},
		{
			name: "ImportAccumulation",
			cells: []string{
				`import "math"`,
				`res := math.Sqrt(16.0)`,
			},
			check: func(t *testing.T, ctx *runtime.Context) {
				ptrRes := ctx.GetPointer("res")
				if ptrRes == nil || *(*float64)(ptrRes) != 4.0 {
					t.Fatalf("Expected value 4.0")
				}
			},
		},
		{
			name: "Closures", // capturing the environment via a pointer
			cells: []string{
				`factor := 10; factorPtr := &factor; multiplier := func(x int) int { return x * *factorPtr }`,
				`*factorPtr = 20`,
				`res := multiplier(5)`,
			},
			check: func(t *testing.T, ctx *runtime.Context) {
				ptrRes := ctx.GetPointer("res")
				if ptrRes == nil || *(*int)(ptrRes) != 100 {
					t.Fatalf("Expected value 100")
				}
			},
		},
		{
			name: "Channels", // persistence of channels and goroutines
			cells: []string{
				`ch := make(chan string, 1)`,
				`ch <- "hello gocell"`,
				`msg := <-ch`,
			},
			check: func(t *testing.T, ctx *runtime.Context) {
				ptrMsg := ctx.GetPointer("msg")
				if ptrMsg == nil || *(*string)(ptrMsg) != "hello gocell" {
					t.Fatalf("Expected value 'hello gocell'")
				}
			},
		},
		{
			name: "MultiPointers", // ***int
			cells: []string{
				`val := 42; p1 := &val; p2 := &p1; p3 := &p2`,
				`***p3 = 99`,
				`check := ***p3`,
			},
			check: func(t *testing.T, ctx *runtime.Context) {
				ptrCheck := ctx.GetPointer("check")
				if ptrCheck == nil || *(*int)(ptrCheck) != 99 {
					t.Fatalf("Expected value 99")
				}
			},
		},
		{
			name: "MapAndSliceMutation", // in-place mutation of maps and slices
			cells: []string{
				`m := map[string]int{"alpha": 10}`,
				`m["beta"] = 20`,
				`s := []int{1, 2}; s = append(s, 3)`,
				`valM := m["beta"]; lenS := len(s)`,
			},
			check: func(t *testing.T, ctx *runtime.Context) {
				ptrValM := ctx.GetPointer("valM")
				if ptrValM == nil || *(*int)(ptrValM) != 20 {
					t.Fatalf("Expected valM = 20")
				}
				ptrLenS := ctx.GetPointer("lenS")
				if ptrLenS == nil || *(*int)(ptrLenS) != 3 {
					t.Fatalf("Expected lenS = 3")
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := runNotebookSession(t, c.cells)
			c.check(t, ctx)
		})
	}
}
