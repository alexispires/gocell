package compiler

import "testing"

func TestBraceDepth(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		code string
		want int
	}{
		{"empty", "", 0},
		{"balanced block", "if true {\n\tx := 1\n}", 0},
		{"still open block", "if true {\n\tx := 1", 1},
		{"brace inside a closed string", `fmt.Println("Result: {")`, 0},
		{"brace inside an unterminated string", `fmt.Println("Result: {`, 0},
		{"brace inside a line comment", "// a comment with { in it\nx := 1", 0},
		{"brace inside a block comment", "/* block comment { */\nx := 1", 0},
		{"brace inside a multi-line raw string", "`raw string with { spanning\nmultiple lines`\nx := 1", 0},
		{"the ParseCell repro", "func weird() string {\n\treturn \"a { b\"\n}\n\nx := 5", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := BraceDepth(c.code); got != c.want {
				t.Fatalf("BraceDepth(%q) = %d, want %d", c.code, got, c.want)
			}
		})
	}
}
