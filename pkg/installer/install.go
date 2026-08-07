package installer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type KernelSpec struct {
	Argv        []string          `json:"argv"`
	DisplayName string            `json:"display_name"`
	Language    string            `json:"language"`
	Env         map[string]string `json:"env,omitempty"`
}

// InstallKernelSpec installs the kernel.json file in the appropriate Jupyter directory.
func InstallKernelSpec() (string, error) {
	// 1. Get the absolute path of the gocell-kernel binary
	kernelBinary, err := exec.LookPath("gocell-kernel")
	if err != nil {
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			home, _ := os.UserHomeDir()
			gopath = filepath.Join(home, "go")
		}
		binPath := filepath.Join(gopath, "bin", "gocell-kernel")
		if _, errStat := os.Stat(binPath); errStat == nil {
			kernelBinary = binPath
		} else {
			cwd, _ := os.Getwd()
			kernelBinary = filepath.Join(cwd, "gocell-kernel")
		}
	}

	absBinaryPath, err := filepath.Abs(kernelBinary)
	if err != nil {
		absBinaryPath = kernelBinary
	}

	// 2. Determine the gocell module's root directory
	cwd, _ := os.Getwd()
	modRoot := cwd
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			modRoot = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// 3. Determine the Jupyter directory based on the OS
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to find the user's home directory: %w", err)
	}

	var jupyterKernelDir string
	if runtime.GOOS == "darwin" {
		jupyterKernelDir = filepath.Join(homeDir, "Library", "Jupyter", "kernels", "gocell")
	} else {
		jupyterKernelDir = filepath.Join(homeDir, ".local", "share", "jupyter", "kernels", "gocell")
	}

	if err := writeKernelSpec(jupyterKernelDir, absBinaryPath, modRoot); err != nil {
		return "", err
	}
	return jupyterKernelDir, nil
}

// writeKernelSpec creates jupyterKernelDir and writes kernel.json into it. Split out from
// InstallKernelSpec so tests can point it at a throwaway directory instead of the real,
// hardcoded per-OS Jupyter kernels directory under the user's actual home directory.
func writeKernelSpec(jupyterKernelDir, kernelBinary, modRoot string) error {
	if err := os.MkdirAll(jupyterKernelDir, 0755); err != nil {
		return fmt.Errorf("failed to create kernelspec directory %s: %w", jupyterKernelDir, err)
	}

	spec := KernelSpec{
		Argv:        []string{kernelBinary, "{connection_file}"},
		DisplayName: "Go (gocell)",
		Language:    "go",
		Env: map[string]string{
			"GOCELL_MODULE_ROOT": modRoot,
		},
	}

	specData, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode kernel.json: %w", err)
	}

	kernelJsonPath := filepath.Join(jupyterKernelDir, "kernel.json")
	if err := os.WriteFile(kernelJsonPath, specData, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", kernelJsonPath, err)
	}

	return nil
}
