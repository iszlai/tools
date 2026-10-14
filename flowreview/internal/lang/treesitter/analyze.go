package treesitter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"

	ts "github.com/tree-sitter/go-tree-sitter"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// fileAnalysis is everything derived from one parse: named definitions in
// source order, anonymous function-like ranges (closures), and call sites
// found anywhere in the file. Outline/FunctionAt use defs and closures;
// Callers/Callees use calls together with a repo-wide walk.
type fileAnalysis struct {
	defs     []model.CodeSymbol
	closures []model.CodeSymbol
	calls    []callSite
}

type callSite struct {
	name string
	line int // 1-based
	col  int // 1-based
}

// analyze parses src and caches the result keyed by path and content hash,
// so repeated Outline/FunctionAt/Callers/Callees calls on an unchanged file
// (including the repo-wide walk in Callers/Callees) reparse only once.
func (p *Provider) analyze(path, language string, src []byte) (fileAnalysis, error) {
	sum := sha256.Sum256(src)
	hash := hex.EncodeToString(sum[:])

	p.mu.Lock()
	if c, ok := p.cache[path]; ok && c.hash == hash {
		fa := c.fa
		p.mu.Unlock()
		return fa, nil
	}
	p.mu.Unlock()

	fa, err := parseAndAnalyze(path, language, src)
	if err != nil {
		return fileAnalysis{}, err
	}

	p.mu.Lock()
	p.cache[path] = &cachedFile{hash: hash, fa: fa}
	p.mu.Unlock()
	return fa, nil
}

// rawEntry is one query match, grouped by the capture without a "." suffix
// (the definition node itself) plus its optional name/receiver captures.
// See queries/*.scm for the capture-name convention.
type rawEntry struct {
	role         string
	node         ts.Node
	nameNode     *ts.Node
	receiverNode *ts.Node
}

func parseAndAnalyze(path, language string, src []byte) (fileAnalysis, error) {
	g, ok := registry[language]
	if !ok {
		return fileAnalysis{}, fmt.Errorf("treesitter: no grammar for %q", language)
	}

	parser := ts.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(g.language); err != nil {
		return fileAnalysis{}, err
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return fileAnalysis{}, fmt.Errorf("treesitter: parse failed for %s", path)
	}
	defer tree.Close()

	qc := ts.NewQueryCursor()
	defer qc.Close()

	var rawDefs, rawContainers, rawClosures []rawEntry
	var calls []callSite

	names := g.query.CaptureNames()
	matches := qc.Matches(g.query, tree.RootNode(), src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var e rawEntry
		hasRole := false
		for _, capture := range m.Captures {
			role, sub := splitCapture(names[capture.Index])
			n := capture.Node
			switch sub {
			case "":
				e.role, e.node, hasRole = role, n, true
			case "name":
				e.nameNode = &n
			case "receiver":
				e.receiverNode = &n
			}
		}
		if !hasRole {
			continue
		}
		switch e.role {
		case "container":
			rawContainers = append(rawContainers, e)
		case "closure":
			rawClosures = append(rawClosures, e)
		case "call":
			if e.nameNode == nil {
				continue
			}
			sp := e.nameNode.StartPosition()
			calls = append(calls, callSite{
				name: e.nameNode.Utf8Text(src),
				line: int(sp.Row) + 1,
				col:  int(sp.Column) + 1,
			})
		default:
			rawDefs = append(rawDefs, e)
		}
	}

	sort.Slice(rawDefs, func(i, j int) bool { return rawDefs[i].node.StartByte() < rawDefs[j].node.StartByte() })
	sort.Slice(rawClosures, func(i, j int) bool { return rawClosures[i].node.StartByte() < rawClosures[j].node.StartByte() })

	containers := buildContainers(rawDefs, rawContainers, src)

	defs := make([]model.CodeSymbol, 0, len(rawDefs))
	for _, e := range rawDefs {
		if e.nameNode == nil {
			continue
		}
		role, receiver := e.role, ""
		switch e.role {
		case "method", "constructor":
			if e.receiverNode != nil {
				receiver = e.receiverNode.Utf8Text(src)
			} else if name, ok := containers.find(e.node.StartByte(), e.node.EndByte()); ok {
				receiver = name
			}
		case "function":
			if g.reclassify {
				if name, ok := containers.find(e.node.StartByte(), e.node.EndByte()); ok {
					role, receiver = "method", name
				}
			}
		}
		kind := kindForRole(role)
		if kind == "" {
			continue
		}
		defs = append(defs, buildSymbol(path, language, kind, e.nameNode.Utf8Text(src), receiver, e.node))
	}

	closures := make([]model.CodeSymbol, 0, len(rawClosures))
	for _, e := range rawClosures {
		closures = append(closures, buildClosureSymbol(path, language, e.node))
	}

	return fileAnalysis{defs: defs, closures: closures, calls: calls}, nil
}

func splitCapture(name string) (role, sub string) {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return name, ""
}

func kindForRole(role string) model.SymbolKind {
	switch role {
	case "function":
		return model.SymFunc
	case "method", "constructor":
		return model.SymMethod
	case "class":
		return model.SymClass
	case "interface":
		return model.SymInterface
	default:
		return ""
	}
}

// containerSet holds enclosing-scope candidates (classes, interfaces, Rust
// impl blocks, ...) used only to attach a Receiver to a nested method; they
// are never emitted as Outline entries unless also captured as @class or
// @interface.
type containerSet []struct {
	name               string
	startByte, endByte uint
}

func buildContainers(rawDefs, rawContainers []rawEntry, src []byte) containerSet {
	var cs containerSet
	add := func(e rawEntry) {
		if e.nameNode == nil {
			return
		}
		cs = append(cs, struct {
			name               string
			startByte, endByte uint
		}{e.nameNode.Utf8Text(src), e.node.StartByte(), e.node.EndByte()})
	}
	for _, e := range rawContainers {
		add(e)
	}
	for _, e := range rawDefs {
		if e.role == "class" || e.role == "interface" {
			add(e)
		}
	}
	return cs
}

// find returns the name of the smallest container whose range contains
// [startByte, endByte), or ("", false) when none does.
func (cs containerSet) find(startByte, endByte uint) (string, bool) {
	best, bestSpan := -1, uint(0)
	for i, c := range cs {
		if c.startByte <= startByte && endByte <= c.endByte {
			span := c.endByte - c.startByte
			if best == -1 || span < bestSpan {
				best, bestSpan = i, span
			}
		}
	}
	if best == -1 {
		return "", false
	}
	return cs[best].name, true
}

func buildSymbol(path, language string, kind model.SymbolKind, name, receiver string, node ts.Node) model.CodeSymbol {
	id := path + "#" + name
	if receiver != "" {
		id = path + "#" + receiver + "." + name
	}
	sym := model.CodeSymbol{
		ID:       id,
		Name:     name,
		Kind:     kind,
		Language: language,
		Path:     path,
		Receiver: receiver,
		Range:    nodeRange(node),
	}
	if language == "go" {
		if r := []rune(name); len(r) > 0 {
			sym.Exported = unicode.IsUpper(r[0])
		}
	}
	return sym
}

func buildClosureSymbol(path, language string, node ts.Node) model.CodeSymbol {
	r := nodeRange(node)
	return model.CodeSymbol{
		ID:       fmt.Sprintf("%s#closure@%d:%d", path, r.StartLine, r.StartCol),
		Kind:     model.SymFunc,
		Language: language,
		Path:     path,
		Range:    r,
	}
}

func nodeRange(node ts.Node) model.Range {
	sp, ep := node.StartPosition(), node.EndPosition()
	return model.Range{
		StartLine: int(sp.Row) + 1,
		EndLine:   int(ep.Row) + 1,
		StartCol:  int(sp.Column) + 1,
		EndCol:    int(ep.Column) + 1,
	}
}
