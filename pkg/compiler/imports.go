package compiler

import (
	"fmt"
	"go/ast"
	"sort"
	"strings"
	"sync"
)

// ImportSpec represents an import specification with its optional alias.
type ImportSpec struct {
	Alias string
	Path  string
}

// ImportTracker thread-safely tracks the set of imports for a session.
type ImportTracker struct {
	mu      sync.RWMutex
	imports map[string]*ImportSpec
}

// NewImportTracker creates a new import manager.
func NewImportTracker() *ImportTracker {
	return &ImportTracker{
		imports: make(map[string]*ImportSpec),
	}
}

// AddImport adds an import to the session.
func (it *ImportTracker) AddImport(alias, importPath string) {
	it.mu.Lock()
	defer it.mu.Unlock()

	cleanPath := strings.Trim(importPath, `"`)
	it.imports[cleanPath] = &ImportSpec{
		Alias: alias,
		Path:  importPath,
	}
}

// AllImports returns the full list of registered imports.
func (it *ImportTracker) AllImports() map[string]*ImportSpec {
	it.mu.RLock()
	defer it.mu.RUnlock()

	result := make(map[string]*ImportSpec, len(it.imports))
	for k, v := range it.imports {
		result[k] = v
	}
	return result
}

// CollectImportsFromFile extracts the imports from an AST file and adds them to the tracker.
func (it *ImportTracker) CollectImportsFromFile(node *ast.File) {
	for _, imp := range node.Imports {
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		p := imp.Path.Value
		it.AddImport(alias, p)
	}
}

// GenerateImportBlockForCode produces the Go import block for the cell.
// goimports takes care of sanitizing and removing unused imports via the AST.
func (it *ImportTracker) GenerateImportBlockForCode(codeBody string) string {
	it.mu.RLock()
	defer it.mu.RUnlock()

	paths := make([]string, 0, len(it.imports))
	for p := range it.imports {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var sb strings.Builder
	sb.WriteString("import (\n")
	sb.WriteString("\t\"fmt\"\n")
	sb.WriteString("\t\"reflect\"\n")
	sb.WriteString("\t\"strings\"\n")
	sb.WriteString("\t\"unsafe\"\n")
	sb.WriteString("\t\"gosk/pkg/runtime\"\n")

	for _, p := range paths {
		spec := it.imports[p]
		cleanPath := strings.Trim(spec.Path, `"`)

		if cleanPath == "gosk/pkg/runtime" || cleanPath == "unsafe" || cleanPath == "fmt" || cleanPath == "strings" || cleanPath == "reflect" {
			continue
		}

		if spec.Alias != "" {
			sb.WriteString(fmt.Sprintf("\t%s %s\n", spec.Alias, spec.Path))
		} else {
			sb.WriteString(fmt.Sprintf("\t%s\n", spec.Path))
		}
	}
	sb.WriteString(")\n")
	return sb.String()
}
