package runtime

import (
	"fmt"
	"reflect"
)

// Bundle maps a MIME type to its payload, mirroring the shape of Jupyter's `data` field exactly so
// no conversion is needed on the way to the socket. Binary payloads (images) are base64-encoded
// strings, as the protocol requires.
type Bundle map[string]any

// Output is one thing a cell chose to show: a Bundle plus the optional per-MIME metadata Jupyter
// carries alongside it. Meta is nil in the common case; it exists because that is where an image's
// display size lives (`{"image/png": {"width": 400}}`), which a bare Bundle cannot express.
type Output struct {
	Data Bundle
	Meta map[string]any
}

// Text builds the plain-text Output that every value falls back to.
func Text(s string) Output {
	return Output{Data: Bundle{"text/plain": s}}
}

// MIMEBundler is implemented by types that know how to render themselves. It is declared here,
// next to the type switch that consumes it, because the switch can only match the interface it was
// compiled against -- pkg/display re-exports it as an alias so notebook code never has to name this
// package.
type MIMEBundler interface {
	MIMEBundle() Output
}

// autoOutput renders a cell's last expression, preferring a rich representation when the value
// offers one and falling back to %#v -- which is what exploration wants, and why fmt.Stringer is
// deliberately *not* consulted: String() would quietly hide the structure %#v reveals.
func autoOutput(v any) Output {
	if isNilValue(v) {
		return Text(fmt.Sprintf("%#v", v))
	}

	if b, ok := v.(MIMEBundler); ok {
		return b.MIMEBundle()
	}
	if out, ok := imageOutput(v); ok {
		return out
	}

	return Text(fmt.Sprintf("%#v", v))
}

// isNilValue reports whether v holds a nil pointer, map, slice, channel, func or interface. A typed
// nil satisfies an interface -- a (*myImage)(nil) is a perfectly good image.Image as far as the type
// switch is concerned -- so without this guard the rich arms would match and then dereference it.
func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}
