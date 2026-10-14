package render

import (
	"bytes"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
)

// cssFormatter is a separate Formatter from the one lineHTML uses: WriteCSS
// needs WithClasses, same as formatter, but keeping it distinct avoids
// coupling the two call sites to the same option set in the future.
var cssFormatter = chromahtml.New(chromahtml.WithClasses(true))

var stylesheetOnce = sync.OnceValue(func() []byte {
	var buf bytes.Buffer
	buf.WriteString("@media (prefers-color-scheme: light) {\n")
	writeRules(&buf, styles.Get("github"))
	buf.WriteString("}\n@media (prefers-color-scheme: dark) {\n")
	writeRules(&buf, styles.Get("github-dark"))
	buf.WriteString("}\n")
	return buf.Bytes()
})

// writeRules writes style's per-token-type rules (".chroma .err { ... }" and
// so on -- the UI gives every highlighted cell the "chroma" class so these
// match), skipping the Background/PreWrapper rules WriteCSS also emits for
// a ".chroma" wrapper element's own background: flowreview's own
// styles.css owns code-cell backgrounds, so add/del row tinting shows
// through: chroma only supplies token colors here.
func writeRules(buf *bytes.Buffer, style *chroma.Style) {
	var raw bytes.Buffer
	_ = cssFormatter.WriteCSS(&raw, style)
	for _, line := range strings.Split(raw.String(), "\n") {
		if line == "" || strings.HasPrefix(line, "/* Background */") || strings.HasPrefix(line, "/* PreWrapper */") {
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
}

// Stylesheet is the CSS for lineHTML's output: rules from the "github"
// chroma style and from "github-dark", each behind its own
// prefers-color-scheme media query -- no JS theme toggle needed. The light
// rules must be scoped too, not merely overridden: github-dark leaves some
// classes github colors unset (.nx, plain identifiers, is #1f2328 in light),
// and those would leak near-black text onto the dark background. Computed
// once; callers must not mutate the returned slice.
func Stylesheet() []byte {
	return stylesheetOnce()
}
