package display

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	"github.com/alexispires/gocell/pkg/runtime"
)

// JSON shows a value as a collapsible tree.
//
// Three representations go out together and the frontend picks, which is what the protocol is for.
// application/json reaches a native viewer where one exists -- JupyterLab's has search, path
// copying and virtualisation for large documents, none of which is worth reimplementing here, and
// it improves without gocell changing. text/html is the fallback for everywhere that has no such
// viewer: a static HTML export, nbviewer, a rendered .ipynb on GitHub. text/plain keeps the
// indented form for terminals and gocell-repl.
func JSON(v any) Output {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return runtime.Text(fmt.Sprintf("%#v", v))
	}

	// Round-trip through a generic value so the tree walks what a frontend would actually
	// receive, not the Go type, and so struct tags and custom marshallers are already applied.
	var generic any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		return runtime.Text(string(encoded))
	}

	return Output{
		Data: Bundle{
			"application/json": json.RawMessage(encoded),
			"text/html":        jsonTreeHTML(generic),
			"text/plain":       string(encoded),
		},
		// Honoured by JupyterLab's viewer: open the tree rather than show a collapsed root the
		// reader has to click before seeing anything.
		Meta: map[string]any{
			"application/json": map[string]any{"expanded": true},
		},
	}
}

const (
	jsonKeyColor    = "#00718C"
	jsonStringColor = "#0B7A5B"
	jsonNumberColor = "#8A4B00"
	jsonNullColor   = "#7A6E86"
	jsonPunctColor  = "#6A7A82"
)

func jsonTreeHTML(v any) string {
	var sb strings.Builder
	sb.WriteString(`<div style="font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:13px;line-height:1.5">`)
	writeJSONNode(&sb, "", v, true)
	sb.WriteString(`</div>`)
	return sb.String()
}

// writeJSONNode renders one node. Containers become <details> so they fold; scalars are inline.
func writeJSONNode(sb *strings.Builder, key string, v any, open bool) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		writeContainer(sb, key, "{", "}", len(t), open, func() {
			for _, k := range keys {
				writeJSONNode(sb, k, t[k], false)
			}
		})
	case []any:
		writeContainer(sb, key, "[", "]", len(t), open, func() {
			for i, item := range t {
				writeJSONNode(sb, strconv.Itoa(i), item, false)
			}
		})
	default:
		sb.WriteString(`<div style="padding-left:1.15em">`)
		writeKey(sb, key)
		sb.WriteString(scalarHTML(v))
		sb.WriteString(`</div>`)
	}
}

func writeContainer(sb *strings.Builder, key, openTok, closeTok string, n int, open bool, body func()) {
	sb.WriteString(`<details`)
	if open {
		sb.WriteString(` open`)
	}
	sb.WriteString(`><summary style="cursor:pointer;list-style:revert">`)
	writeKey(sb, key)
	fmt.Fprintf(sb, `<span style="color:%s">%s</span> <span style="color:%s">%d %s</span> <span style="color:%s">%s</span>`,
		jsonPunctColor, openTok, jsonNullColor, n, plural(n, itemNoun(openTok)), jsonPunctColor, closeTok)
	sb.WriteString(`</summary><div style="padding-left:0.9em;border-left:1px solid rgba(127,127,127,0.28);margin-left:0.35em">`)
	body()
	sb.WriteString(`</div></details>`)
}

func writeKey(sb *strings.Builder, key string) {
	if key == "" {
		return
	}
	fmt.Fprintf(sb, `<span style="color:%s">%s</span><span style="color:%s">: </span>`,
		jsonKeyColor, html.EscapeString(key), jsonPunctColor)
}

func scalarHTML(v any) string {
	switch t := v.(type) {
	case nil:
		return fmt.Sprintf(`<span style="color:%s">null</span>`, jsonNullColor)
	case string:
		return fmt.Sprintf(`<span style="color:%s">%q</span>`, jsonStringColor, html.EscapeString(t))
	case bool:
		return fmt.Sprintf(`<span style="color:%s">%t</span>`, jsonNumberColor, t)
	case float64:
		// Every JSON number decodes as float64; render whole ones without a trailing ".0".
		return fmt.Sprintf(`<span style="color:%s">%s</span>`, jsonNumberColor,
			strconv.FormatFloat(t, 'f', -1, 64))
	default:
		return html.EscapeString(fmt.Sprintf("%v", t))
	}
}

func itemNoun(openTok string) string {
	if openTok == "[" {
		return "item"
	}
	return "key"
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}
