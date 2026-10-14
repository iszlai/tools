package render

// Blob renders src as a single-sided, non-diff row list for FR-18's
// read-only file view: one Row per line of src, carrying only NewNo/NewHTML
// (Kind is always KindContext, so the UI's existing row styling applies with
// no add/del tinting -- a blob is not a diff), syntax highlighted through
// the same lexerFor/tokenizeLines/lineHTML path File and ContextRows already
// use. HunkID and both Positions are left zero: a blob row never anchors a
// publishable comment and belongs to no hunk.
func Blob(path string, src []byte) []Row {
	lexer := lexerFor(path)
	lines := splitSourceLines(src)
	toks := tokenizeLines(lexer, lines)

	rows := make([]Row, len(lines))
	for i := range lines {
		rows[i] = Row{
			Kind:    KindContext,
			NewNo:   i + 1,
			NewHTML: lineHTML(toks[i], nil, ""),
		}
	}
	return rows
}
