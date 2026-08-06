package compiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

// defaultCellGoVersion is used if the host go.mod's `go` directive cannot be found or
// read; it should normally never be needed in real usage.
const defaultCellGoVersion = "1.22"

// Builder orchestrates compiling a Go file into a .so plugin.
type Builder struct {
	moduleRoot string
	goVersion  string
}

// ModuleRoot returns the root directory of the host gocell module used to build cells.
func (b *Builder) ModuleRoot() string {
	return b.moduleRoot
}

func isGocellModule(dir string) bool {
	if dir == "" {
		return false
	}
	content, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return false
	}
	return strings.Contains(string(content), "module github.com/alexispires/gocell")
}

// NewBuilder creates a new Builder by discovering the host gocell module.
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

// discoverModuleRoot locates the root directory of the host "gocell" module, in order:
// the explicitly provided path, the GOCELL_MODULE_ROOT environment variable (set by the
// installed kernelspec, see pkg/installer), the current module per `go env GOMOD`, then
// walking up from the working directory. Unlike a hardcoded fallback path, an explicit
// error is returned if none of these strategies succeed.
func discoverModuleRoot(moduleRoot string) (string, error) {
	if moduleRoot != "" && isGocellModule(moduleRoot) {
		return moduleRoot, nil
	}

	if envRoot := os.Getenv("GOCELL_MODULE_ROOT"); envRoot != "" && isGocellModule(envRoot) {
		return envRoot, nil
	}

	if out, err := exec.Command("go", "env", "GOMOD").Output(); err == nil {
		modFile := strings.TrimSpace(string(out))
		if modFile != "" && modFile != os.DevNull {
			if dir := filepath.Dir(modFile); isGocellModule(dir) {
				return dir, nil
			}
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for {
			if isGocellModule(dir) {
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
		"gocell module not found: neither the provided path, $GOCELL_MODULE_ROOT, `go env GOMOD`, " +
			"nor the current directory or its parents contain a gocell module go.mod",
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

require github.com/alexispires/gocell v0.0.0
replace github.com/alexispires/gocell => %q
`, b.goVersion, cleanRoot)

	goModPath := filepath.Join(cellDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte(goModContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write go.mod in %s: %w", cellDir, err)
	}

	mainGoPath := filepath.Join(cellDir, "main.go")

	// sourceCode is already goimports-formatted by GeneratePluginCode, which also relies on
	// that formatted output to keep its panic-line mapping accurate -- reformatting again here
	// would risk silently invalidating it.
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
