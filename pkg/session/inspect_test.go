package session

import (
	"strings"
	"testing"

	"github.com/alexispires/gocell/pkg/workspace"
)

// Inspect answers from the session first and the toolchain second. The session half is the part
// worth having: nothing outside this kernel can say that w is a []float64 still alive at a given
// address, whereas go doc is the same answer any editor would give.
func TestInspect(t *testing.T) {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	defer func() { _ = wsMgr.CleanUp() }()
	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	if _, err := sess.Execute(`type Point struct{ X, Y int }`); err != nil {
		t.Fatalf("declaring a type: %v", err)
	}
	if _, err := sess.Execute(`w := []float64{1, 2, 3}`); err != nil {
		t.Fatalf("declaring a variable: %v", err)
	}

	cases := []struct {
		name    string
		code    string
		cursor  int
		want    string
		wantYes bool
	}{
		{"session variable", "w", 1, "[]float64", true},
		{"cell-declared type", "Point", 5, "type Point struct", true},
		{"standard library", "strings.Builder", 15, "type Builder", true},
		{"stdlib function", "fmt.Println", 11, "func Println", true},
		{"cursor mid-word still describes the word", "strings.Builder", 10, "type Builder", true},
		{"unknown", "zzzNope", 7, "", false},
		// An unqualified name means nothing to go doc, and guessing a package would be worse
		// than saying nothing.
		{"unqualified stdlib name", "Println", 7, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found := sess.Inspect(c.code, c.cursor)
			if found != c.wantYes {
				t.Fatalf("found=%v, want %v (got %q)", found, c.wantYes, got)
			}
			if c.wantYes && !strings.Contains(got, c.want) {
				t.Fatalf("expected %q in the answer, got %q", c.want, got)
			}
		})
	}

	// The address is what proves gocell's persistence claim, so it must actually be there.
	got, _ := sess.Inspect("w", 1)
	if !strings.Contains(got, "addr") || !strings.Contains(got, "0x") {
		t.Fatalf("expected the live address in the answer, got %q", got)
	}
}

func TestQualifiedNameAt(t *testing.T) {
	cases := []struct {
		code, want string
		pos        int
	}{
		{"strings.Builder", "strings.Builder", 15},
		{"strings.Builder", "strings.Builder", 8},
		{"x := fmt.Println", "fmt.Println", 16},
		{"  spaced  ", "spaced", 8},
		{"", "", 0},
		{"  ", "", 1},
	}
	for _, c := range cases {
		if got := qualifiedNameAt(c.code, c.pos); got != c.want {
			t.Errorf("qualifiedNameAt(%q, %d) = %q, want %q", c.code, c.pos, got, c.want)
		}
	}
}
