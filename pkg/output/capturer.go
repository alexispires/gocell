package output

import (
	"io"
	"os"
	"sync"
)

// Capturer temporarily intercepts os.Stdout and os.Stderr while a cell executes.
type Capturer struct {
	mu         sync.Mutex
	oldStdout  *os.File
	oldStderr  *os.File
	rOut, wOut *os.File
	rErr, wErr *os.File
	outBuf     []byte
	errBuf     []byte
}

// NewCapturer creates a new output interceptor.
func NewCapturer() *Capturer {
	return &Capturer{}
}

// Start begins redirecting os.Stdout and os.Stderr to in-memory pipes.
func (c *Capturer) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	rOut, wOut, err := os.Pipe()
	if err != nil {
		return err
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		_ = rOut.Close()
		_ = wOut.Close()
		return err
	}

	c.oldStdout = os.Stdout
	c.oldStderr = os.Stderr

	c.rOut = rOut
	c.wOut = wOut
	c.rErr = rErr
	c.wErr = wErr

	os.Stdout = wOut
	os.Stderr = wErr

	return nil
}

// Stop stops the redirection, restores os.Stdout/os.Stderr, and returns the captured output.
func (c *Capturer) Stop() (stdoutStr string, stderrStr string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	os.Stdout = c.oldStdout
	os.Stderr = c.oldStderr

	if c.wOut != nil {
		_ = c.wOut.Close()
	}
	if c.wErr != nil {
		_ = c.wErr.Close()
	}

	var outChan = make(chan []byte)
	var errChan = make(chan []byte)

	go func() {
		b, _ := io.ReadAll(c.rOut)
		outChan <- b
	}()

	go func() {
		b, _ := io.ReadAll(c.rErr)
		errChan <- b
	}()

	c.outBuf = <-outChan
	c.errBuf = <-errChan

	if c.rOut != nil {
		_ = c.rOut.Close()
	}
	if c.rErr != nil {
		_ = c.rErr.Close()
	}

	return string(c.outBuf), string(c.errBuf), nil
}
