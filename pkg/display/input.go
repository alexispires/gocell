package display

import (
	"fmt"

	"github.com/alexispires/gocell/pkg/runtime"
)

// Input prompts the user and returns what they type.
//
//	name := display.Input("Your name: ")
//
// Reading os.Stdin directly -- fmt.Scanln, bufio.NewReader(os.Stdin) -- does not do this. A cell
// runs inside a kernel with no terminal attached, so there is nothing at the other end of file
// descriptor 0; Jupyter's answer is a message the kernel sends to the frontend, which is what this
// does. Python can hide that behind its own input() because it controls the function; Go's
// fmt.Scanln is a plain blocking read and there is no hook to put underneath it. Such a read gets
// an immediate EOF rather than hanging the kernel -- see session.Execute.
//
// Returns an empty string if the frontend cannot prompt (nbconvert, a headless run) or if the cell
// is interrupted while waiting. Use InputErr when the difference matters.
func Input(prompt string) string {
	value, _ := InputErr(prompt)
	return value
}

// Password prompts without echoing. The value still crosses the wire in clear -- this hides it from
// the room, not from the network or the notebook's own history.
func Password(prompt string) string {
	value, _ := runtime.Current().Input(prompt, true)
	return value
}

// InputErr is Input with the failure reason: no frontend attached, a frontend that declined to
// prompt, or an interrupted wait.
func InputErr(prompt string) (string, error) {
	return runtime.Current().Input(prompt, false)
}

// Inputf prompts with a formatted message.
func Inputf(format string, args ...any) string {
	return Input(fmt.Sprintf(format, args...))
}
