// Command gocell-repl is a standalone, Jupyter-free interactive shell for gocell: it drives the
// same pkg/session used by the Jupyter kernel, reading cells from stdin instead of ZMQ
// messages. A cell is submitted once its braces balance out, so multi-line func/type/if/for
// blocks can be typed naturally; single-line cells run as soon as you press Enter.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"gocell/pkg/session"
	"gocell/pkg/workspace"
)

func main() {
	wsMgr, err := workspace.NewManager("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize workspace: %v\n", err)
		os.Exit(1)
	}
	defer wsMgr.CleanUp()

	sess, err := session.New(wsMgr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("gocell - standalone Go REPL (Ctrl+D to exit)")

	var buf strings.Builder
	braceDepth := 0

	printPrompt := func() {
		if braceDepth > 0 {
			fmt.Print("...> ")
		} else {
			fmt.Print("gocell> ")
		}
	}

	printPrompt()
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		buf.WriteString(line)
		buf.WriteString("\n")

		if braceDepth > 0 {
			printPrompt()
			continue
		}

		code := buf.String()
		buf.Reset()
		braceDepth = 0

		if strings.TrimSpace(code) != "" {
			runCell(sess, code)
		}

		printPrompt()
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
