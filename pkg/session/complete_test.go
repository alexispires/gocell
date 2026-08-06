package session

import (
	"slices"
	"testing"

	"gocell/pkg/workspace"
)

func newTestSession(t *testing.T) *Session {
	t.Helper()
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		t.Fatalf("Failed to create workspace: %v", err)
	}
	t.Cleanup(func() { _ = wsMgr.CleanUp() })

	sess, err := New(wsMgr)
	if err != nil {
		t.Fatalf("New session failed: %v", err)
	}
	return sess
}

func TestCompleteMatchesExistingVariable(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.Execute(`countdown := 10`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	code := "coun"
	matches, start, end := sess.Complete(code, len(code))

	if start != 0 || end != len(code) {
		t.Fatalf("Expected range [0, %d), got [%d, %d)", len(code), start, end)
	}
	if !slices.Contains(matches, "countdown") {
		t.Fatalf("Expected 'countdown' among matches, got %v", matches)
	}
}

func TestCompleteMatchesDeclaredTypeAndFunc(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.Execute(`type Widget struct{ N int }
func WidgetCount() int { return 1 }`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	code := "Widg"
	matches, _, _ := sess.Complete(code, len(code))

	if !slices.Contains(matches, "Widget") {
		t.Fatalf("Expected 'Widget' among matches, got %v", matches)
	}
	if !slices.Contains(matches, "WidgetCount") {
		t.Fatalf("Expected 'WidgetCount' among matches, got %v", matches)
	}
}

func TestCompleteMatchesImportedPackage(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.Execute(`import "strings"
_ = strings.ToUpper("x")`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	code := "stri"
	matches, _, _ := sess.Complete(code, len(code))

	if !slices.Contains(matches, "strings") {
		t.Fatalf("Expected 'strings' among matches, got %v", matches)
	}
}

func TestCompleteMatchesKeywordsAndBuiltins(t *testing.T) {
	sess := newTestSession(t)

	code := "fu"
	matches, _, _ := sess.Complete(code, len(code))
	if !slices.Contains(matches, "func") {
		t.Fatalf("Expected keyword 'func' among matches, got %v", matches)
	}

	code2 := "appe"
	matches2, _, _ := sess.Complete(code2, len(code2))
	if !slices.Contains(matches2, "append") {
		t.Fatalf("Expected builtin 'append' among matches, got %v", matches2)
	}
}

// The cross-cell case: `foo` was declared and exported in an earlier, already-executed cell.
func TestCompleteMemberFromEarlierCell(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.Execute(`type Foo struct{ Name string; Age int }
func (f *Foo) Greet() string { return "hi " + f.Name }
foo := &Foo{Name: "Ada", Age: 30}`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	code := "foo."
	matches, start, end := sess.Complete(code, len(code))

	if start != len(code) || end != len(code) {
		t.Fatalf("Expected an empty-prefix range at %d, got [%d, %d)", len(code), start, end)
	}
	for _, want := range []string{"Name", "Age", "Greet"} {
		found := false
		for _, m := range matches {
			if m == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Expected %q among matches, got %v", want, matches)
		}
	}
}

// The same-cell case: `foo` was declared moments ago in the SAME, not-yet-submitted cell --
// nothing has executed yet, so this can only work via static (go/types) resolution, not
// anything reflection-based on a live Registry value.
func TestCompleteMemberFromSameNotYetSubmittedCell(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.Execute(`type Foo struct{ Name string }`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	code := "foo := &Foo{Name: \"Ada\"}\nfoo."
	matches, _, _ := sess.Complete(code, len(code))

	found := false
	for _, m := range matches {
		if m == "Name" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Expected 'Name' among matches for a same-cell, not-yet-executed variable, got %v", matches)
	}
}

// Completion after a `.` filters by whatever prefix follows it, e.g. "foo.Na" -> "Name".
func TestCompleteMemberWithPartialFieldPrefix(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.Execute(`type Foo struct{ Name string; Nation string; Age int }
foo := &Foo{}`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	code := "foo.Na"
	matches, start, end := sess.Complete(code, len(code))

	if start != 4 || end != len(code) {
		t.Fatalf("Expected range [4, %d), got [%d, %d)", len(code), start, end)
	}
	for _, want := range []string{"Name", "Nation"} {
		found := false
		for _, m := range matches {
			if m == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("Expected %q among matches, got %v", want, matches)
		}
	}
	if slices.Contains(matches, "Age") {
		t.Fatalf("Expected 'Age' to be filtered out by the 'Na' prefix, got %v", matches)
	}
}

// A member completion that can't be resolved (base expression too complex for this scope,
// e.g. a chained call) falls back to plain name matching rather than returning nothing.
func TestCompleteMemberFallsBackWhenUnresolvable(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.Execute(`nameVariable := 42`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	code := "foo.Bar().name"
	matches, _, _ := sess.Complete(code, len(code))

	if !slices.Contains(matches, "nameVariable") {
		t.Fatalf("Expected fallback to plain matching to still find 'nameVariable', got %v", matches)
	}
}

// Completion is computed mid-line, not just at the end of the whole code string --
// cursorPos may be in the middle of a longer, still-being-typed cell.
func TestCompleteUsesCursorPosNotEndOfString(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.Execute(`total := 5`); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	code := "tot + 1"
	cursorPos := 3 // right after "tot"
	matches, start, end := sess.Complete(code, cursorPos)

	if start != 0 || end != 3 {
		t.Fatalf("Expected range [0, 3), got [%d, %d)", start, end)
	}
	if !slices.Contains(matches, "total") {
		t.Fatalf("Expected 'total' among matches, got %v", matches)
	}
}
