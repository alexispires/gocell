package compiler

import (
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/ast/astutil"
)

// injectGoroutinePanicRecovery walks cell.Stmts and every cell.FuncDecls body, prepending a
// recover-and-report defer to the body of every `go func() { ... }()` literal found -- Go's
// default behavior for a panic on any goroutine, not just the one running Execute, is to crash
// the whole process, unlike a normal cell error (recovered around Execute itself in
// pkg/plugin/loader.go). A background goroutine (examples/live-goroutines.ipynb's own pattern)
// is meant to survive independently of whichever cell started it or whatever unrelated cell
// runs later -- an out-of-bounds index inside one shouldn't take the entire session down with
// it.
//
// Only `go func() { ... }()` literals are covered, not `go someFunc()` calling a function by
// name (cell-declared or otherwise): that same function might also be called synchronously
// elsewhere, where a panic must still propagate to Execute()'s own recover -- reporting a
// synchronous panic as if it were a stray background one, or worse, silently swallowing it,
// would be actively wrong. Injecting into the function literal's own body, rather than
// wrapping the `go` statement's call expression, is what makes this distinction possible: the
// literal only ever runs as that goroutine's entry point, never synchronously.
//
// Unlike injectInterruptChecks, descent does not stop at a FuncLit boundary: a goroutine that
// itself starts another goroutine should have both protected, not just the outer one.
func injectGoroutinePanicRecovery(cell *CellContent) {
	pre := goroutineRecoveryPre
	for i, stmt := range cell.Stmts {
		cell.Stmts[i] = astutil.Apply(stmt, pre, nil).(ast.Stmt)
	}
	for _, decl := range cell.FuncDecls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		astutil.Apply(fn.Body, pre, nil)
	}
}

// goroutineRecoveryPre is astutil.Apply's pre callback: on every `go func() { ... }()`, prepend
// a recover-and-report defer to the literal's own body.
func goroutineRecoveryPre(c *astutil.Cursor) bool {
	goStmt, ok := c.Node().(*ast.GoStmt)
	if !ok {
		return true
	}
	lit, ok := goStmt.Call.Fun.(*ast.FuncLit)
	if !ok {
		return true
	}
	lit.Body.List = append([]ast.Stmt{newGoroutineRecoverDefer()}, lit.Body.List...)
	return true
}

// newGoroutineRecoverDefer builds a fresh
// `defer func() { if r := recover(); r != nil { fmt.Fprintf(os.Stderr, "panic in background
// goroutine: %v\n", r) } }()` -- a distinct node on every call, never shared/reused across
// injection points.
func newGoroutineRecoverDefer() ast.Stmt {
	recoverCall := &ast.CallExpr{Fun: ast.NewIdent("recover")}
	reportCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{X: ast.NewIdent("fmt"), Sel: ast.NewIdent("Fprintf")},
		Args: []ast.Expr{
			&ast.SelectorExpr{X: ast.NewIdent("os"), Sel: ast.NewIdent("Stderr")},
			&ast.BasicLit{Kind: token.STRING, Value: `"panic in background goroutine: %v\n"`},
			ast.NewIdent("r"),
		},
	}
	innerIf := &ast.IfStmt{
		Init: &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent("r")},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{recoverCall},
		},
		Cond: &ast.BinaryExpr{X: ast.NewIdent("r"), Op: token.NEQ, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: reportCall}}},
	}
	return &ast.DeferStmt{
		Call: &ast.CallExpr{
			Fun: &ast.FuncLit{
				Type: &ast.FuncType{Params: &ast.FieldList{}},
				Body: &ast.BlockStmt{List: []ast.Stmt{innerIf}},
			},
		},
	}
}
