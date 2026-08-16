package display_test

import (
	"encoding/base64"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"

	"github.com/alexispires/gocell/pkg/display"
	"github.com/alexispires/gocell/pkg/runtime"
)

// These live in pkg/display rather than pkg/runtime on purpose: CI excludes pkg/runtime from the
// test run (Go's coverage instrumentation there breaks the plugin ABI), so tests placed in it would
// silently never execute. Everything under test is reachable through the exported API anyway.
//
// Nothing here calls t.Parallel(): runtime.NewContext binds a package-level "current" Context, so
// concurrent tests would rebind it under each other.

func newCtx() *runtime.Context {
	return runtime.NewContext(runtime.NewRegistry(), runtime.NewTypeRegistry())
}

func plain(t *testing.T, out runtime.Output) string {
	t.Helper()
	s, ok := out.Data["text/plain"].(string)
	if !ok {
		t.Fatalf("Output carries no text/plain entry: %v", out.Data)
	}
	return s
}

// A type with no rich representation must render exactly as it did before rich display existed.
func TestAutoResultFallsBackToGoSyntax(t *testing.T) {
	ctx := newCtx()

	ctx.SetAutoResult(42)
	out, ok := ctx.TakeResult()
	if !ok || plain(t, out) != "42" {
		t.Fatalf("Expected %q, got %v (ok=%v)", "42", out.Data, ok)
	}

	type point struct{ X, Y int }
	ctx.SetAutoResult(point{1, 2})
	out, _ = ctx.TakeResult()
	if got := plain(t, out); !strings.Contains(got, "X:1") {
		t.Fatalf("Expected %%#v-style rendering, got %q", got)
	}

	// Only one MIME type: nothing should claim a richer representation it does not have.
	if len(out.Data) != 1 {
		t.Fatalf("Expected a text/plain-only bundle, got %v", out.Data)
	}
}

// fmt.Stringer is deliberately NOT consulted -- %#v shows structure, which is what exploration
// wants, and silently preferring String() would hide it.
type stringly struct{ N int }

func (s stringly) String() string { return "STRINGER" }

func TestStringerIsNotHijacked(t *testing.T) {
	ctx := newCtx()
	ctx.SetAutoResult(stringly{7})
	out, _ := ctx.TakeResult()
	if got := plain(t, out); got == "STRINGER" {
		t.Fatalf("String() must not win over %%#v, got %q", got)
	}
}

func TestImageIsDetectedAutomatically(t *testing.T) {
	ctx := newCtx()

	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	ctx.SetAutoResult(img)

	out, ok := ctx.TakeResult()
	if !ok {
		t.Fatal("Expected a result for an image.Image")
	}
	encoded, isString := out.Data["image/png"].(string)
	if !isString || encoded == "" {
		t.Fatalf("Expected a base64 image/png entry, got %v", out.Data)
	}
	if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
		t.Fatalf("image/png payload is not valid base64: %v", err)
	}
	if got := plain(t, out); got != "<image 4x3>" {
		t.Fatalf("Expected a text/plain fallback naming the size, got %q", got)
	}
	if out.Meta["image/png"] == nil {
		t.Fatalf("Expected display-size metadata, got %v", out.Meta)
	}
}

// A typed nil satisfies image.Image -- the interface is non-nil while the value inside is -- so
// without a guard the image arm matches and then dereferences nil.
func TestTypedNilDoesNotPanic(t *testing.T) {
	ctx := newCtx()

	var img *image.RGBA
	ctx.SetAutoResult(img)

	out, ok := ctx.TakeResult()
	if !ok {
		t.Fatal("Expected a result even for a nil image")
	}
	if _, isImage := out.Data["image/png"]; isImage {
		t.Fatalf("A nil image must not be encoded, got %v", out.Data)
	}
}

type bundled struct{}

func (bundled) MIMEBundle() display.Output { return display.HTML("<b>custom</b>") }

func TestMIMEBundlerWins(t *testing.T) {
	ctx := newCtx()
	ctx.SetAutoResult(bundled{})
	out, _ := ctx.TakeResult()
	if out.Data["text/html"] != "<b>custom</b>" {
		t.Fatalf("Expected the type's own bundle, got %v", out.Data)
	}
}

func TestShowAppendsInOrder(t *testing.T) {
	ctx := newCtx()

	display.Show(display.HTML("<p>one</p>"))
	display.Show(display.Markdown("**two**"))

	shown := ctx.TakeDisplays()
	if len(shown) != 2 {
		t.Fatalf("Expected 2 displays, got %d", len(shown))
	}
	if shown[0].Data["text/html"] != "<p>one</p>" || shown[1].Data["text/markdown"] != "**two**" {
		t.Fatalf("Displays out of order or wrong: %v", shown)
	}
	if len(ctx.TakeDisplays()) != 0 {
		t.Fatal("TakeDisplays must drain the queue")
	}
}

// Show is a no-op rather than a panic when no session exists, so a plugin linked outside a kernel
// stays safe.
func TestShowWithoutSessionDoesNotPanic(t *testing.T) {
	display.Show(display.Text("orphan"))
}

// A goroutine started by one cell and still running can call Show at the exact moment the kernel
// drains -- gocell's headline behaviour, and a real data race without the Context mutex. Fails
// under -race on an unguarded implementation.
func TestConcurrentShowIsRaceFree(t *testing.T) {
	ctx := newCtx()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				display.Show(display.Text("from a background goroutine"))
			}
		}
	}()

	for i := 0; i < 200; i++ {
		ctx.TakeDisplays()
		ctx.SetAutoResult(i)
		ctx.TakeResult()
	}

	close(stop)
	wg.Wait()
}

// JSON ships three representations because no single one works everywhere: JupyterLab has a native
// application/json viewer, other frontends have none and would show flat text, and the <details>
// tree covers that middle case without JavaScript.
func TestJSONShipsACollapsibleTree(t *testing.T) {
	out := display.JSON(map[string]any{
		"name":   "gocell",
		"nested": map[string]any{"depth": 2, "ok": true, "none": nil},
		"list":   []any{1, 2},
	})

	// All three go out and the frontend chooses: a native viewer where one exists, our tree where
	// none does, plain text in a terminal.
	for _, mime := range []string{"application/json", "text/html", "text/plain"} {
		if _, ok := out.Data[mime]; !ok {
			t.Errorf("missing %s in %v", mime, out.Data)
		}
	}

	html, _ := out.Data["text/html"].(string)
	if !strings.Contains(html, "<details") || !strings.Contains(html, "<summary") {
		t.Fatalf("expected a <details>/<summary> tree, got %q", html)
	}
	// Containers fold; scalars do not. Root object + nested object + list = 3.
	if got := strings.Count(html, "<details"); got != 3 {
		t.Fatalf("expected one <details> per container, got %d", got)
	}
	if !strings.Contains(html, `"gocell"`) || !strings.Contains(html, "null") {
		t.Fatalf("expected scalars rendered, got %q", html)
	}

}

// A value carrying HTML must not be able to inject markup into the tree.
func TestJSONEscapesHTML(t *testing.T) {
	out := display.JSON(map[string]any{"<script>": "</summary><img src=x>"})
	html, _ := out.Data["text/html"].(string)
	if strings.Contains(html, "<script>") || strings.Contains(html, "<img src=x>") {
		t.Fatalf("unescaped markup leaked into the tree: %q", html)
	}
}
