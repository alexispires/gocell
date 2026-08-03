package main

import (
	"fmt"
	"log"

	"gosk/pkg/installer"
)

func main() {
	fmt.Println("Installing the Go Jupyter kernel (gosk)...")

	dir, err := installer.InstallKernelSpec()
	if err != nil {
		log.Fatalf("Failed to install kernelspec: %v", err)
	}

	fmt.Printf("✅ gosk kernel successfully installed in:\n%s\n\n", dir)
	fmt.Println("You can now use 'gosk' in Jupyter Lab, Jupyter Notebook, or VS Code!")
}
