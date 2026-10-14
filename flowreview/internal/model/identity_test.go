package model

import "testing"

func lines(spec ...string) []DiffLine {
	out := make([]DiffLine, 0, len(spec))
	for i, s := range spec {
		l := DiffLine{Text: s[1:], Position: i + 1}
		switch s[0] {
		case '+':
			l.Kind, l.NewNo = LineAdd, 10+i
		case '-':
			l.Kind, l.OldNo = LineDel, 10+i
		default:
			l.Kind, l.OldNo, l.NewNo = LineContext, 10+i, 10+i
		}
		out = append(out, l)
	}
	return out
}

func shift(ls []DiffLine, by int) []DiffLine {
	out := make([]DiffLine, len(ls))
	for i, l := range ls {
		if l.OldNo != 0 {
			l.OldNo += by
		}
		if l.NewNo != 0 {
			l.NewNo += by
		}
		l.Position += by
		out[i] = l
	}
	return out
}

func TestHunkID(t *testing.T) {
	base := lines(" func f() {", "-\treturn 1", "+\treturn 2", " }")
	id := HunkID("a/f.go", base)

	cases := []struct {
		name  string
		path  string
		lines []DiffLine
		same  bool
	}{
		{"identical", "a/f.go", base, true},
		{"line shift only", "a/f.go", shift(base, 40), true},
		{"CRLF only", "a/f.go", lines(" func f() {\r", "-\treturn 1\r", "+\treturn 2\r", " }\r"), true},
		{"rename only", "b/f.go", base, false},
		{"whitespace only", "a/f.go", lines(" func f() {", "-\treturn 1", "+\treturn  2", " }"), false},
		{"content change", "a/f.go", lines(" func f() {", "-\treturn 1", "+\treturn 3", " }"), false},
		{"marker change", "a/f.go", lines(" func f() {", " \treturn 1", "+\treturn 2", " }"), false},
	}
	for _, c := range cases {
		got := HunkID(c.path, c.lines)
		if (got == id) != c.same {
			t.Errorf("%s: same=%v, want same=%v", c.name, got == id, c.same)
		}
	}

	noNL := lines(" func f() {", "-\treturn 1", "+\treturn 2", " }")
	noNL[3].NoNewline = true
	if HunkID("a/f.go", noNL) == id {
		t.Error("a missing final newline is content and must change the id")
	}
}

func TestSummaryKey(t *testing.T) {
	k := SummaryKey("m", "v1", "body", "func", "ctx")
	if k != SummaryKey("m", "v1", "body", "func", "ctx") {
		t.Fatal("not deterministic")
	}
	for _, other := range []string{
		SummaryKey("m2", "v1", "body", "func", "ctx"),
		SummaryKey("m", "v2", "body", "func", "ctx"),
		SummaryKey("m", "v1", "body2", "func", "ctx"),
		SummaryKey("m", "v1", "body", "func2", "ctx"),
		SummaryKey("m", "v1", "body", "func", "ctx2"),
		SummaryKey("m", "v1", "bodyf", "unc", "ctx"), // boundary shift
	} {
		if other == k {
			t.Errorf("collision: %s", other)
		}
	}
}

func TestFileID(t *testing.T) {
	if FileID("a", "b") == FileID("b", "a") {
		t.Error("old/new order must matter")
	}
	if FileID("ab", "") == FileID("a", "b") {
		t.Error("boundary collision")
	}
	if len(FileID("a", "b")) != 12 {
		t.Error("want 12 hex chars")
	}
}
