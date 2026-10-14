package render

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// formatter renders chroma tokens as classed HTML spans with no surrounding
// <pre>/<code> and no per-line wrapper spans -- just the spans for whatever
// tokens it's handed. That lets lineHTML call it once per unmarked line, or
// once per marked/unmarked run within a line, and wrap only the marked runs
// in <mark>.
var formatter = chromahtml.New(chromahtml.WithClasses(true), chromahtml.PreventSurroundingPre(true))

// anyStyle is the style formatter.Format's signature requires. It is never
// actually read: WithClasses makes every styling decision class-based
// (lineHTML emits no inline colors), so any non-nil style satisfies the
// call. The real colors come from Stylesheet, generated separately per style
// name for light and dark.
var anyStyle = styles.Get("github")

// lexerFor picks a lexer by filename, falling back to plain text so a file
// of an unknown language still renders -- just without coloring.
func lexerFor(path string) chroma.Lexer {
	l := lexers.Match(path)
	if l == nil {
		l = lexers.Fallback
	}
	return chroma.Coalesce(l)
}

// tokenizeLines highlights lines as a single stream -- joined by "\n" -- so
// a token spanning multiple lines (a block comment, a triple-quoted string)
// tokenizes correctly, then splits the result back into one token slice per
// line. The result always has exactly len(lines) entries: a line a lexer
// quirk drops becomes a single plain-text token, never a missing index.
func tokenizeLines(lexer chroma.Lexer, lines []string) [][]chroma.Token {
	if len(lines) == 0 {
		return nil
	}
	var perLine [][]chroma.Token
	if it, err := lexer.Tokenise(nil, strings.Join(lines, "\n")+"\n"); err == nil {
		perLine = chroma.SplitTokensIntoLines(it.Tokens())
	}
	out := make([][]chroma.Token, len(lines))
	for i, text := range lines {
		if i < len(perLine) {
			out[i] = trimTrailingNewline(perLine[i])
		} else {
			out[i] = []chroma.Token{{Type: chroma.Text, Value: text}}
		}
	}
	return out
}

// trimTrailingNewline removes the "\n" tokenizeLines' own join put at the end
// of every line's token stream (SplitTokensIntoLines keeps a line's
// terminator attached to it), so it never shows up as a literal line break
// inside a <pre>-styled table cell.
func trimTrailingNewline(tokens []chroma.Token) []chroma.Token {
	if len(tokens) == 0 {
		return tokens
	}
	last := tokens[len(tokens)-1]
	if !strings.HasSuffix(last.Value, "\n") {
		return tokens
	}
	out := make([]chroma.Token, len(tokens))
	copy(out, tokens)
	last.Value = strings.TrimSuffix(last.Value, "\n")
	if last.Value == "" {
		return out[:len(out)-1]
	}
	out[len(out)-1] = last
	return out
}

// byteRange is a half-open [Start, End) span of byte offsets into a line's
// text, as produced by diffRanges and consumed by lineHTML.
type byteRange struct{ Start, End int }

// lineHTML renders tokens -- one source line's tokens, from tokenizeLines --
// as classed HTML spans, wrapping the given byte ranges (sorted,
// non-overlapping, measured in the same byte offsets as the concatenated
// token values) in <mark class="{mark}">.
func lineHTML(tokens []chroma.Token, ranges []byteRange, mark string) string {
	if len(ranges) == 0 {
		return formatSegment(tokens)
	}
	var b strings.Builder
	for _, seg := range splitAtRanges(tokens, ranges) {
		segHTML := formatSegment(seg.tokens)
		if seg.marked {
			b.WriteString(`<mark class="`)
			b.WriteString(mark)
			b.WriteString(`">`)
			b.WriteString(segHTML)
			b.WriteString(`</mark>`)
		} else {
			b.WriteString(segHTML)
		}
	}
	return b.String()
}

// formatSegment renders tokens with formatter. The only way Format can fail
// is its io.Writer erroring, and strings.Builder's Write never does.
func formatSegment(tokens []chroma.Token) string {
	var buf strings.Builder
	_ = formatter.Format(&buf, anyStyle, chroma.Literator(tokens...))
	return buf.String()
}

// segment is a maximal run of token pieces that are all inside a marked
// range, or all outside every range.
type segment struct {
	tokens []chroma.Token
	marked bool
}

// splitAtRanges walks tokens in byte order, cutting any token that straddles
// a range boundary, and groups the resulting pieces into marked/unmarked
// segments so each can be formatted -- and, if marked, wrapped -- on its
// own.
func splitAtRanges(tokens []chroma.Token, ranges []byteRange) []segment {
	var segs []segment
	pos := 0
	ri := 0
	for _, t := range tokens {
		val := t.Value
		for len(val) > 0 {
			for ri < len(ranges) && ranges[ri].End <= pos {
				ri++
			}
			marked := ri < len(ranges) && pos >= ranges[ri].Start && pos < ranges[ri].End
			limit := pos + len(val)
			switch {
			case marked && ranges[ri].End < limit:
				limit = ranges[ri].End
			case !marked && ri < len(ranges) && ranges[ri].Start < limit:
				limit = ranges[ri].Start
			}
			n := limit - pos
			if n <= 0 {
				n = len(val) // unreachable in practice; never spin forever
			}
			piece := chroma.Token{Type: t.Type, Value: val[:n]}
			if len(segs) > 0 && segs[len(segs)-1].marked == marked {
				last := &segs[len(segs)-1]
				last.tokens = append(last.tokens, piece)
			} else {
				segs = append(segs, segment{tokens: []chroma.Token{piece}, marked: marked})
			}
			val = val[n:]
			pos += n
		}
	}
	return segs
}
