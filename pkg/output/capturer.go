package output

import (
	"bytes"
	"os"
	"sync"
)

// Capturer temporarily intercepts os.Stdout and os.Stderr while a cell executes. Both pipes
// are drained continuously from Start() onward, not just once in Stop(): the OS pipe buffer
// is finite (~64KB), and a cell that writes past it would otherwise block on that write
// forever, since nothing reads the other end until Stop() runs -- which never happens while
// the cell itself is stuck blocked on the write.
type Capturer struct {
	mu         sync.Mutex
	oldStdout  *os.File
	oldStderr  *os.File
	wOut, wErr *os.File
	outBuf     bytes.Buffer
	errBuf     bytes.Buffer
	wg         sync.WaitGroup
}

// NewCapturer creates a new output interceptor.
func NewCapturer() *Capturer {
	return &Capturer{}
}

// Start begins redirecting os.Stdout and os.Stderr to in-memory pipes, and starts draining
// both continuously in the background.
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
	c.wOut = wOut
	c.wErr = wErr
	c.outBuf.Reset()
	c.errBuf.Reset()

	os.Stdout = wOut
	os.Stderr = wErr

	c.wg.Add(2)
	go func() {
		defer c.wg.Done()
		c.drain(rOut, &c.outBuf)
	}()
	go func() {
		defer c.wg.Done()
		c.drain(rErr, &c.errBuf)
	}()

	return nil
}

// drain continuously copies from r into buf until r hits EOF, which happens once its write
// end is closed (in Stop). Writes to buf are guarded by c.mu, since Stop reads it too.
func (c *Capturer) drain(r *os.File, buf *bytes.Buffer) {
	defer func() { _ = r.Close() }()
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			c.mu.Lock()
			buf.Write(chunk[:n])
			c.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// Stop stops the redirection, restores os.Stdout/os.Stderr, and returns the captured output
// once both drain goroutines have finished (i.e. seen EOF on their pipe).
func (c *Capturer) Stop() (stdoutStr string, stderrStr string, err error) {
	c.mu.Lock()
	os.Stdout = c.oldStdout
	os.Stderr = c.oldStderr
	wOut, wErr := c.wOut, c.wErr
	c.mu.Unlock()

	if wOut != nil {
		_ = wOut.Close()
	}
	if wErr != nil {
		_ = wErr.Close()
	}
	c.wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()
	return c.outBuf.String(), c.errBuf.String(), nil
}
