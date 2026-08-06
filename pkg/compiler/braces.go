package compiler

import (
	"go/scanner"
	"go/token"
)

// BraceDepth returns the net brace depth ({ minus }) of code, counting only real `{`/`}`
// tokens -- unlike a plain character count, one sitting inside a string/rune literal or a
// comment is correctly ignored, since this tokenizes with go/scanner (the real Go lexer)
// instead of scanning raw characters. A positive result means code has more unclosed `{` than
// `}` (a statement or declaration isn't finished yet); zero or negative means it's balanced
// (or over-closed, a real syntax error the compiler will report on its own terms -- this
// function only answers "is it still open," not "is it valid").
//
// Used both by ParseCell (to tell a still-open type/func block from a finished one) and by
// cmd/gocell-repl (to tell when to keep showing the "...>" continuation prompt) -- previously
// two separate, naive `strings.Count(line, "{")` implementations, each breaking the same way
// on a `{` inside a string (e.g. `fmt.Println("Result: {")` desynced both: ParseCell silently
// dropped the rest of the cell, and the REPL hung forever waiting for a `}` that already
// existed, textually, inside the string).
//
// code need not be syntactically valid Go -- it's frequently mid-typed, incomplete input by
// design (that's the whole reason this function exists). The scanner is given a nil error
// handler, so it never stops or panics on malformed input (an unterminated string literal, for
// instance, is tolerated: it's treated as running to EOF rather than emitting a hard error),
// it just does its best-effort token classification.
func BraceDepth(code string) int {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(code))

	var s scanner.Scanner
	s.Init(file, []byte(code), nil, scanner.ScanComments)

	depth := 0
	for {
		_, tok, _ := s.Scan()
		switch tok {
		case token.LBRACE:
			depth++
		case token.RBRACE:
			depth--
		case token.EOF:
			return depth
		}
	}
}
