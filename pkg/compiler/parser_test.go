package compiler

import (
	"strings"
	"testing"
)

// Keeps stmtWrapperPreambleLines honest against stmtWrapperPreamble: GeneratePluginCode's
// line mapping (see LineMapping) relies on this constant to translate a panicking statement's
// position back to the cell's own source line, and a silent drift between the two would make
// every reported panic line wrong by a fixed, easy-to-miss offset.
func TestStmtWrapperPreambleLinesMatchesPreamble(t *testing.T) {
	t.Parallel()

	if got := strings.Count(stmtWrapperPreamble, "\n"); got != stmtWrapperPreambleLines {
		t.Fatalf("stmtWrapperPreambleLines = %d, but stmtWrapperPreamble actually has %d newlines -- keep them in sync", stmtWrapperPreambleLines, got)
	}
}
