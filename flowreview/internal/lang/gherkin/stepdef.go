package gherkin

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// StepDef is one registered step definition: a compiled pattern, where it
// was registered, and the handler symbol id when it could be resolved.
type StepDef struct {
	ID        string         // synthetic, unique: "gherkin-stepdef:<path>:<line>"
	Keyword   string         // "Given" | "When" | "Then" | "And" | "But" | "" (godog's generic Step)
	Raw       string         // the pattern/expression text as written in source
	Pattern   *regexp.Regexp // Raw translated to an anchored regexp
	Location  model.Location
	HandlerID string // symbol id of the handler function, when resolvable
	Language  string // "go" | "java" | "javascript"
}

// repoIndex is everything discoverStepDefs needs from a single pass over the
// repo's Go/Java/JS/TS files: source text (read once) and, for Go and JS,
// named function/method definitions by name (used to resolve a step
// definition whose handler is a plain identifier).
type repoIndex struct {
	goFuncsByName map[string][]model.CodeSymbol
	jsDefsByName  map[string][]model.CodeSymbol
	srcByPath     map[string][]byte

	goPaths, javaPaths, jsPaths []string
}

func buildRepoIndex(ctx context.Context, root string, tsProv *treesitter.Provider) (*repoIndex, error) {
	ri := &repoIndex{
		goFuncsByName: map[string][]model.CodeSymbol{},
		jsDefsByName:  map[string][]model.CodeSymbol{},
		srcByPath:     map[string][]byte{},
	}
	err := walk(root, func(rel, language string, src []byte) error {
		switch language {
		case "go":
			ri.goPaths = append(ri.goPaths, rel)
		case "java":
			ri.javaPaths = append(ri.javaPaths, rel)
		case "javascript", "typescript", "tsx":
			ri.jsPaths = append(ri.jsPaths, rel)
		default:
			return nil
		}
		ri.srcByPath[rel] = src

		syms, err := tsProv.Outline(ctx, rel, src)
		if err != nil {
			return nil // unparsable file: skip it, don't abort the walk
		}
		switch language {
		case "go":
			for _, s := range syms {
				if s.Kind == model.SymFunc || s.Kind == model.SymMethod {
					ri.goFuncsByName[s.Name] = append(ri.goFuncsByName[s.Name], s)
				}
			}
		case "javascript", "typescript", "tsx":
			for _, s := range syms {
				if s.Kind == model.SymFunc || s.Kind == model.SymMethod {
					ri.jsDefsByName[s.Name] = append(ri.jsDefsByName[s.Name], s)
				}
			}
		}
		return nil
	})
	return ri, err
}

func discoverStepDefs(ctx context.Context, tsProv *treesitter.Provider, ri *repoIndex) ([]StepDef, error) {
	var defs []StepDef
	for _, p := range ri.goPaths {
		defs = append(defs, discoverGoStepDefs(p, ri.srcByPath[p], ri.goFuncsByName)...)
	}
	for _, p := range ri.javaPaths {
		defs = append(defs, discoverJavaStepDefs(ctx, tsProv, p, ri.srcByPath[p])...)
	}
	for _, p := range ri.jsPaths {
		defs = append(defs, discoverJSStepDefs(ctx, tsProv, p, ri.srcByPath[p], ri.jsDefsByName)...)
	}
	return defs, nil
}

func lineOf(src []byte, offset int) int {
	return 1 + bytes.Count(src[:offset], []byte("\n"))
}

func stepDefID(path string, line int) string {
	return fmt.Sprintf("gherkin-stepdef:%s:%d", path, line)
}

// --- Go: godog ctx.Step(`^...$`, fn) / s.Step(...) / sc.Given/When/Then(...) ---

// goStepCallRe matches "<ident>.(Step|Given|When|Then)(<pattern>, <handler>)"
// on one line: group 1 the method name, group 2 the pattern literal (a
// backtick-raw string or a double-quoted Go string), group 3 the handler
// expression. godog step patterns are always regex, never cucumber
// expressions, so the pattern is compiled as-is.
var goStepCallRe = regexp.MustCompile(
	`(?m)^[ \t]*[A-Za-z_][A-Za-z0-9_]*\.(Step|Given|When|Then)\(\s*(` +
		"`[^`]*`" +
		`|"(?:[^"\\]|\\.)*")\s*,\s*([A-Za-z_][A-Za-z0-9_.]*)\s*\)`,
)

func discoverGoStepDefs(path string, src []byte, byName map[string][]model.CodeSymbol) []StepDef {
	var out []StepDef
	for _, m := range goStepCallRe.FindAllSubmatchIndex(src, -1) {
		method := string(src[m[2]:m[3]])
		patLit := string(src[m[4]:m[5]])
		handlerExpr := string(src[m[6]:m[7]])
		line := lineOf(src, m[0])

		patText, err := unquoteGoPatternLiteral(patLit)
		if err != nil {
			continue
		}
		re, err := regexp.Compile(patText)
		if err != nil {
			continue
		}

		keyword := method
		if keyword == "Step" {
			keyword = ""
		}

		def := StepDef{
			ID:       stepDefID(path, line),
			Keyword:  keyword,
			Raw:      patText,
			Pattern:  re,
			Location: model.Location{Path: path, Line: line},
			Language: "go",
		}
		// Only a plain identifier is resolved (spec: "function name arg ->
		// path#Name"); a selector like suite.handler names a method value on
		// a variable, which needs type information this package doesn't have.
		if !strings.Contains(handlerExpr, ".") {
			if cands := byName[handlerExpr]; len(cands) == 1 {
				def.HandlerID = cands[0].ID
			}
		}
		out = append(out, def)
	}
	return out
}

func unquoteGoPatternLiteral(lit string) (string, error) {
	if strings.HasPrefix(lit, "`") {
		return strings.Trim(lit, "`"), nil
	}
	return strconv.Unquote(lit)
}

// --- Java: cucumber-jvm @Given("...")/@When/@Then/@And annotations ---

// javaAnnotationRe matches "@Given("pattern")" (and When/Then/And/But).
// Group 1 is the annotation name, group 2 the quoted pattern's inner text.
var javaAnnotationRe = regexp.MustCompile(
	`@(Given|When|Then|And|But)\(\s*"((?:[^"\\]|\\.)*)"\s*\)`,
)

func discoverJavaStepDefs(ctx context.Context, tsProv *treesitter.Provider, path string, src []byte) []StepDef {
	var out []StepDef
	for _, m := range javaAnnotationRe.FindAllSubmatchIndex(src, -1) {
		keyword := string(src[m[2]:m[3]])
		rawPat := string(src[m[4]:m[5]])
		line := lineOf(src, m[0])

		patText := unescapeQuoted(rawPat)
		re, err := compilePattern(patText)
		if err != nil {
			continue
		}

		def := StepDef{
			ID:       stepDefID(path, line),
			Keyword:  keyword,
			Raw:      patText,
			Pattern:  re,
			Location: model.Location{Path: path, Line: line},
			Language: "java",
		}
		// The annotation is inside the annotated method's own declaration
		// range (tree-sitter-java's method_declaration spans its modifiers,
		// which include annotations), so FunctionAt at the annotation's line
		// finds the method it decorates.
		if sym, ferr := tsProv.FunctionAt(ctx, path, src, line); ferr == nil && sym != nil {
			def.HandlerID = sym.ID
		}
		out = append(out, def)
	}
	return out
}

func unescapeQuoted(inner string) string {
	if s, err := strconv.Unquote(`"` + inner + `"`); err == nil {
		return s
	}
	return inner
}

// --- JS/TS: cucumber-js Given/When/Then/And/But(pattern, fn) ---

// jsStepCallRe matches "Given(<pattern>, <handler-head>" without requiring
// the call to close on the same line — an inline function's body commonly
// spans many lines before the call's closing ")", so only the start of the
// handler (identifier, "function", "async function" or an arrow head) is
// captured; that's enough to classify it and, for an inline function, to
// find its line.
var jsStepCallRe = regexp.MustCompile(
	`(?m)\b(Given|When|Then|And|But)\(\s*(/(?:[^/\\\n]|\\.)*/|'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*")\s*,\s*(async\s+function|function\b|\([^)]*\)\s*=>|[A-Za-z_$][A-Za-z0-9_$]*\s*=>|[A-Za-z_$][A-Za-z0-9_$.]*)`,
)

func discoverJSStepDefs(ctx context.Context, tsProv *treesitter.Provider, path string, src []byte, byName map[string][]model.CodeSymbol) []StepDef {
	var out []StepDef
	for _, m := range jsStepCallRe.FindAllSubmatchIndex(src, -1) {
		keyword := string(src[m[2]:m[3]])
		patLit := string(src[m[4]:m[5]])
		head := string(src[m[6]:m[7]])
		line := lineOf(src, m[0])

		patText, isRegex := unquoteJSPatternLiteral(patLit)
		var re *regexp.Regexp
		var err error
		if isRegex {
			re, err = regexp.Compile(patText)
		} else {
			re, err = cucumberExpressionToRegex(patText)
		}
		if err != nil {
			continue
		}

		def := StepDef{
			ID:       stepDefID(path, line),
			Keyword:  keyword,
			Raw:      patText,
			Pattern:  re,
			Location: model.Location{Path: path, Line: line},
			Language: "javascript",
		}
		switch {
		case isInlineJSHandler(head):
			if sym, ferr := tsProv.FunctionAt(ctx, path, src, line); ferr == nil && sym != nil {
				def.HandlerID = sym.ID
			}
		case !strings.Contains(head, "."):
			if cands := byName[head]; len(cands) == 1 {
				def.HandlerID = cands[0].ID
			}
		}
		out = append(out, def)
	}
	return out
}

func isInlineJSHandler(head string) bool {
	return strings.HasPrefix(head, "function") || strings.HasPrefix(head, "async") || strings.Contains(head, "=>")
}

// unquoteJSPatternLiteral strips a JS pattern literal's delimiters. A
// /regex/ literal is always a regex. A quoted string starting with "^" is
// treated as a classic cucumber-jvm/cucumber-js regex pattern; otherwise
// it's a cucumber expression.
func unquoteJSPatternLiteral(lit string) (text string, isRegex bool) {
	if strings.HasPrefix(lit, "/") {
		return lit[1 : len(lit)-1], true
	}
	inner := lit[1 : len(lit)-1]
	inner = strings.NewReplacer(`\\`, `\`, `\'`, `'`, `\"`, `"`).Replace(inner)
	return inner, strings.HasPrefix(inner, "^")
}

// compilePattern compiles a Java/JS quoted pattern: already a regex when it
// starts with "^" (the classic style), a cucumber expression otherwise.
func compilePattern(text string) (*regexp.Regexp, error) {
	if strings.HasPrefix(text, "^") {
		return regexp.Compile(text)
	}
	return cucumberExpressionToRegex(text)
}
