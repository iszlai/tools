package expand

import (
	"context"
	"strings"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// src joins lines with "\n" and a trailing newline, so a test can point at a
// line by its 1-based position in the call without counting characters.
func src(lines ...string) []byte {
	return []byte(strings.Join(lines, "\n") + "\n")
}

func TestFunctionAt_GoMethod(t *testing.T) {
	s := src(
		"package x",                     // 1
		"",                              // 2
		"type Stack struct{}",           // 3
		"",                              // 4
		"func (s *Stack) Push(v int) {", // 5
		"\tv++",                         // 6
		"}",                             // 7
	)
	sym, err := FunctionAt(context.Background(), "x.go", s, 6)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if sym.StartLine != 5 || sym.EndLine != 7 {
		t.Fatalf("Range = %+v, want {5 7}", sym)
	}
}

func TestFunctionAt_GoClosure(t *testing.T) {
	// A closure's line should resolve to the *named* enclosing function, not
	// the closure's own literal -- see golang.Provider's doc comment: it
	// only walks file.Decls, so a func literal is never itself a candidate.
	s := src(
		"package x",                 // 1
		"",                          // 2
		"func Outer() func() int {", // 3
		"\treturn func() int {",     // 4
		"\t\treturn 1",              // 5
		"\t}",                       // 6
		"}",                         // 7
	)
	sym, err := FunctionAt(context.Background(), "x.go", s, 5)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if sym == (model.Range{}) {
		t.Fatal("FunctionAt = zero Range, want Outer's range")
	}
	if sym.StartLine != 3 || sym.EndLine != 7 {
		t.Fatalf("Range = %+v, want {3 7} (Outer, not the closure)", sym)
	}
}

func TestFunctionAt_GoGeneric(t *testing.T) {
	s := src(
		"package x",                             // 1
		"",                                      // 2
		"type Stack[T any] struct{ items []T }", // 3
		"",                                      // 4
		"func Map[T, U any](in []T, f func(T) U) []U { // 5",
		"\tout := make([]U, len(in))",    // 6
		"\treturn out",                   // 7
		"}",                              // 8
		"",                               // 9
		"func (s *Stack[T]) Push(v T) {", // 10
		"\ts.items = append(s.items, v)", // 11
		"}",                              // 12
	)
	// generic function
	sym, err := FunctionAt(context.Background(), "x.go", s, 6)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if sym.StartLine != 5 || sym.EndLine != 8 {
		t.Fatalf("Range = %+v, want {5 8} (Map)", sym)
	}
	// generic method
	sym, err = FunctionAt(context.Background(), "x.go", s, 11)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if sym.StartLine != 10 || sym.EndLine != 12 {
		t.Fatalf("Range = %+v, want {10 12} (Stack[T].Push)", sym)
	}
}

func TestFunctionAt_Java(t *testing.T) {
	s := src(
		"class Greeter {",            // 1
		"  String name;",             // 2
		"",                           // 3
		"  String greet() {",         // 4
		"    return \"hi \" + name;", // 5
		"  }",                        // 6
		"}",                          // 7
	)
	r, err := FunctionAt(context.Background(), "Greeter.java", s, 5)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	// A tight {4 6} range (not the +/-15 fallback window) confirms this went
	// through treesitter.Provider and found Greeter.greet, not just that
	// line 5 landed somewhere plausible.
	if r.StartLine != 4 || r.EndLine != 6 {
		t.Errorf("Range = %+v, want {4 6}", r)
	}
}

func TestFunctionAt_JS(t *testing.T) {
	s := src(
		"function add(a, b) {", // 1
		"  return a + b;",      // 2
		"}",                    // 3
	)
	r, err := FunctionAt(context.Background(), "sample.js", s, 2)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if r.StartLine != 1 || r.EndLine != 3 {
		t.Errorf("Range = %+v, want {1 3}", r)
	}
}

func TestFunctionAt_FallbackNoGrammar(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "line"
	}
	s := src(lines...)
	// .sql has no tree-sitter grammar in the registry (FR-39 covers Go,
	// Java, JS, TS, Gherkin, YAML, Python, Rust -- not SQL).
	r, err := FunctionAt(context.Background(), "query.sql", s, 20)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	want := model.Range{StartLine: 5, EndLine: 35} // 20 +/- 15
	if r != want {
		t.Fatalf("Range = %+v, want %+v", r, want)
	}
}

func TestFunctionAt_FallbackClampedAtFileBounds(t *testing.T) {
	s := src("one", "two", "three")
	r, err := FunctionAt(context.Background(), "query.sql", s, 2)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	want := model.Range{StartLine: 1, EndLine: 3} // clamped, file only has 3 lines
	if r != want {
		t.Fatalf("Range = %+v, want %+v", r, want)
	}
}

func TestFunctionAt_FallbackNoEnclosingGoLine(t *testing.T) {
	// A blank top-level line between declarations has no enclosing Go
	// func/method/type either, so it falls back to the window too.
	lines := make([]string, 0, 40)
	lines = append(lines, "package x", "")
	for i := 0; i < 36; i++ {
		lines = append(lines, "// filler")
	}
	s := src(lines...)
	r, err := FunctionAt(context.Background(), "x.go", s, 2)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if r.StartLine != 1 {
		t.Errorf("StartLine = %d, want 1 (clamped)", r.StartLine)
	}
	if r.EndLine != 17 { // line 2 + 15
		t.Errorf("EndLine = %d, want 17", r.EndLine)
	}
}

// goFunc builds a small Go file with one function of n body lines (plus the
// signature and closing brace), each body line distinguishable by its
// index, for ForHunk/Align tests that need real before/after context.
func goFunc(n int, bodyPrefix string) []byte {
	lines := []string{"package x", "", "func F() {"}
	for i := 0; i < n; i++ {
		lines = append(lines, "\t"+bodyPrefix+itoa(i))
	}
	lines = append(lines, "}")
	return src(lines...)
}

func itoa(i int) string {
	// Avoid importing strconv for one call site; test-only.
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

func TestForHunk(t *testing.T) {
	oldSrc := goFunc(10, "old")
	newSrc := goFunc(10, "new")
	// Hunk touches the middle line of the function on both sides: old line
	// 8 (body index 4, 1-based line 4+4=8) becomes new line 8.
	h := model.DiffHunk{
		OldStart: 8, OldLines: 1,
		NewStart: 8, NewLines: 1,
		Lines: []model.DiffLine{
			{Kind: model.LineDel, OldNo: 8, Text: "\told4"},
			{Kind: model.LineAdd, NewNo: 8, Text: "\tnew4"},
		},
	}
	hr, err := ForHunk(context.Background(), "x.go", h, oldSrc, newSrc)
	if err != nil {
		t.Fatalf("ForHunk: %v", err)
	}
	wantRange := model.Range{StartLine: 3, EndLine: 14} // func F() { ... } spans 3..14 (3 header + 10 body + 1 brace)
	if hr.OldRange != wantRange {
		t.Errorf("OldRange = %+v, want %+v", hr.OldRange, wantRange)
	}
	if hr.NewRange != wantRange {
		t.Errorf("NewRange = %+v, want %+v", hr.NewRange, wantRange)
	}
}

func TestForHunk_NilSourceLeavesRangeZero(t *testing.T) {
	newSrc := goFunc(3, "new")
	h := model.DiffHunk{NewStart: 1, NewLines: 4}
	hr, err := ForHunk(context.Background(), "x.go", h, nil, newSrc)
	if err != nil {
		t.Fatalf("ForHunk: %v", err)
	}
	if hr.OldRange != (model.Range{}) {
		t.Errorf("OldRange = %+v, want zero (no old source)", hr.OldRange)
	}
	if hr.NewRange == (model.Range{}) {
		t.Error("NewRange = zero, want F's range")
	}
}

func TestAlign(t *testing.T) {
	// A function with 10 body lines (lines 4..13 inside "func F() {" at
	// line 3 and "}" at line 14), one hunk changing body line index 4 (file
	// line 8) on both sides -- same shape as TestForHunk.
	h := model.DiffHunk{ID: "h1", OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 1}
	funcRange := model.Range{StartLine: 3, EndLine: 14}

	before, after := Align(h, []model.DiffHunk{h}, funcRange, funcRange)
	if len(before) != 5 { // lines 3 ("func F() {"), 4, 5, 6, 7
		t.Fatalf("len(before) = %d, want 5: %+v", len(before), before)
	}
	for i, c := range before {
		want := 3 + i
		if c.OldLine != want || c.NewLine != want {
			t.Errorf("before[%d] = %+v, want {%d %d}", i, c, want, want)
		}
	}
	if len(after) != 6 { // lines 9..14 (14 is "}", also unchanged context)
		t.Fatalf("len(after) = %d, want 6: %+v", len(after), after)
	}
	for i, c := range after {
		want := 9 + i
		if c.OldLine != want || c.NewLine != want {
			t.Errorf("after[%d] = %+v, want {%d %d}", i, c, want, want)
		}
	}
}

func TestAlign_ClippedByNeighboringHunk(t *testing.T) {
	// Same function as TestAlign, but another hunk already touches line 5
	// (old) / line 5 (new) just above our hunk, and another touches line 11
	// just below it. Align must not claim those lines as unchanged context.
	target := model.DiffHunk{ID: "mid", OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 1}
	before := model.DiffHunk{ID: "before", OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1}
	afterH := model.DiffHunk{ID: "after", OldStart: 11, OldLines: 1, NewStart: 11, NewLines: 1}
	funcRange := model.Range{StartLine: 3, EndLine: 14}

	hunks := []model.DiffHunk{before, target, afterH}
	beforeCtx, afterCtx := Align(target, hunks, funcRange, funcRange)

	// "before" hunk covers old/new line 5, so context must start at line 6,
	// not line 4.
	if len(beforeCtx) != 2 { // lines 6, 7
		t.Fatalf("len(beforeCtx) = %d, want 2: %+v", len(beforeCtx), beforeCtx)
	}
	if beforeCtx[0].OldLine != 6 {
		t.Errorf("beforeCtx[0] = %+v, want OldLine 6", beforeCtx[0])
	}

	// "after" hunk covers line 11, so context must stop at line 10.
	if len(afterCtx) != 2 { // lines 9, 10
		t.Fatalf("len(afterCtx) = %d, want 2: %+v", len(afterCtx), afterCtx)
	}
	if afterCtx[0].OldLine != 9 || afterCtx[len(afterCtx)-1].OldLine != 10 {
		t.Errorf("afterCtx = %+v, want lines 9..10", afterCtx)
	}
}

func TestAlign_InvalidRangeYieldsNoPairs(t *testing.T) {
	h := model.DiffHunk{ID: "h1", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1}
	before, after := Align(h, []model.DiffHunk{h}, model.Range{}, model.Range{StartLine: 1, EndLine: 5})
	if before != nil || after != nil {
		t.Errorf("Align with a zero old range = (%v, %v), want (nil, nil)", before, after)
	}
}
