package compiler

import (
	"go/ast"
	"go/token"
	"sort"

	"golang.org/x/tools/go/ast/astutil"
)

// injectInterruptChecks walks cell.Stmts and every cell.FuncDecls body, prepending a
// cooperative interrupt check to the top of every for/range loop it finds -- Go has no way to
// forcibly stop a running goroutine, so a loop can only be asked to notice and stop itself.
// Descent stops at any *ast.FuncLit: a closure/goroutine is meant to keep running
// independently of whichever cell started it (examples/live-goroutines.ipynb depends on
// exactly this), so injecting into one would let an unrelated later cell's interrupt reach
// into background work it was never meant to touch.
//
// Real, known limitation, not fixed by this or anything short of a redesign: this only helps
// a loop that keeps *iterating* without ever terminating -- it gives the check somewhere to
// run. A single statement that blocks once and never returns on its own (an unbuffered
// channel receive nothing ever sends to, a giant time.Sleep, a mutex.Lock() that never gets
// the lock, a blocking network call) has no loop, hence no repeated point to place a check at,
// and Interrupt() does nothing for it -- the flag gets set, but nothing ever looks at it
// again. Restarting the kernel remains the only way out of that case, same as before this.
//
// The check references a package-level __gocell_ctx (see GeneratePluginCode), not a local ctx
// parameter: cell.Stmts run inside Execute(ctx *runtime.Context) error, where a local ctx is
// in scope, but a function the cell itself declared has its own, arbitrary signature -- no ctx
// parameter, and a bare "return" of the wrong type wouldn't compile. The injected check
// panics instead of returning: panic doesn't care about the enclosing function's return type,
// and pkg/plugin/loader.go already recover()s around the whole Execute() call, so an
// interrupt becomes "a panic that says interrupted" -- reusing that existing machinery
// instead of building a parallel one.
//
// Returns, for each cell.Stmts[i], the original (pre-injection) line of every loop touched
// within it, sorted ascending -- GeneratePluginCode threads this into LineMapping so
// remapPanicError can correct for the extra generated lines. cell.FuncDecls aren't tracked
// this way: their printed text was never tied to a LineMapping entry at all, before or after
// this change (see LineMapping's own doc).
func injectInterruptChecks(cell *CellContent) [][]int {
	injected := make([][]int, len(cell.Stmts))
	for i, stmt := range cell.Stmts {
		var lines []int
		pre := func(c *astutil.Cursor) bool {
			return injectInterruptPre(c, cell.Fset, &lines)
		}
		cell.Stmts[i] = astutil.Apply(stmt, pre, nil).(ast.Stmt)
		sort.Ints(lines)
		injected[i] = lines
	}

	for _, decl := range cell.FuncDecls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		pre := func(c *astutil.Cursor) bool {
			return injectInterruptPre(c, cell.Fset, nil)
		}
		astutil.Apply(fn.Body, pre, nil)
	}

	return injected
}

// injectInterruptPre is astutil.Apply's pre callback shared by both cell.Stmts and
// cell.FuncDecls: stop descending into a FuncLit, and prepend a check to every For/Range
// loop's body. lines, when non-nil, collects each touched loop's original line.
func injectInterruptPre(c *astutil.Cursor, fset *token.FileSet, lines *[]int) bool {
	switch n := c.Node().(type) {
	case *ast.FuncLit:
		return false
	case *ast.ForStmt:
		injectCheckInto(n.Body, n.Pos(), fset, lines)
	case *ast.RangeStmt:
		injectCheckInto(n.Body, n.Pos(), fset, lines)
	}
	return true
}

func injectCheckInto(body *ast.BlockStmt, pos token.Pos, fset *token.FileSet, lines *[]int) {
	if lines != nil && fset != nil {
		*lines = append(*lines, fset.Position(pos).Line)
	}
	body.List = append([]ast.Stmt{newInterruptCheck()}, body.List...)
}

// newInterruptCheck builds a fresh `if __gocell_ctx.Err() != nil { panic(__gocell_ctx.Err()) }`
// -- a distinct node on every call, never shared/reused across injection points.
func newInterruptCheck() ast.Stmt {
	errCall := func() *ast.CallExpr {
		return &ast.CallExpr{
			Fun: &ast.SelectorExpr{X: ast.NewIdent("__gocell_ctx"), Sel: ast.NewIdent("Err")},
		}
	}
	return &ast.IfStmt{
		Cond: &ast.BinaryExpr{X: errCall(), Op: token.NEQ, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{errCall()}}},
			},
		},
	}
}
