package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// These tests exercise writeKernelSpec directly (an internal, package-level function) rather
// than the exported InstallKernelSpec, which hardcodes the real, per-OS Jupyter kernels
// directory under the user's actual home directory -- calling it from a test would write into
// that real, live directory on whatever machine runs `go test`.

func TestWriteKernelSpecCreatesTheDirectoryAndKernelJSON(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kernels", "gocell")

	if err := writeKernelSpec(dir, "/usr/local/bin/gocell-kernel", "/home/user/go-jupyter"); err != nil {
		t.Fatalf("writeKernelSpec failed: %v", err)
	}

	kernelJSONPath := filepath.Join(dir, "kernel.json")
	data, err := os.ReadFile(kernelJSONPath)
	if err != nil {
		t.Fatalf("Expected kernel.json to have been written: %v", err)
	}

	var spec KernelSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("kernel.json is not valid JSON: %v", err)
	}

	if len(spec.Argv) != 2 || spec.Argv[0] != "/usr/local/bin/gocell-kernel" || spec.Argv[1] != "{connection_file}" {
		t.Fatalf("Unexpected Argv: %v", spec.Argv)
	}
	if spec.DisplayName != "Go (gocell)" {
		t.Fatalf("Unexpected DisplayName: %q", spec.DisplayName)
	}
	if spec.Language != "go" {
		t.Fatalf("Unexpected Language: %q", spec.Language)
	}
	if spec.Env["GOCELL_MODULE_ROOT"] != "/home/user/go-jupyter" {
		t.Fatalf("Unexpected GOCELL_MODULE_ROOT: %q", spec.Env["GOCELL_MODULE_ROOT"])
	}

	for _, name := range []string{"logo-32x32.png", "logo-64x64.png"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("Expected %s to have been written: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("Expected %s to be non-empty", name)
		}
	}
}

func TestWriteKernelSpecOverwritesAnExistingKernelJSON(t *testing.T) {
	dir := t.TempDir()

	if err := writeKernelSpec(dir, "/bin/one", "/mod/one"); err != nil {
		t.Fatalf("First writeKernelSpec failed: %v", err)
	}
	if err := writeKernelSpec(dir, "/bin/two", "/mod/two"); err != nil {
		t.Fatalf("Second writeKernelSpec failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "kernel.json"))
	if err != nil {
		t.Fatalf("Failed to read kernel.json: %v", err)
	}
	var spec KernelSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("kernel.json is not valid JSON: %v", err)
	}
	if spec.Argv[0] != "/bin/two" || spec.Env["GOCELL_MODULE_ROOT"] != "/mod/two" {
		t.Fatalf("Expected the second call to overwrite the first, got Argv=%v Env=%v", spec.Argv, spec.Env)
	}
}
