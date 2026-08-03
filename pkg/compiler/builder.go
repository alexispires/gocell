package compiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/imports"
)

// defaultCellGoVersion is used if the host go.mod's `go` directive cannot be found or
// read; it should normally never be needed in real usage.
const defaultCellGoVersion = "1.22"

// Builder orchestrates compiling a Go file into a .so plugin.
type Builder struct {
	moduleRoot string
	goVersion  string
}

// ModuleRoot returns the root directory of the host gosk module used to build cells.
func (b *Builder) ModuleRoot() string {
	return b.moduleRoot
}

func isGoskModule(dir string) bool {
	if dir == "" {
		return false
	}
	content, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	return strings.Contains(string(content), "module gosk")
}

// NewBuilder creates a new Builder by discovering the host gosk module.
func NewBuilder(moduleRoot string) (*Builder, error) {
	root, err := discoverModuleRoot(moduleRoot)
	if err != nil {
		return nil, err
	}

	return &Builder{
		moduleRoot: root,
		goVersion:  detectGoVersion(root),
	}, nil
}

// discoverModuleRoot locates the root directory of the host "gosk" module, in order:
// the explicitly provided path, the GOSK_MODULE_ROOT environment variable (set by the
// installed kernelspec, see pkg/installer), the current module per `go env GOMOD`, then
// walking up from the working directory. Unlike a hardcoded fallback path, an explicit
// error is returned if none of these strategies succeed.
func discoverModuleRoot(moduleRoot string) (string, error) {
	if moduleRoot != "" && isGoskModule(moduleRoot) {
		return moduleRoot, nil
	}

	if envRoot := os.Getenv("GOSK_MODULE_ROOT"); envRoot != "" && isGoskModule(envRoot) {
		return envRoot, nil
	}

	if out, err := exec.Command("go", "env", "GOMOD").Output(); err == nil {
		modFile := strings.TrimSpace(string(out))
		if modFile != "" && modFile != os.DevNull {
			if dir := filepath.Dir(modFile); isGoskModule(dir) {
				return dir, nil
			}
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for {
			if isGoskModule(dir) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	return "", fmt.Errorf(
		"gosk module not found: neither the provided path, $GOSK_MODULE_ROOT, `go env GOMOD`, " +
			"nor the current directory or its parents contain a gosk module go.mod",
	)
}

// detectGoVersion reads the `go` directive from the host module's go.mod, so the go.mod
// generated for each cell declares the same version as the kernel itself — Go's `plugin`
// package requires the host and the plugin to be built with a compatible toolchain.
func detectGoVersion(moduleRoot string) string {
	data, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		return defaultCellGoVersion
	}

	mf, err := modfile.Parse("go.mod", data, nil)
	if err != nil || mf.Go == nil || mf.Go.Version == "" {
		return defaultCellGoVersion
	}

	return mf.Go.Version
}

// BuildPlugin writes go.mod, main.go, and runs 'go build -mod=mod -buildmode=plugin'.
func (b *Builder) BuildPlugin(cellDir string, sourceCode string) (string, error) {
	cleanRoot := filepath.Clean(b.moduleRoot)

	goModContent := fmt.Sprintf(`module cell

go %s

require gosk v0.0.0
replace gosk => %q
`, b.goVersion, cleanRoot)

	goModPath := filepath.Join(cellDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte(goModContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write go.mod in %s: %w", cellDir, err)
	}

	mainGoPath := filepath.Join(cellDir, "main.go")

	// Clean up via goimports (automatic AST resolution and removal of unused imports)
	opts := &imports.Options{
		Comments:   true,
		TabIndent:  true,
		TabWidth:   8,
		FormatOnly: false,
	}
	formattedCode, err := imports.Process(mainGoPath, []byte(sourceCode), opts)
	if err == nil {
		sourceCode = string(formattedCode)
	}

	if err := os.WriteFile(mainGoPath, []byte(sourceCode), 0644); err != nil {
		return "", fmt.Errorf("failed to write file %s: %w", mainGoPath, err)
	}

	soPath := filepath.Join(cellDir, "cell.so")

	cmd := exec.Command("go", "build", "-mod=mod", "-buildmode=plugin", "-o", soPath, mainGoPath)
	cmd.Dir = cellDir

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("plugin compilation error:\n%s", stderrBuf.String())
	}

	return soPath, nil
}
