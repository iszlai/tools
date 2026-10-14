// Package expand finds the function, method or class enclosing a line of
// source, on both sides of a diff hunk, so the UI can show the hunk in the
// context of its whole enclosing declaration instead of just the changed
// lines (FR-8). It composes the language providers FR-14 and FR-39 already
// built rather than parsing anything itself: golang.Provider for ".go"
// files, treesitter.Provider for every language with a compiled grammar,
// and a fixed-size line window for everything else.
package expand

import (
	"bytes"
	"context"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// FallbackLines is how many lines of context the +/-N window gives on each
// side of the requested line when no provider can name an enclosing
// declaration: the language has no compiled grammar (FR-39 does not cover
// everything -- YAML, SQL, Dockerfiles, ...), or the line sits at file scope
// with nothing enclosing it at all.
const FallbackLines = 15

// goProvider and tsProvider are package-level: both are safe for concurrent
// use (golang.Provider's FunctionAt is a bare go/parser call; treesitter's
// caches per file internally behind a mutex, see treesitter.Provider), and
// sharing them lets a provider's grammar registry and per-file cache do
// their job across repeated expand requests instead of being rebuilt every
// time. golang.NewProvider(nil) is safe: FunctionAt never touches the
// loader argument (see golang.Provider's doc comment). treesitter.New("")
// is safe for FunctionAt/Supports too: repoRoot is only read by Callers/
// Callees's repo-wide name search, which this package never calls.
var (
	goProvider = golang.NewProvider(nil)
	tsProvider = treesitter.New("")
)

// FunctionAt returns the line range (1-based, inclusive) of the function,
// method or class member enclosing line in src at path, or the
// FallbackLines window around line, clamped to src's own bounds, when
// nothing does.
//
// path's extension picks the provider: ".go" goes through golang.Provider's
// go/parser-only FunctionAt (FR-14) -- no type checking, matching FR-8's
// brief, and it already does exactly what FR-8 asks for a closure: it walks
// only file.Decls (see golang.declsFromFile), so it never considers a func
// literal a candidate in the first place -- a line inside a closure simply
// resolves to its innermost *enclosing named* func or method declaration,
// with no special-casing needed here. Receivers, generics (type params
// parse as an ordinary part of the FuncDecl/receiver syntax) and multiple
// top-level decls all fall out of that same, already-tested path.
//
// Every other language with a compiled grammar goes through
// treesitter.Provider (FR-39) instead, whose FunctionAt deliberately does
// treat a func literal as a closer candidate than its enclosing named
// function (gh-view's approach, which FR-39 followed): a JS/TS callback body
// is often the only function worth expanding, and that tier has no
// "named declarations only" rule to begin with. A path with no grammar in
// either tier, or a line that is not inside anything either provider
// recognizes (e.g. a blank line between top-level declarations), gets the
// fallback window instead of an error: "show me more around this line" is
// still a reasonable thing to ask for even without structure.
func FunctionAt(ctx context.Context, path string, src []byte, line int) (model.Range, error) {
	var sym *model.CodeSymbol
	var err error
	switch {
	case lang.Detect(path) == "go":
		sym, err = goProvider.FunctionAt(ctx, path, src, line)
	case tsProvider.Supports(path):
		sym, err = tsProvider.FunctionAt(ctx, path, src, line)
	}
	if err != nil {
		return model.Range{}, err
	}
	if sym != nil {
		return sym.Range, nil
	}
	return fallbackRange(src, line), nil
}

// fallbackRange is the +/-FallbackLines window around line, clamped to
// src's own line count so it never claims a line that does not exist.
func fallbackRange(src []byte, line int) model.Range {
	total := lineCount(src)
	if total == 0 {
		return model.Range{}
	}
	if line < 1 {
		line = 1
	}
	start, end := line-FallbackLines, line+FallbackLines
	if start < 1 {
		start = 1
	}
	if end > total {
		end = total
	}
	if end < start {
		end = start
	}
	return model.Range{StartLine: start, EndLine: end}
}

// lineCount is the number of lines src has, counting a final partial line
// (no trailing newline) as one more -- the same convention go/token's
// FileSet uses for a file's last line.
func lineCount(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	n := bytes.Count(src, []byte("\n"))
	if !bytes.HasSuffix(src, []byte("\n")) {
		n++
	}
	return n
}

// Hunk is one diff hunk's enclosing-declaration range on each side.
// OldRange/NewRange are the zero model.Range when that side has nothing to
// expand into (ForHunk was given no source for it, e.g. the file was added
// or deleted) -- Align below treats a zero range as "no context on this
// side" rather than a real, empty range at line 0.
type Hunk struct {
	OldRange model.Range
	NewRange model.Range
}

// ForHunk locates h's enclosing declaration on each side. oldSrc/newSrc are
// the full file content on that side -- git Show at the diff's old/new
// revision, or the worktree file for an uncommitted new side -- and may be
// nil for a side that does not exist (an added file has no old side, a
// deleted file has no new side), in which case that side's Range is left
// zero rather than FunctionAt being asked to parse nothing.
//
// The anchor line on each side is simply h.OldStart/h.NewStart: parseHunk
// (internal/core/diff) starts both of its running line counters there and
// only advances a side's counter when a line actually belonging to that
// side is consumed, so the first line of the hunk that has a line number at
// all on a given side always carries exactly that side's Start -- even when
// the hunk opens with lines that are pure additions or deletions on the
// other side.
func ForHunk(ctx context.Context, path string, h model.DiffHunk, oldSrc, newSrc []byte) (Hunk, error) {
	var out Hunk
	if oldSrc != nil {
		r, err := FunctionAt(ctx, path, oldSrc, h.OldStart)
		if err != nil {
			return Hunk{}, err
		}
		out.OldRange = r
	}
	if newSrc != nil {
		r, err := FunctionAt(ctx, path, newSrc, h.NewStart)
		if err != nil {
			return Hunk{}, err
		}
		out.NewRange = r
	}
	return out, nil
}

// Context is one line unchanged between the old and new side, carried as a
// pair so a context row can show both line numbers.
type Context struct {
	OldLine int
	NewLine int
}

// Align pairs the unchanged lines around h -- within oldRange/newRange,
// which come from ForHunk -- into aligned old/new line pairs for context
// rows before and after h's own body. hunks is every hunk of h's file, in
// order, used to stop short of a neighboring hunk that also falls inside
// the same enclosing declaration: a line actually touched by another hunk
// is not identical on both sides, so it must never be rendered as if it
// were.
//
// The pairing itself assumes the prefix (the declaration's start up to h's
// own start) and the suffix (h's own end up to the declaration's end) are
// byte-identical text on both sides -- true exactly because nothing other
// than h (or a clipped-out neighboring hunk) changed there -- so the i-th
// line before h on the old side is the i-th line before it on the new side,
// and likewise after. oldRange or newRange being the zero Range (ForHunk's
// "no source on this side") yields no pairs at all: there is nothing to
// align a side that does not exist against.
func Align(h model.DiffHunk, hunks []model.DiffHunk, oldRange, newRange model.Range) (before, after []Context) {
	if !validRange(oldRange) || !validRange(newRange) {
		return nil, nil
	}
	prev, next := neighbors(h, hunks)

	beforeOldStart, beforeNewStart := oldRange.StartLine, newRange.StartLine
	if prev != nil {
		beforeOldStart = max(beforeOldStart, prev.OldStart+prev.OldLines)
		beforeNewStart = max(beforeNewStart, prev.NewStart+prev.NewLines)
	}
	before = pairRange(beforeOldStart, h.OldStart-1, beforeNewStart, h.NewStart-1)

	afterOldEnd, afterNewEnd := oldRange.EndLine, newRange.EndLine
	if next != nil {
		afterOldEnd = min(afterOldEnd, next.OldStart-1)
		afterNewEnd = min(afterNewEnd, next.NewStart-1)
	}
	after = pairRange(h.OldStart+h.OldLines, afterOldEnd, h.NewStart+h.NewLines, afterNewEnd)

	return before, after
}

func validRange(r model.Range) bool {
	return r.StartLine > 0 && r.EndLine >= r.StartLine
}

// pairRange pairs old [oldStart, oldEnd] with new [newStart, newEnd]
// line-by-line. The two spans are assumed equal length (see Align's doc
// comment on why); min defends against the two independently-computed
// FunctionAt results disagreeing by a line or two, in which case the
// shorter length wins and the extra line on the longer side is simply left
// out rather than mispaired with its neighbor.
func pairRange(oldStart, oldEnd, newStart, newEnd int) []Context {
	n := min(oldEnd-oldStart+1, newEnd-newStart+1)
	if n <= 0 {
		return nil
	}
	out := make([]Context, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Context{OldLine: oldStart + i, NewLine: newStart + i})
	}
	return out
}

// neighbors returns the hunks immediately before and after h in hunks
// (matched by ID, which is stable regardless of slice copies), or nil for
// either end h is at.
func neighbors(h model.DiffHunk, hunks []model.DiffHunk) (prev, next *model.DiffHunk) {
	for i := range hunks {
		if hunks[i].ID != h.ID {
			continue
		}
		if i > 0 {
			p := hunks[i-1]
			prev = &p
		}
		if i+1 < len(hunks) {
			n := hunks[i+1]
			next = &n
		}
		return prev, next
	}
	return nil, nil
}
