package session

import (
	"fmt"
	"os/exec"
	"strings"
)

// Inspect answers Shift-Tab: what is the thing under the cursor?
//
// Two sources, in that order. The session first, because it knows things nothing else can -- that
// `w` is a []float64 you fitted three cells ago, living at a given address, or what the body of the
// Point type you declared in cell 2 actually says. Then `go doc`, which covers the standard library
// and anything gocell's own module requires.
//
// `go doc` rather than gopls on purpose: the toolchain is already a hard dependency (every cell is
// a `go build`), it costs about 120ms, and it returns text a frontend can show as-is. Introducing a
// language server to answer a popup would be a large piece of architecture for a small feature.
//
// found is false when nothing recognises the identifier, so the frontend falls back to its own
// help rather than showing an empty panel.
func (s *Session) Inspect(code string, cursorPos int) (text string, found bool) {
	expr := qualifiedNameAt(code, cursorPos)
	if expr == "" {
		return "", false
	}

	// The bare first segment is what the session knows by name; `w.Len` still describes `w`.
	root := expr
	if i := strings.Index(root, "."); i >= 0 {
		root = root[:i]
	}

	if sym, ok := s.reg.GetSymbol(root); ok {
		var b strings.Builder
		fmt.Fprintf(&b, "%s\n\n", root)
		fmt.Fprintf(&b, "  type   %s\n", strings.TrimPrefix(sym.TypeName, "*"))
		// The address is the whole point of gocell: a variable is the same memory from cell to
		// cell, and showing it proves that rather than asserting it.
		fmt.Fprintf(&b, "  addr   %p\n", sym.Ptr)
		fmt.Fprintf(&b, "  since  cell %d\n", sym.CreatedAt)
		return b.String(), true
	}

	// Types and functions a cell declared are already kept as source, to re-inject into later
	// cells. The declaration is better than any summary of it.
	decls := s.typeReg.AllTypes()
	for _, key := range []string{"type_" + root, "func_" + root} {
		if src, ok := decls[key]; ok {
			return root + "\n\n" + src + "\n", true
		}
	}

	return s.goDoc(expr)
}

// goDoc shells out to the toolchain. It runs from the module root so the standard library and
// gocell's own requirements resolve; a third-party package a cell imported does not, because it
// only ever enters the generated plugin's go.mod, never this one.
func (s *Session) goDoc(expr string) (string, bool) {
	if !strings.Contains(expr, ".") {
		// An unqualified name means nothing to `go doc` outside a package directory, and
		// guessing which package the user meant would be worse than saying nothing.
		return "", false
	}

	cmd := exec.Command("go", "doc", expr)
	cmd.Dir = s.builder.ModuleRoot()
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return "", false
	}
	return string(out), true
}

// qualifiedNameAt returns the dotted expression the cursor sits in -- "strings.Builder", not just
// "Builder" -- because that is the form `go doc` needs, and because the package half is what
// disambiguates the name.
//
// Unlike completion, this looks both ways: Shift-Tab in the middle of a word should describe the
// whole word.
func qualifiedNameAt(code string, cursorPos int) string {
	if cursorPos < 0 || cursorPos > len(code) {
		cursorPos = len(code)
	}

	start := cursorPos
	for start > 0 {
		r := rune(code[start-1])
		if isIdentRune(r) || (r == '.' && start >= 2 && isIdentRune(rune(code[start-2]))) {
			start--
			continue
		}
		break
	}
	end := cursorPos
	for end < len(code) && isIdentRune(rune(code[end])) {
		end++
	}

	name := strings.Trim(code[start:end], ".")
	if name == "" || !isIdentRune(rune(name[0])) {
		return ""
	}
	return name
}
