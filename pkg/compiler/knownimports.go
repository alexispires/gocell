package compiler

import (
	"go/ast"

	"github.com/alexispires/gocell/pkg/runtime"
)

// knownPackages maps a package identifier to its import path, for third-party libraries common
// enough in a notebook that typing the import is friction.
//
// goimports fills in standard-library packages by itself, and anything already required by the
// module, but it cannot guess that `plot` means `gonum.org/v1/plot` -- resolution works from what
// is on disk, and nothing on disk mentions gonum until someone asks for it. This table is that
// missing hint, and nothing more: the import still goes through the normal `go build -mod=mod`,
// which fetches it like any other dependency. gocell gains no dependency of its own here, since
// these are strings, not imports.
//
// Ambiguity is the reason this list is short rather than generous. An entry only earns its place
// when the identifier is unlikely to be anything else in a cell: `mat` and `plotter` are safe bets,
// while `charts`, `opts`, `series` or `draw` are ordinary words (and `draw` is already
// `image/draw`), so they stay out. A wrong guess is cheap but not free -- see resolveKnownImports.
var knownPackages = map[string]string{
	// gonum: plotting
	"plot":     "gonum.org/v1/plot",
	"plotter":  "gonum.org/v1/plot/plotter",
	"plotutil": "gonum.org/v1/plot/plotutil",
	"vg":       "gonum.org/v1/plot/vg",
	"vgimg":    "gonum.org/v1/plot/vg/vgimg",

	// gonum: numerics
	"mat":    "gonum.org/v1/gonum/mat",
	"stat":   "gonum.org/v1/gonum/stat",
	"floats": "gonum.org/v1/gonum/floats",

	// 2D graphics, and a common source of image.Image values
	"gg": "github.com/fogleman/gg",

	// gocell's own display API, so `display.Show(...)` needs no import either
	"display": "github.com/alexispires/gocell/pkg/display",
}

// resolveKnownImports registers an import for every known package the cell refers to by selector,
// so `plot.New()` compiles with no import line.
//
// A false positive is self-correcting: a cell that uses `mat` as a variable makes the import
// unused, and goimports strips it from the generated source before it ever reaches the compiler.
// Registry symbols are skipped anyway, so the common case of a variable outliving its cell never
// even reaches that fallback.
func resolveKnownImports(cell *CellContent, importTracker *ImportTracker, known symbolLookup) {
	if cell == nil || importTracker == nil {
		return
	}

	visit := func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		path, isKnown := knownPackages[ident.Name]
		if !isKnown {
			return true
		}
		// A variable of that name shadows the package; it is a field access, not a package
		// reference, and importing here would be wrong.
		if known != nil && known.Has(ident.Name) {
			return true
		}
		importTracker.AddImport("", `"`+path+`"`)
		return true
	}

	for _, stmt := range cell.Stmts {
		ast.Inspect(stmt, visit)
	}
	for _, decl := range cell.FuncDecls {
		ast.Inspect(decl, visit)
	}
	for _, decl := range cell.TypeDecls {
		ast.Inspect(decl, visit)
	}
}

// symbolLookup reports whether a name is already a live symbol, so a package identifier is never
// guessed over a variable that actually exists.
type symbolLookup interface {
	Has(name string) bool
}

// symbolSet adapts the registry snapshot AnalyzeCell already has in hand.
type symbolSet map[string]*runtime.Symbol

func (s symbolSet) Has(name string) bool {
	_, ok := s[name]
	return ok
}
