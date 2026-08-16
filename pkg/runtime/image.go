package runtime

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
)

// imageOutput renders anything satisfying the standard image.Image interface as an inline PNG.
//
// image.Image is the only auto-detected rich type, and deliberately so: Go has no equivalent of
// Python's _repr_html_ convention -- each Jupyter kernel invented its own incompatible interface,
// so a gocell-specific one would be implemented by no third-party library. image.Image is the one
// hook the standard library already provides and everything already satisfies.
//
// Note what that does *not* reach: gonum/plot's Plot has no Bounds/At, being resolution-independent
// until given a size and format. That stays a two-line job on the cell's side
// (p.WriterTo(w, h, "png") into a buffer, then display.PNG) rather than a helper here, which would
// put gonum in gocell's own go.mod for every user.
func imageOutput(v any) (Output, bool) {
	img, ok := v.(image.Image)
	if !ok {
		return Output{}, false
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// Fall back to the plain rendering rather than losing the value entirely.
		return Output{}, false
	}

	b := img.Bounds()
	return Output{
		Data: Bundle{
			"image/png": base64.StdEncoding.EncodeToString(buf.Bytes()),
			// A frontend that cannot draw still shows something meaningful.
			"text/plain": fmt.Sprintf("<image %dx%d>", b.Dx(), b.Dy()),
		},
		Meta: map[string]any{
			"image/png": map[string]any{"width": b.Dx(), "height": b.Dy()},
		},
	}, true
}
