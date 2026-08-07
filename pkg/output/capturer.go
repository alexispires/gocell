package output

import (
	"bytes"
	"os"
	"sync"
	"syscall"
)

// Capturer temporarily intercepts os.Stdout and os.Stderr while a cell executes. Both pipes
// are drained continuously from Start() onward, not just once in Stop(): the OS pipe buffer
// is finite (~64KB), and a cell that writes past it would otherwise block on that write
// forever, since nothing reads the other end until Stop() runs -- which never happens while
// the cell itself is stuck blocked on the write.
//
// Redirection happens at the file-descriptor level (dup2 onto fd 1/2), not by reassigning the
// os.Stdout/os.Stderr package variables. Those variables are never written here -- only read
// once, for their Fd() -- so no other goroutine reading them (a background goroutine from an
// earlier cell, still running via examples/live-goroutines.ipynb's own pattern, printing
// concurrently with this cell's Start()/Stop()) can observe a torn or stale value: there is no
// concurrent write to race against. What os.Stdout.Write ends up writing to is redirected
// instead, transparently to every caller, including ones that cached os.Stdout before this
// package ever ran.
type Capturer struct {
	mu             sync.Mutex
	savedStdoutFd  int
	savedStderrFd  int
	wOut, wErr     *os.File
	outBuf, errBuf bytes.Buffer
	wg             sync.WaitGroup
}

// NewCapturer creates a new output interceptor.
func NewCapturer() *Capturer {
	return &Capturer{}
}

// Start begins redirecting fd 1/2 (stdout/stderr) to in-memory pipes, and starts draining both
// continuously in the background.
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

	stdoutFd := int(os.Stdout.Fd())
	stderrFd := int(os.Stderr.Fd())

	savedStdoutFd, err := syscall.Dup(stdoutFd)
	if err != nil {
		_ = rOut.Close()
		_ = wOut.Close()
		_ = rErr.Close()
		_ = wErr.Close()
		return err
	}
	savedStderrFd, err := syscall.Dup(stderrFd)
	if err != nil {
		_ = syscall.Close(savedStdoutFd)
		_ = rOut.Close()
		_ = wOut.Close()
		_ = rErr.Close()
		_ = wErr.Close()
		return err
	}

	if err := syscall.Dup2(int(wOut.Fd()), stdoutFd); err != nil {
		_ = syscall.Close(savedStdoutFd)
		_ = syscall.Close(savedStderrFd)
		_ = rOut.Close()
		_ = wOut.Close()
		_ = rErr.Close()
		_ = wErr.Close()
		return err
	}
	if err := syscall.Dup2(int(wErr.Fd()), stderrFd); err != nil {
		_ = syscall.Dup2(savedStdoutFd, stdoutFd)
		_ = syscall.Close(savedStdoutFd)
		_ = syscall.Close(savedStderrFd)
		_ = rOut.Close()
		_ = wOut.Close()
		_ = rErr.Close()
		_ = wErr.Close()
		return err
	}

	c.savedStdoutFd = savedStdoutFd
	c.savedStderrFd = savedStderrFd
	c.wOut = wOut
	c.wErr = wErr
	c.outBuf.Reset()
	c.errBuf.Reset()

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

// Stop stops the redirection, restores fd 1/2 to what they pointed at before Start, and
// returns the captured output once both drain goroutines have finished (i.e. seen EOF on their
// pipe).
func (c *Capturer) Stop() (stdoutStr string, stderrStr string, err error) {
	c.mu.Lock()
	savedStdoutFd, savedStderrFd := c.savedStdoutFd, c.savedStderrFd
	wOut, wErr := c.wOut, c.wErr
	c.mu.Unlock()

	// Restoring fd 1/2 before closing our pipe's write end matters: once nothing refers to
	// wOut/wErr's underlying file description, closing it is what signals EOF to drain's Read,
	// so the order here (restore, then close) mirrors Start's redirect-then-hand-off shape.
	_ = syscall.Dup2(savedStdoutFd, int(os.Stdout.Fd()))
	_ = syscall.Dup2(savedStderrFd, int(os.Stderr.Fd()))
	_ = syscall.Close(savedStdoutFd)
	_ = syscall.Close(savedStderrFd)

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
