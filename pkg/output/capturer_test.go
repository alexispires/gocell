package output_test

import (
	"fmt"
	"github.com/alexispires/gocell/pkg/output"
	"os"
	"strings"
	"testing"
	"time"
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

// Regression test for the deadlock this fix addresses: the OS pipe buffer is finite
// (commonly 64KB), and writing past it blocks until something reads the other end. Before
// this fix, nothing did until Stop() ran -- which the cell itself was responsible for
// reaching, but couldn't, since it was stuck blocked on the write. Writes here total well
// past 64KB, all before Stop() is called, exactly reproducing that ordering.
func TestCapturerDrainsContinuouslyPastPipeBufferSize(t *testing.T) {
	cap := output.NewCapturer()
	if err := cap.Start(); err != nil {
		t.Fatalf("Failed to start capturer: %v", err)
	}

	const chunk = "0123456789ABCDEF" // 16 bytes
	const totalWrites = 10000        // 160,000 bytes, well past any OS pipe buffer

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < totalWrites; i++ {
			fmt.Print(chunk)
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("Writing past the pipe buffer size deadlocked (Stop() was never reached)")
	}

	stdOut, _, err := cap.Stop()
	if err != nil {
		t.Fatalf("Failed to stop capturer: %v", err)
	}

	want := strings.Repeat(chunk, totalWrites)
	if stdOut != want {
		t.Fatalf("Expected %d bytes of captured stdout, got %d bytes (content mismatch)", len(want), len(stdOut))
	}
}
