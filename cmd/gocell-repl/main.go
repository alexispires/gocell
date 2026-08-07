// Command gocell-repl is a standalone, Jupyter-free interactive shell for gocell: it drives the
// same pkg/session used by the Jupyter kernel, reading cells from stdin instead of ZMQ
// messages. A cell is submitted once its braces balance out, so multi-line func/type/if/for
// blocks can be typed naturally; single-line cells run as soon as you press Enter.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alexispires/gocell/pkg/compiler"
	"github.com/alexispires/gocell/pkg/session"
	"github.com/alexispires/gocell/pkg/workspace"
)

func main() {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize workspace: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = wsMgr.CleanUp() }()

	sess, err := session.New(wsMgr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("gocell - standalone Go REPL (Ctrl+D to exit)")

	// SIGINT interrupts the cell currently running (a stuck for{}), rather than killing the
	// process -- matching how the kernel handles it, and how a terminal-attached Jupyter
	// client's own Ctrl-C behaves for other kernels. SIGTERM is a real shutdown: unlike the
	// kernel's ctx.Done()-driven loop, the REPL's main goroutine is blocked in scanner.Scan()
	// on stdin, which a signal can't unblock -- so this runs the workspace cleanup and exits
	// explicitly here instead of relying on main's own deferred CleanUp, which an unhandled
	// SIGTERM would otherwise skip entirely (default Go behavior is immediate termination, no
	// deferred functions run).
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		for sig := range sigChan {
			if sig == syscall.SIGTERM {
				_ = wsMgr.CleanUp()
				os.Exit(0)
			}
			sess.Interrupt()
		}
	}()

	var buf strings.Builder

	printPrompt := func(continuation bool) {
		if continuation {
			fmt.Print("...> ")
		} else {
			fmt.Print("gocell> ")
		}
	}

	printPrompt(false)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteString("\n")

		// Token-aware, not a raw character count: a `{`/`}` sitting inside a string, rune
		// literal, or comment must not be mistaken for a real, still-open block -- e.g.
		// `fmt.Println("Result: {")` used to hang here forever waiting for a `}` that was
		// already there, textually, inside the string.
		if compiler.BraceDepth(buf.String()) > 0 {
			printPrompt(true)
			continue
		}

		code := buf.String()
		buf.Reset()

		if strings.TrimSpace(code) != "" {
			runCell(sess, code)
		}

		printPrompt(false)
	}
	fmt.Println()
}

func runCell(sess *session.Session, code string) {
	res, err := sess.Execute(code)

	if res.Stdout != "" {
		fmt.Print(res.Stdout)
	}
	if res.Stderr != "" {
		fmt.Fprint(os.Stderr, res.Stderr)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return
	}

	if res.HasDisplay {
		fmt.Println(res.DisplayText)
	}
}
