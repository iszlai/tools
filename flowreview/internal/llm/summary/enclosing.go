package summary

import (
	"bytes"
	"context"
	"regexp"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// goProvider and tsProvider mirror internal/core/expand's package-level
// providers (same construction, same "safe to share" reasoning documented
// there): golang.Provider's FunctionAt is a bare go/parser call and
// treesitter.Provider caches per file behind its own mutex, so one instance
// serves every EnclosingFunction call across the process.
var (
	goProvider = golang.NewProvider(nil)
	tsProvider = treesitter.New("")
)

// EnclosingFunction returns the source text of the function, method or class
// member enclosing line in src at path, best effort: "" when path's language
// has no provider, or when line is not inside anything a provider
// recognizes. Unlike internal/core/expand.FunctionAt, there is no fallback
// line-window here -- prompt.Input's doc comment is explicit that an empty
// EnclosingFunction (not an arbitrary window of surrounding lines) is what
// "no LanguageProvider found one" means to the prompt.
func EnclosingFunction(ctx context.Context, path string, src []byte, line int) string {
	var sym *model.CodeSymbol
	var err error
	switch {
	case lang.Detect(path) == "go":
		sym, err = goProvider.FunctionAt(ctx, path, src, line)
	case tsProvider.Supports(path):
		sym, err = tsProvider.FunctionAt(ctx, path, src, line)
	}
	if err != nil || sym == nil {
		return ""
	}
	return textOfRange(src, sym.Range)
}

// textOfRange extracts r's lines (1-based, inclusive) from src, or "" if r is
// out of bounds.
func textOfRange(src []byte, r model.Range) string {
	if r.StartLine < 1 || r.EndLine < r.StartLine {
		return ""
	}
	lines := bytes.Split(src, []byte("\n"))
	start, end := r.StartLine-1, r.EndLine
	if start >= len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	return string(bytes.Join(lines[start:end], []byte("\n")))
}

// enclosingNameRE picks a best-effort symbol name off the first declaration
// line of an EnclosingFunction text, across the handful of keywords common to
// the languages internal/lang/treesitter and internal/lang/golang cover. Used
// only by Fallback's deterministic text, which is explicitly not supposed to
// be as reliable as a real symbol lookup -- a miss just means Fallback falls
// back further, to the file's base name (see Fallback).
var enclosingNameRE = regexp.MustCompile(`\b(?:func|function|def|class|interface|type)\s+\*?(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)

// enclosingName extracts that best-effort name, or "" if nothing matched.
func enclosingName(text string) string {
	m := enclosingNameRE.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}
