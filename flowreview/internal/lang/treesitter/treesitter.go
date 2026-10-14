// Package treesitter is the structural LanguageProvider: tree-sitter
// grammars give function/method/class ranges and names for every language
// with a grammar compiled in, without type-checking. Callers and callees are
// heuristic: a name search over files of the same language in the repo, the
// same approach gh-view uses. Adding a language is adding a grammar module
// and a query file in queries/; no other code changes.
package treesitter

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

//go:embed queries/*.scm
var queryFS embed.FS

// grammarSpec is a compiled grammar: its Language and the one query that
// captures definitions, containers, closures and calls (see queries/*.scm
// for the capture-name convention). reclassify is set for languages where
// the grammar uses the same node kind for free functions and methods
// (Python, Rust): a "function" capture nested in a container becomes a
// method.
type grammarSpec struct {
	language   *ts.Language
	query      *ts.Query
	reclassify bool
}

var (
	registryOnce sync.Once
	registry     map[string]*grammarSpec
)

// buildRegistry compiles every grammar and its query once per process.
// Language and Query are immutable after construction, so the registry is
// safe to share for concurrent reads; a fresh Parser and QueryCursor is
// created per parse, which is what makes the Provider safe for concurrent
// use despite tree-sitter parsers not being thread-safe themselves.
func buildRegistry() {
	registry = make(map[string]*grammarSpec, len(grammarPlugins))
	for _, p := range grammarPlugins {
		l := ts.NewLanguage(p.languagePtr())
		q, qerr := ts.NewQuery(l, mustLoadQuery(p.queryFile))
		if qerr != nil {
			// Defensive skip: a grammar whose query fails to compile is left
			// out of the registry rather than crashing every provider. Should
			// not happen for the shipped queries; see package tests.
			continue
		}
		registry[p.name] = &grammarSpec{language: l, query: q, reclassify: p.reclassify}
	}
}

func mustLoadQuery(name string) string {
	b, err := queryFS.ReadFile("queries/" + name)
	if err != nil {
		panic("treesitter: embedded query missing: " + name)
	}
	return string(b)
}

// Provider is the structural tree-sitter LanguageProvider for every
// language with a grammar in grammarPlugins, plus Gherkin via a
// hand-written line parser (no usable Go tree-sitter binding exists for
// it). One Provider is constructed per repo root; it is safe for
// concurrent use.
type Provider struct {
	repoRoot string

	mu    sync.Mutex
	cache map[string]*cachedFile // keyed by repo-relative path
}

type cachedFile struct {
	hash string
	fa   fileAnalysis
}

// New constructs a Provider rooted at repoRoot, used to resolve the
// repo-relative paths carried by model.CodeSymbol for Callers and Callees.
func New(repoRoot string) *Provider {
	registryOnce.Do(buildRegistry)
	return &Provider{repoRoot: repoRoot, cache: make(map[string]*cachedFile)}
}

const gherkinLanguage = "gherkin"

func (p *Provider) Capabilities() lang.Capabilities {
	langs := make([]string, 0, len(registry)+1)
	for name := range registry {
		langs = append(langs, name)
	}
	langs = append(langs, gherkinLanguage)
	sort.Strings(langs)
	return lang.Capabilities{
		Name:            "treesitter",
		Tier:            lang.TierStructural,
		Languages:       langs,
		Definitions:     true,
		Callers:         true,
		Callees:         true,
		Implementations: false,
		EntryPoints:     false,
	}
}

func (p *Provider) Supports(path string) bool {
	l := lang.Detect(path)
	if l == gherkinLanguage {
		return true
	}
	_, ok := registry[l]
	return ok
}

func (p *Provider) FunctionAt(ctx context.Context, path string, src []byte, line int) (*model.CodeSymbol, error) {
	language := lang.Detect(path)
	if language == gherkinLanguage {
		syms := parseGherkin(path, src)
		return innermost(filterKinds(syms, model.SymScenario, model.SymBackground, model.SymRule), line), nil
	}
	fa, err := p.analyze(path, language, src)
	if err != nil {
		return nil, err
	}
	candidates := make([]model.CodeSymbol, 0, len(fa.defs)+len(fa.closures))
	for _, d := range fa.defs {
		if d.Kind == model.SymFunc || d.Kind == model.SymMethod {
			candidates = append(candidates, d)
		}
	}
	candidates = append(candidates, fa.closures...)
	return innermost(candidates, line), nil
}

func (p *Provider) Outline(ctx context.Context, path string, src []byte) ([]model.CodeSymbol, error) {
	language := lang.Detect(path)
	if language == gherkinLanguage {
		return parseGherkin(path, src), nil
	}
	fa, err := p.analyze(path, language, src)
	if err != nil {
		return nil, err
	}
	out := make([]model.CodeSymbol, len(fa.defs))
	copy(out, fa.defs)
	return out, nil
}

func (p *Provider) Implementations(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error) {
	return nil, lang.ErrUnsupported
}

// IdentifierAt returns the identifier-like token at line/col (both 1-based)
// and its range, or ("", zero Range, nil) when there is none. It is not
// part of lang.LanguageProvider; callers that need the symbol under the
// cursor for navigation use this directly.
func (p *Provider) IdentifierAt(path string, src []byte, line, col int) (string, model.Range, error) {
	language := lang.Detect(path)
	if language == gherkinLanguage {
		lines := strings.Split(string(src), "\n")
		if line < 1 || line > len(lines) {
			return "", model.Range{}, nil
		}
		text := strings.TrimSpace(lines[line-1])
		if text == "" {
			return "", model.Range{}, nil
		}
		return text, model.Range{StartLine: line, EndLine: line}, nil
	}
	g, ok := registry[language]
	if !ok {
		return "", model.Range{}, fmt.Errorf("treesitter: no grammar for %q", language)
	}
	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(g.language); err != nil {
		return "", model.Range{}, err
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return "", model.Range{}, fmt.Errorf("treesitter: parse failed for %s", path)
	}
	defer tree.Close()

	point := ts.Point{Row: uint(line - 1), Column: uint(col - 1)}
	node := tree.RootNode().DescendantForPointRange(point, point)
	for node != nil && !strings.Contains(node.Kind(), "identifier") {
		node = node.Parent()
	}
	if node == nil {
		return "", model.Range{}, nil
	}
	sp, ep := node.StartPosition(), node.EndPosition()
	return node.Utf8Text(src), model.Range{
		StartLine: int(sp.Row) + 1,
		EndLine:   int(ep.Row) + 1,
		StartCol:  int(sp.Column) + 1,
		EndCol:    int(ep.Column) + 1,
	}, nil
}

func filterKinds(syms []model.CodeSymbol, kinds ...model.SymbolKind) []model.CodeSymbol {
	out := make([]model.CodeSymbol, 0, len(syms))
	for _, s := range syms {
		for _, k := range kinds {
			if s.Kind == k {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// innermost returns the symbol with the smallest line span containing line,
// or nil when none contains it. Used for FunctionAt (over defs + closures)
// and for finding the enclosing function at a heuristic call site.
func innermost(syms []model.CodeSymbol, line int) *model.CodeSymbol {
	best := -1
	bestSpan := 0
	for i, s := range syms {
		if s.Range.StartLine <= line && line <= s.Range.EndLine {
			span := s.Range.EndLine - s.Range.StartLine
			if best == -1 || span < bestSpan {
				best, bestSpan = i, span
			}
		}
	}
	if best == -1 {
		return nil
	}
	out := syms[best]
	return &out
}

var skipDirNames = map[string]bool{
	"vendor":       true,
	"node_modules": true,
	".git":         true,
}

// familyOf groups languages that may reasonably call into each other for
// the purposes of a repo-wide name search. TypeScript and TSX share a
// grammar's node shapes and commonly import across the .ts/.tsx boundary.
func familyOf(language string) string {
	if language == "typescript" || language == "tsx" {
		return "typescript"
	}
	return language
}

// walkRepo calls fn for every file under repoRoot whose detected language
// is in the same family as family, skipping vendor/node_modules/.git.
// Per-file read or parse errors are skipped rather than aborting the walk.
func walkRepo(repoRoot, family string, fn func(relPath, language string, src []byte)) error {
	return filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		language := lang.Detect(p)
		if language == "" || familyOf(language) != family {
			return nil
		}
		if _, ok := registry[language]; !ok {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		fn(rel, language, src)
		return nil
	})
}
