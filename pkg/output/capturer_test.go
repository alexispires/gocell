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

// Regression test for the data race this fix addresses: a background goroutine from an
// earlier cell (examples/live-goroutines.ipynb's own pattern) keeps calling fmt.Println --
// reading os.Stdout -- concurrently with a later, unrelated cell's Capturer cycling
// Start()/Stop(). Before this fix, Start/Stop reassigned the os.Stdout variable itself, an
// unsynchronized concurrent write racing the background goroutine's unsynchronized read of
// that same variable -- run with `go test -race` to catch it.
//
// This only asserts the cell's own write always survives, not that background output never
// bleeds into an unrelated cell's captured buffer -- it still can, since both now share the
// same underlying fd once redirected (see the Capturer doc comment). Fixing the race is not
// the same as fixing that; `-race` is what actually proves this test's real point.
func TestCapturerNoRaceWithConcurrentBackgroundWriter(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A real background goroutine (examples/live-goroutines.ipynb's own pattern) does
		// actual work between prints; an unthrottled tight loop here doesn't exercise the
		// race any harder, it just floods go test's own stdout pipe with an unbounded amount
		// of data over the test's lifetime, for no added value.
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				fmt.Println("background goroutine, unrelated to any cell")
			}
		}
	}()

	for i := 0; i < 50; i++ {
		cap := output.NewCapturer()
		if err := cap.Start(); err != nil {
			t.Fatalf("Failed to start capturer: %v", err)
		}
		fmt.Print("this cell's own output")
		stdOut, _, err := cap.Stop()
		if err != nil {
			t.Fatalf("Failed to stop capturer: %v", err)
		}
		if !strings.Contains(stdOut, "this cell's own output") {
			t.Fatalf("round %d: this cell's own write is missing entirely from %q", i, stdOut)
		}
	}

	close(stop)
	<-done
}
