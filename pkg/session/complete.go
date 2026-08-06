package session

import (
	"sort"
	"strings"
	"unicode"

	"github.com/alexispires/gocell/pkg/compiler"
)

// goKeywordsAndBuiltins is completed like any other known name -- Go's own keyword and
// universe-scope builtin lists are fixed and small enough to just spell out, unlike
// session-specific names, which come from the Registry/TypeRegistry/ImportTracker below.
var goKeywordsAndBuiltins = []string{
	// Keywords (https://go.dev/ref/spec#Keywords).
	"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough",
	"for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range",
	"return", "select", "struct", "switch", "type", "var",
	// Universe-scope predeclared identifiers (https://go.dev/ref/spec#Predeclared_identifiers).
	"any", "bool", "byte", "complex64", "complex128", "error", "float32", "float64",
	"int", "int8", "int16", "int32", "int64", "rune", "string",
	"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
	"true", "false", "iota", "nil",
	"append", "cap", "close", "complex", "copy", "delete", "imag", "len",
	"make", "new", "panic", "print", "println", "real", "recover", "min", "max", "clear",
}

// isIdentRune reports whether r can appear in a Go identifier -- deliberately permissive
// about the first character (unlike the language spec, which disallows a leading digit):
// Complete only needs to find where a prefix *starts*, not validate that the prefix is
// itself a legal identifier on its own.
func isIdentRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Complete returns candidate names for the identifier prefix ending at cursorPos in code,
// along with the [start, end) byte range in code that a chosen match should replace -- the
// shape complete_reply's cursor_start/cursor_end expect.
//
// If the prefix is immediately preceded by a `.` (completing `foo.` or `foo.Na`), this
// resolves `foo`'s real go/types type (see compiler.ResolveMembers) and offers its actual
// fields and methods instead. Only a single trailing identifier as the selector's base is
// supported (`foo.`, not `foo.Bar().`) -- if that resolution fails for any reason (parse
// error, unresolvable type, a base expression more complex than a bare identifier), this
// falls back to plain name matching over everything the session knows, rather than going
// silent: a less specific suggestion list beats none.
//
// Otherwise, candidates are every name the session already knows (variables, declared
// types/funcs, imported packages, Go keywords and builtins) that starts with the prefix.
func (s *Session) Complete(code string, cursorPos int) (matches []string, start, end int) {
	if cursorPos < 0 || cursorPos > len(code) {
		cursorPos = len(code)
	}

	start = cursorPos
	for start > 0 {
		r := rune(code[start-1])
		if !isIdentRune(r) {
			break
		}
		start--
	}
	end = cursorPos
	prefix := code[start:end]

	if start > 0 && code[start-1] == '.' {
		if memberMatches, ok := s.completeMember(code[:start-1], prefix); ok {
			return memberMatches, start, end
		}
	}

	seen := make(map[string]bool)
	add := func(name string) {
		if name == "" || seen[name] || !strings.HasPrefix(name, prefix) {
			return
		}
		seen[name] = true
		matches = append(matches, name)
	}

	for name := range s.reg.AllSymbols() {
		add(name)
	}
	for key := range s.typeReg.AllTypes() {
		add(typeRegistryCandidateName(key))
	}
	for _, spec := range s.importTracker.AllImports() {
		add(importedPackageName(spec.Alias, spec.Path))
	}
	for _, kw := range goKeywordsAndBuiltins {
		add(kw)
	}

	sort.Strings(matches)
	return matches, start, end
}

// completeMember resolves codeBeforeDot's trailing expression's real type and lists its
// fields/methods matching prefix. ok is false when resolution fails for any reason -- Complete
// falls back to plain name matching in that case, not an empty result.
func (s *Session) completeMember(codeBeforeDot, prefix string) (matches []string, ok bool) {
	members, err := compiler.ResolveMembers(codeBeforeDot, s.reg, s.importTracker, s.typeReg)
	if err != nil {
		return nil, false
	}
	for _, name := range members {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	return matches, true
}

// typeRegistryCandidateName recovers the plain, typeable name from a TypeRegistry key. Keys
// are prefixed by declaration kind (see pkg/compiler/generator.go's cellDeclarations):
// "type_Foo" for a type, "func_Bar" for a plain function, "method_*Foo_Bar" for a method.
// Methods aren't valid bare top-level completions (they need a receiver -- `foo.Bar`, which
// CompleteMember covers, not plain "Bar"), so they're excluded here entirely.
func typeRegistryCandidateName(key string) string {
	switch {
	case strings.HasPrefix(key, "type_"):
		return strings.TrimPrefix(key, "type_")
	case strings.HasPrefix(key, "func_"):
		return strings.TrimPrefix(key, "func_")
	default:
		return ""
	}
}

// importedPackageName is how an import is actually referred to in code: its alias if it has
// one, otherwise the last path segment -- e.g. `"github.com/alexispires/gocell/pkg/runtime"` (no alias) is referred
// to as `runtime`, matching Go's own default-name-from-path-segment rule.
func importedPackageName(alias, path string) string {
	if alias != "" && alias != "_" && alias != "." {
		return alias
	}
	clean := strings.Trim(path, `"`)
	if i := strings.LastIndexByte(clean, '/'); i >= 0 {
		clean = clean[i+1:]
	}
	return clean
}
