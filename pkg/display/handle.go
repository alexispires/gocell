package display

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/alexispires/gocell/pkg/runtime"
)

// Handle is a spot in the notebook that can be rewritten. Show returns nothing; ShowUpdatable
// returns one of these, and every Update replaces what is on screen instead of appending below it.
//
//	bar := display.ShowUpdatable(display.Text("0%"))
//	for i := 1; i <= 100; i++ {
//	    bar.Update(display.Text(fmt.Sprintf("%d%%", i)))
//	}
//
// The handle outlives its cell: a later cell holding it can still rewrite that output, which is how
// one progress bar is driven from several cells.
type Handle struct {
	id string
}

// ShowUpdatable displays an Output and returns a handle to it.
func ShowUpdatable(o Output) *Handle {
	h := &Handle{id: newDisplayID()}
	runtime.Current().DisplayWithID(o, h.id, false)
	return h
}

// Update replaces what the handle is showing.
//
// A no-op where nothing publishes live -- gocell-repl has no notion of rewriting an output that has
// already scrolled past.
func (h *Handle) Update(o Output) {
	if h == nil {
		return
	}
	runtime.Current().DisplayWithID(o, h.id, true)
}

// ID is the display_id the frontend knows this output by.
func (h *Handle) ID() string {
	if h == nil {
		return ""
	}
	return h.id
}

func newDisplayID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Only reached if the OS entropy source fails; a fixed id still works for a single
		// output, it merely stops being unique.
		return "gocell-display"
	}
	return "gocell-" + hex.EncodeToString(b[:])
}
