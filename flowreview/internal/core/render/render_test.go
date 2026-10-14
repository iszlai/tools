package render

import (
	"strings"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// hunk builds a model.DiffHunk for tests that only care about Lines and ID:
// OldStart/NewStart/line counts are derived, matching what diff.Parse would
// have produced.
func hunk(id string, lines ...model.DiffLine) model.DiffHunk {
	return model.DiffHunk{ID: id, Lines: lines}
}

func ctx(old, new, pos int, text string) model.DiffLine {
	return model.DiffLine{Kind: model.LineContext, OldNo: old, NewNo: new, Position: pos, Text: text}
}

func del(old, pos int, text string) model.DiffLine {
	return model.DiffLine{Kind: model.LineDel, OldNo: old, Position: pos, Text: text}
}

func add(new, pos int, text string) model.DiffLine {
	return model.DiffLine{Kind: model.LineAdd, NewNo: new, Position: pos, Text: text}
}

func kinds(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Kind
	}
	return out
}

func eqStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestPairing covers pairBlock's cases: equal-count pairing, leftover
// deletions, leftover additions, pure add/del hunks, and that a context line
// between two change blocks keeps them from pairing across it.
func TestPairing(t *testing.T) {
	cases := []struct {
		name  string
		lines []model.DiffLine
		want  []string // row kinds, hunk row excluded
	}{
		{
			name:  "one del one add pairs",
			lines: []model.DiffLine{del(1, 1, "a"), add(1, 2, "b")},
			want:  []string{KindChange},
		},
		{
			name:  "two del one add: one change, one leftover del",
			lines: []model.DiffLine{del(1, 1, "a"), del(2, 2, "b"), add(1, 3, "c")},
			want:  []string{KindChange, KindDel},
		},
		{
			name:  "one del two add: one change, one leftover add",
			lines: []model.DiffLine{del(1, 1, "a"), add(1, 2, "b"), add(2, 3, "c")},
			want:  []string{KindChange, KindAdd},
		},
		{
			name:  "pure addition hunk",
			lines: []model.DiffLine{add(1, 1, "a"), add(2, 2, "b")},
			want:  []string{KindAdd, KindAdd},
		},
		{
			name:  "pure deletion hunk",
			lines: []model.DiffLine{del(1, 1, "a"), del(2, 2, "b")},
			want:  []string{KindDel, KindDel},
		},
		{
			name: "context splits two change blocks",
			lines: []model.DiffLine{
				del(1, 1, "a"), add(1, 2, "b"),
				ctx(2, 2, 3, "same"),
				del(3, 4, "c"), add(3, 5, "d"),
			},
			want: []string{KindChange, KindContext, KindChange},
		},
		{
			name:  "pure context hunk",
			lines: []model.DiffLine{ctx(1, 1, 1, "same")},
			want:  []string{KindContext},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := renderHunk(lexerFor("f.txt"), hunk("h", c.lines...))
			got := kinds(rows)
			if !eqStrings(got, c.want) {
				t.Errorf("kinds = %v, want %v", got, c.want)
			}
		})
	}
}

// TestPositions checks that every row's Old/NewPosition is exactly the
// model.DiffLine.Position of that side's input line -- the anchor FR-9 and
// the host publisher both key off.
func TestPositions(t *testing.T) {
	lines := []model.DiffLine{
		ctx(1, 1, 10, "same"),
		del(2, 11, "old"),
		add(2, 12, "new"),
		ctx(3, 3, 13, "same2"),
	}
	rows := renderHunk(lexerFor("f.txt"), hunk("h", lines...))
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (context, change, context)", len(rows))
	}

	ctxRow, changeRow, ctxRow2 := rows[0], rows[1], rows[2]
	if ctxRow.OldPosition != 10 || ctxRow.NewPosition != 10 {
		t.Errorf("first context row positions = %d/%d, want 10/10", ctxRow.OldPosition, ctxRow.NewPosition)
	}
	if changeRow.OldPosition != 11 || changeRow.NewPosition != 12 {
		t.Errorf("change row positions = %d/%d, want 11/12", changeRow.OldPosition, changeRow.NewPosition)
	}
	if ctxRow2.OldPosition != 13 || ctxRow2.NewPosition != 13 {
		t.Errorf("second context row positions = %d/%d, want 13/13", ctxRow2.OldPosition, ctxRow2.NewPosition)
	}
	if changeRow.OldNo != 2 || changeRow.NewNo != 2 {
		t.Errorf("change row line numbers = %d/%d, want 2/2", changeRow.OldNo, changeRow.NewNo)
	}
}

// TestIntralineRanges exercises diffRanges directly: identical text marks
// nothing, a one-word change marks only that word, whitespace-only changes
// still mark, and text past maxIntralineTokens is left unmarked rather than
// diffed.
func TestIntralineRanges(t *testing.T) {
	t.Run("identical text marks nothing", func(t *testing.T) {
		oldR, newR := diffRanges("same text", "same text")
		if oldR != nil || newR != nil {
			t.Errorf("got %v/%v, want nil/nil", oldR, newR)
		}
	})

	t.Run("one word change marks only that word", func(t *testing.T) {
		oldText, newText := "line two here", "line TWO here"
		oldR, newR := diffRanges(oldText, newText)
		if len(oldR) != 1 || len(newR) != 1 {
			t.Fatalf("got %d/%d ranges, want 1/1: %v/%v", len(oldR), len(newR), oldR, newR)
		}
		if got := oldText[oldR[0].Start:oldR[0].End]; got != "two" {
			t.Errorf("old marked text = %q, want %q", got, "two")
		}
		if got := newText[newR[0].Start:newR[0].End]; got != "TWO" {
			t.Errorf("new marked text = %q, want %q", got, "TWO")
		}
	})

	t.Run("whitespace only change still marks", func(t *testing.T) {
		oldR, newR := diffRanges("a b", "a  b")
		if len(oldR) == 0 && len(newR) == 0 {
			t.Error("whitespace-only change produced no marks")
		}
	})

	t.Run("past the token cap produces no marks", func(t *testing.T) {
		big := strings.Repeat("a ", maxIntralineTokens+10)
		oldR, newR := diffRanges(big, big+"b")
		if oldR != nil || newR != nil {
			t.Errorf("got %v/%v, want nil/nil past the cap", oldR, newR)
		}
	})
}

// TestHTMLEscaping checks that code containing HTML-significant characters
// never reaches the UI unescaped, in both a context line and a change row's
// intra-line-diffed sides.
func TestHTMLEscaping(t *testing.T) {
	script := `<script>alert(1)</script>`

	t.Run("context line", func(t *testing.T) {
		rows := renderHunk(lexerFor("f.txt"), hunk("h", ctx(1, 1, 1, script)))
		if strings.Contains(rows[0].OldHTML, "<script>") {
			t.Errorf("OldHTML contains an unescaped <script>: %s", rows[0].OldHTML)
		}
		if !strings.Contains(rows[0].OldHTML, "&lt;script&gt;") {
			t.Errorf("OldHTML missing escaped script tag: %s", rows[0].OldHTML)
		}
	})

	t.Run("change row, both sides", func(t *testing.T) {
		rows := renderHunk(lexerFor("f.txt"), hunk("h", del(1, 1, script), add(1, 2, script+"x")))
		row := rows[0]
		for _, html := range []string{row.OldHTML, row.NewHTML} {
			if strings.Contains(html, "<script>") {
				t.Errorf("html contains an unescaped <script>: %s", html)
			}
			if !strings.Contains(html, "&lt;script&gt;") {
				t.Errorf("html missing escaped script tag: %s", html)
			}
		}
	})

	t.Run("hunk header", func(t *testing.T) {
		row := hunkRow(model.DiffHunk{ID: "h", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Section: script})
		if strings.Contains(row.OldHTML, "<script>") {
			t.Errorf("hunk header contains an unescaped <script>: %s", row.OldHTML)
		}
	})
}
