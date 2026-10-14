package render

import (
	"strings"
	"testing"
)

func TestContextRows(t *testing.T) {
	oldSrc := []byte("func F() {\n\tfoo()\n\tbar()\n\tbaz()\n}\n")
	newSrc := []byte("func F() {\n\tfoo()\n\tBAR()\n\tbaz()\n}\n")

	rows := ContextRows("x.go", "h1", oldSrc, newSrc, []int{1, 2}, []int{1, 2})
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	for i, r := range rows {
		if r.Kind != KindContext {
			t.Errorf("rows[%d].Kind = %q, want %q", i, r.Kind, KindContext)
		}
		if !r.Expanded {
			t.Errorf("rows[%d].Expanded = false, want true", i)
		}
		if r.HunkID != "h1" {
			t.Errorf("rows[%d].HunkID = %q, want h1", i, r.HunkID)
		}
		if r.OldPosition != 0 || r.NewPosition != 0 {
			t.Errorf("rows[%d] positions = %d/%d, want 0/0", i, r.OldPosition, r.NewPosition)
		}
	}
	if rows[0].OldNo != 1 || rows[0].NewNo != 1 {
		t.Errorf("rows[0] line numbers = %d/%d, want 1/1", rows[0].OldNo, rows[0].NewNo)
	}
	if rows[1].OldNo != 2 || rows[1].NewNo != 2 {
		t.Errorf("rows[1] line numbers = %d/%d, want 2/2", rows[1].OldNo, rows[1].NewNo)
	}
	if !strings.Contains(rows[1].OldHTML, "foo") {
		t.Errorf("rows[1].OldHTML = %q, want it to contain %q", rows[1].OldHTML, "foo")
	}
}

func TestContextRows_MultiLineTokenAcrossRequestedLines(t *testing.T) {
	// A block comment spanning lines 1-3; only line 2 is requested. Tokenizing
	// line-by-line would see an unterminated "/*" on its own and highlight it
	// wrong; tokenizing the whole stream first (as ContextRows does) keeps it
	// correct regardless of which line is sliced out afterward.
	src := []byte("/* start\n   middle\n   end */\nfunc F() {}\n")
	rows := ContextRows("x.go", "h1", src, src, []int{2}, []int{2})
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if !strings.Contains(rows[0].OldHTML, "middle") {
		t.Errorf("OldHTML = %q, want it to contain %q", rows[0].OldHTML, "middle")
	}
}

func TestContextRows_Empty(t *testing.T) {
	rows := ContextRows("x.go", "h1", nil, nil, nil, nil)
	if len(rows) != 0 {
		t.Fatalf("len(rows) = %d, want 0", len(rows))
	}
}
