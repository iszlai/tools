// Package render turns a parsed diff into side-by-side rows ready for the
// UI: one row per hunk header, context line, matched change, or unpaired
// addition/deletion, each side syntax-highlighted via chroma and, for a
// matched change, intra-line diffed.
package render

import (
	"fmt"
	"html"

	"github.com/alecthomas/chroma/v2"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// Row kinds. "change" is FR-6's own addition: a deletion paired with the
// addition that replaced it, shown on one line instead of two.
const (
	KindContext = "context"
	KindChange  = "change"
	KindAdd     = "add"
	KindDel     = "del"
	KindHunk    = "hunk"
)

// Row is one line of a side-by-side diff. OldHTML/NewHTML are ready to drop
// into a table cell: syntax highlighted, and for a "change" row, intra-line
// diffed with <mark class="x-add"|"x-del"> around the changed span. Old/New
// Position is that side's line's model.DiffLine.Position -- the GitHub diff
// position a published comment anchors to -- zero when the row has nothing
// on that side. Expanded marks a row FR-8's function expansion spliced in
// around a hunk rather than one File itself produced: it always carries
// both line numbers but zero for both positions, since it names no position
// in the diff a comment could anchor to (see ContextRows and FR-9's
// publishability check).
type Row struct {
	Kind        string `json:"kind"`
	OldNo       int    `json:"old_no,omitempty"`
	NewNo       int    `json:"new_no,omitempty"`
	OldHTML     string `json:"old_html,omitempty"`
	NewHTML     string `json:"new_html,omitempty"`
	HunkID      string `json:"hunk_id,omitempty"`
	OldPosition int    `json:"old_position,omitempty"`
	NewPosition int    `json:"new_position,omitempty"`
	Expanded    bool   `json:"expanded,omitempty"`
}

// File renders every hunk of f into side-by-side rows.
func File(f model.ChangedFile) []Row {
	lexer := lexerFor(f.Path())
	rows := make([]Row, 0, 8)
	for _, h := range f.Hunks {
		rows = append(rows, hunkRow(h))
		rows = append(rows, renderHunk(lexer, h)...)
	}
	return rows
}

func hunkRow(h model.DiffHunk) Row {
	text := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	if h.Section != "" {
		text += " " + h.Section
	}
	esc := html.EscapeString(text)
	return Row{Kind: KindHunk, HunkID: h.ID, OldHTML: esc, NewHTML: esc}
}

// line pairs a model.DiffLine with the chroma tokens already picked out for
// its side, captured as renderHunk walks the hunk in order so pairing never
// has to re-tokenize.
type line struct {
	dl     model.DiffLine
	tokens []chroma.Token
}

// renderHunk highlights each side of h as one stream -- so a multi-line
// comment or string tokenizes correctly -- then walks the hunk's lines in
// order, pairing each contiguous run of deletions and additions positionally
// into "change" rows (see pairBlock).
func renderHunk(lexer chroma.Lexer, h model.DiffHunk) []Row {
	var oldText, newText []string
	for _, l := range h.Lines {
		if l.OldNo != 0 {
			oldText = append(oldText, l.Text)
		}
		if l.NewNo != 0 {
			newText = append(newText, l.Text)
		}
	}
	oldToks := tokenizeLines(lexer, oldText)
	newToks := tokenizeLines(lexer, newText)

	var rows []Row
	var pendingDel, pendingAdd []line
	oldIdx, newIdx := 0, 0

	flush := func() {
		rows = append(rows, pairBlock(h.ID, pendingDel, pendingAdd)...)
		pendingDel, pendingAdd = nil, nil
	}

	for _, l := range h.Lines {
		switch l.Kind {
		case model.LineContext:
			flush()
			oldLine := line{dl: l, tokens: oldToks[oldIdx]}
			newLine := line{dl: l, tokens: newToks[newIdx]}
			oldIdx++
			newIdx++
			rows = append(rows, contextRow(h.ID, oldLine, newLine))
		case model.LineDel:
			pendingDel = append(pendingDel, line{dl: l, tokens: oldToks[oldIdx]})
			oldIdx++
		case model.LineAdd:
			pendingAdd = append(pendingAdd, line{dl: l, tokens: newToks[newIdx]})
			newIdx++
		}
	}
	flush()
	return rows
}

func contextRow(hunkID string, old, new_ line) Row {
	return Row{
		Kind:        KindContext,
		HunkID:      hunkID,
		OldNo:       old.dl.OldNo,
		NewNo:       new_.dl.NewNo,
		OldHTML:     lineHTML(old.tokens, nil, ""),
		NewHTML:     lineHTML(new_.tokens, nil, ""),
		OldPosition: old.dl.Position,
		NewPosition: new_.dl.Position,
	}
}

// pairBlock pairs a contiguous run of deletions and additions positionally:
// the i-th deletion against the i-th addition, as a "change" row. Whichever
// side has more keeps its leftover lines as unpaired "del"/"add" rows, in
// their original order -- which, since a unified diff always lists a
// block's deletions before its additions, keeps the rows this returns in
// the same order the hunk read them in.
func pairBlock(hunkID string, dels, adds []line) []Row {
	n := min(len(dels), len(adds))
	rows := make([]Row, 0, len(dels)+len(adds))
	for i := 0; i < n; i++ {
		rows = append(rows, changeRow(hunkID, dels[i], adds[i]))
	}
	for i := n; i < len(dels); i++ {
		rows = append(rows, delRow(hunkID, dels[i]))
	}
	for i := n; i < len(adds); i++ {
		rows = append(rows, addRow(hunkID, adds[i]))
	}
	return rows
}

func changeRow(hunkID string, del, add line) Row {
	oldRanges, newRanges := diffRanges(del.dl.Text, add.dl.Text)
	return Row{
		Kind:        KindChange,
		HunkID:      hunkID,
		OldNo:       del.dl.OldNo,
		NewNo:       add.dl.NewNo,
		OldHTML:     lineHTML(del.tokens, oldRanges, "x-del"),
		NewHTML:     lineHTML(add.tokens, newRanges, "x-add"),
		OldPosition: del.dl.Position,
		NewPosition: add.dl.Position,
	}
}

func delRow(hunkID string, del line) Row {
	return Row{
		Kind:        KindDel,
		HunkID:      hunkID,
		OldNo:       del.dl.OldNo,
		OldHTML:     lineHTML(del.tokens, nil, ""),
		OldPosition: del.dl.Position,
	}
}

func addRow(hunkID string, add line) Row {
	return Row{
		Kind:        KindAdd,
		HunkID:      hunkID,
		NewNo:       add.dl.NewNo,
		NewHTML:     lineHTML(add.tokens, nil, ""),
		NewPosition: add.dl.Position,
	}
}
