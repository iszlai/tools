package render

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// ContextRows renders the unchanged lines named by oldLines/newLines (equal-
// length, parallel: oldLines[i] pairs with newLines[i], as internal/core/
// expand's Align produces) as side-by-side Row values for FR-8's function
// expansion. Highlighting goes through the same lexerFor/tokenizeLines/
// lineHTML path File itself uses -- tokenizing each full side once, not
// line by line, so a token spanning multiple lines (a block comment, a
// triple-quoted string) still tokenizes correctly even though only a few of
// its lines are actually being shown. Every row is marked Expanded and
// carries no diff position: expansion is read-only context, never a
// publishable comment anchor (see Row's doc comment and FR-9).
func ContextRows(path, hunkID string, oldSrc, newSrc []byte, oldLines, newLines []int) []Row {
	lexer := lexerFor(path)
	oldTok := tokenizeLines(lexer, splitSourceLines(oldSrc))
	newTok := tokenizeLines(lexer, splitSourceLines(newSrc))

	rows := make([]Row, 0, len(oldLines))
	for i := range oldLines {
		rows = append(rows, Row{
			Kind:     KindContext,
			HunkID:   hunkID,
			OldNo:    oldLines[i],
			NewNo:    newLines[i],
			OldHTML:  lineHTML(tokensAt(oldTok, oldLines[i]), nil, ""),
			NewHTML:  lineHTML(tokensAt(newTok, newLines[i]), nil, ""),
			Expanded: true,
		})
	}
	return rows
}

// splitSourceLines splits raw file content into its lines (without their
// terminators), the indexing tokensAt expects: line n is splitSourceLines(src)[n-1].
func splitSourceLines(src []byte) []string {
	if len(src) == 0 {
		return nil
	}
	text := strings.TrimSuffix(string(src), "\n")
	return strings.Split(text, "\n")
}

// tokensAt returns tokLines[lineNo-1], or nil when lineNo is out of range
// (0, meaning "no line on this side", or past the content ContextRows was
// given) -- lineHTML renders a nil token slice as an empty string.
func tokensAt(tokLines [][]chroma.Token, lineNo int) []chroma.Token {
	if lineNo < 1 || lineNo > len(tokLines) {
		return nil
	}
	return tokLines[lineNo-1]
}
