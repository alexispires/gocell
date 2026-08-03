package output_test

import (
	"fmt"
	"gosk/pkg/output"
	"os"
	"testing"
)

func TestCapturer(t *testing.T) {
	cap := output.NewCapturer()
	if err := cap.Start(); err != nil {
		t.Fatalf("Failed to start capturer: %v", err)
	}

	fmt.Print("Hello Stdout")
	fmt.Fprint(os.Stderr, "Hello Stderr")

	stdOut, stdErr, err := cap.Stop()
	if err != nil {
		t.Fatalf("Failed to stop capturer: %v", err)
	}

	if stdOut != "Hello Stdout" {
		t.Fatalf("Expected stdout 'Hello Stdout', got '%s'", stdOut)
	}
	if stdErr != "Hello Stderr" {
		t.Fatalf("Expected stderr 'Hello Stderr', got '%s'", stdErr)
	}
}
