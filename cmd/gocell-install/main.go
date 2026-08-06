package main

import (
	"fmt"
	"log"

	"github.com/alexispires/gocell/pkg/installer"
)

func main() {
	fmt.Println("Installing the Go Jupyter kernel (gocell)...")

	dir, err := installer.InstallKernelSpec()
	if err != nil {
		log.Fatalf("Failed to install kernelspec: %v", err)
	}

	fmt.Printf("✅ gocell kernel successfully installed in:\n%s\n\n", dir)
	fmt.Println("You can now use 'gocell' in Jupyter Lab, Jupyter Notebook, or VS Code!")
}
