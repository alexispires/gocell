// Package display is what notebook cells import to show something other than plain text: images,
// HTML, Markdown, JSON.
//
//	display.Show(display.HTML("<b>hello</b>"))
//
// A cell's last bare expression is still auto-displayed without any call here, and an image.Image
// is rendered inline automatically. Everything else is explicit, on purpose -- Go has no equivalent
// of Python's _repr_html_ convention for third-party types to opt into.
//
// The richer MIME types only mean something to a Jupyter frontend; gocell-repl prints the
// text/plain entry of whatever is shown.
package display

import (
	"encoding/base64"
	"fmt"

	"github.com/alexispires/gocell/pkg/runtime"
)

// Bundle and Output are aliases, not new definitions: the type switch that consumes them lives in
// pkg/runtime and can only match the types it was compiled against. Aliasing lets a notebook type
// write `MIMEBundle() display.Output` without importing pkg/runtime directly.
type (
	Bundle = runtime.Bundle
	Output = runtime.Output
)

// MIMEBundler is implemented by types that render themselves. Declaring it on a type declared in a
// cell makes that type display richly wherever it appears, including as a bare last expression.
type MIMEBundler = runtime.MIMEBundler

// Show publishes an Output to the notebook. It may be called any number of times per cell, and
// from a goroutine that outlives the cell that started it.
//
// A no-op when no session is running, so a cell compiled outside a kernel cannot panic here.
func Show(o Output) {
	runtime.Current().Display(o)
}

// PNG shows raw PNG bytes.
func PNG(data []byte) Output { return binary("image/png", data) }

// JPEG shows raw JPEG bytes.
func JPEG(data []byte) Output { return binary("image/jpeg", data) }

// GIF shows raw GIF bytes.
func GIF(data []byte) Output { return binary("image/gif", data) }

// SVG shows an SVG document. Unlike the raster formats it travels as text, not base64.
func SVG(svg string) Output {
	return Output{Data: Bundle{
		"image/svg+xml": svg,
		"text/plain":    "<svg image>",
	}}
}

// HTML shows rendered HTML.
func HTML(h string) Output {
	return Output{Data: Bundle{
		"text/html":  h,
		"text/plain": h,
	}}
}

// Markdown shows rendered Markdown.
func Markdown(md string) Output {
	return Output{Data: Bundle{
		"text/markdown": md,
		"text/plain":    md,
	}}
}

// Text shows plain text, the representation every frontend understands.
func Text(s string) Output { return runtime.Text(s) }

func binary(mime string, data []byte) Output {
	return Output{Data: Bundle{
		mime:         base64.StdEncoding.EncodeToString(data),
		"text/plain": fmt.Sprintf("<%s, %d bytes>", mime, len(data)),
	}}
}
