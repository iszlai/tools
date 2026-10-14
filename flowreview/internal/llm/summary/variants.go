package summary

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/llm"
)

// Variant is one prompt + schema combination the eval harness can compare
// (cmd/flowreview-eval --prompts). "v1" is Messages/Schema as shipped; the
// others are candidates. Whichever wins becomes the product prompt, with a
// PromptVersion bump.
type Variant struct {
	Name     string
	Messages func(Input) []llm.Message
	Schema   json.RawMessage
}

// Variants are the prompt candidates, by name.
var Variants = map[string]Variant{
	"v1":         {Name: "v1", Messages: Messages, Schema: Schema},
	"v2-rules":   {Name: "v2-rules", Messages: rulesMessages(false), Schema: Schema},
	"v3-hints":   {Name: "v3-hints", Messages: rulesMessages(true), Schema: Schema},
	"v4-fewshot": {Name: "v4-fewshot", Messages: fewShotMessages, Schema: Schema},
	"v5-reason":  {Name: "v5-reason", Messages: rulesMessages(true), Schema: reasonSchema},
}

// VariantNames lists Variants sorted by name.
func VariantNames() []string {
	names := make([]string, 0, len(Variants))
	for n := range Variants {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// rulesSystemPrompt replaces v1's one-line kind definitions with an ordered
// decision procedure (v1 runs got kind right only 40-86% of the time, the
// misses mostly test/config files labelled "behavior"), and tightens the
// naming rule for "what".
const rulesSystemPrompt = `You summarize one diff hunk for a code reviewer. Answer only with JSON matching the given schema — no prose, no markdown fences.

kind — decide by going down this list and taking the FIRST that applies:
1. "test": the file is test code (e.g. *_test.go, *.test.ts, *.spec.js, test_*.py, tests/ or e2e/ directories, *.feature) — even if the hunk only touches imports or helpers.
2. "config": the file is not application logic: build files (Makefile, go.mod, package.json, Dockerfile), CI/deploy YAML, shell install/build scripts, docs (*.md), settings files (.yaml, .toml, .json, .ini, .gitignore).
3. "rename": the only change is an identifier renamed, everything else identical.
4. "refactor": the program behaves the same afterwards — moved or extracted code, reformatting, comments only, reordered or regrouped imports, clearer names plus restructuring.
5. "behavior": anything else — the program does something different afterwards.

what — one or two plain sentences on the concrete change. Mention the one to three most important identifiers, copied character for character from the hunk or the enclosing function. Never mention an identifier that does not appear there. Do not state line counts.
inferred_why — the most likely motivation, one sentence, or "unclear".
impact — one sentence on what could break or who is affected, or "".
risk_flags — short tags ("error-handling", "concurrency", "breaking-api", "security", "data-loss"), or [] if nothing stands out.
confidence — 0 to 1.`

// rulesMessages builds v2 (rules only) or v3 (rules plus deterministic
// hints computed from the path and the hunk).
func rulesMessages(hints bool) func(Input) []llm.Message {
	return func(in Input) []llm.Message {
		return []llm.Message{
			{Role: "system", Content: rulesSystemPrompt},
			{Role: "user", Content: userContent(in, hints)},
		}
	}
}

func userContent(in Input, hints bool) string {
	language := in.Language
	if language == "" {
		language = "unknown"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "File: %s (%s)\n", in.Path, language)
	if hints {
		added, removed := countLines(in.Hunk)
		fmt.Fprintf(&b, "File category: %s\n", fileCategory(in.Path))
		fmt.Fprintf(&b, "Lines: +%d/-%d\n", added, removed)
	}
	b.WriteString("\n")
	if in.EnclosingFunction != "" {
		fmt.Fprintf(&b, "Enclosing function:\n```\n%s\n```\n\n", in.EnclosingFunction)
	}
	fmt.Fprintf(&b, "Hunk (unified diff body; '+' added, '-' removed, ' ' context):\n```\n%s\n```\n", in.Hunk)
	return b.String()
}

func countLines(hunk string) (added, removed int) {
	for _, l := range strings.Split(hunk, "\n") {
		switch {
		case strings.HasPrefix(l, "+"):
			added++
		case strings.HasPrefix(l, "-"):
			removed++
		}
	}
	return added, removed
}

// fileCategory is a path-only guess at "test file", "config/build/docs
// file" or "source file", given to the model as a hint, not a verdict.
func fileCategory(p string) string {
	base := strings.ToLower(path.Base(p))
	lp := "/" + strings.ToLower(p)
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.Contains(base, ".test."), strings.Contains(base, ".spec."),
		strings.HasPrefix(base, "test_"), strings.HasSuffix(base, ".feature"),
		strings.Contains(lp, "/tests/"), strings.Contains(lp, "/e2e/"), strings.Contains(lp, "/testdata/"):
		return "test file"
	}
	switch path.Ext(base) {
	case ".yaml", ".yml", ".toml", ".json", ".ini", ".md", ".sh", ".mod", ".sum", ".lock", ".cfg", ".conf":
		return "config/build/docs file"
	}
	switch base {
	case "makefile", "dockerfile", ".gitignore", "go.mod", "go.sum":
		return "config/build/docs file"
	}
	return "source file"
}

// fewShot is two invented examples (not eval cases) shown as prior turns.
var fewShot = []llm.Message{
	{Role: "user", Content: userContent(Input{
		Path: "billing/invoice_test.go", Language: "go",
		Hunk: " import (\n \t\"testing\"\n+\t\"time\"\n )\n",
	}, true)},
	{Role: "assistant", Content: `{"kind":"test","what":"Adds the time import to invoice_test.go.","inferred_why":"A test is about to use time values.","impact":"","risk_flags":[],"confidence":0.9}`},
	{Role: "user", Content: userContent(Input{
		Path: "billing/invoice.go", Language: "go",
		EnclosingFunction: "func (s *Service) Total(inv Invoice) (Money, error) {\n\tsum := Money{}\n\tfor _, l := range inv.Lines {\n\t\tsum = sum.Add(l.Amount)\n\t}\n\treturn sum, nil\n}",
		Hunk:              " \tsum := Money{}\n \tfor _, l := range inv.Lines {\n+\t\tif l.Void {\n+\t\t\tcontinue\n+\t\t}\n \t\tsum = sum.Add(l.Amount)\n",
	}, true)},
	{Role: "assistant", Content: `{"kind":"behavior","what":"Total now skips invoice lines marked Void when summing Amount.","inferred_why":"Voided lines were being charged.","impact":"Invoice totals drop for invoices with voided lines.","risk_flags":[],"confidence":0.85}`},
}

func fewShotMessages(in Input) []llm.Message {
	msgs := []llm.Message{{Role: "system", Content: rulesSystemPrompt}}
	msgs = append(msgs, fewShot...)
	return append(msgs, llm.Message{Role: "user", Content: userContent(in, true)})
}

// reasonSchema is Schema with a short "reasoning" field first: with thinking
// disabled, this gives the model one sentence of scratch space before it
// commits to kind. The pipeline would discard it.
var reasonSchema json.RawMessage = []byte(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "reasoning": {"type": "string", "maxLength": 300},
    "kind": {"type": "string", "enum": ["behavior", "refactor", "rename", "test", "config"]},
    "what": {"type": "string"},
    "inferred_why": {"type": "string"},
    "impact": {"type": "string"},
    "risk_flags": {"type": "array", "items": {"type": "string"}},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1}
  },
  "required": ["reasoning", "kind", "what", "confidence"]
}`)

// The behavior-first variants answer "what does this code do now, and how
// is that different from before" in plain language, instead of leading
// with a change-type label. kind stays, last and minor, for badges and the
// "collapse refactor-only" toggle.
const behaviorRules = `Mention identifiers only when copied character for character from the hunk or the enclosing function — never invent one. Talk about inputs, outputs, conditions, side effects and errors as a caller or user of this code would notice them, not about which lines were edited. Do not state line counts.`

const behaviorProseSystemPrompt = `You explain one diff hunk to a code reviewer in plain language. Answer only with JSON matching the given schema — no prose outside it, no markdown fences.

behavior — 1 to 3 sentences on behavior, not text edits:
- changed code: what it did before and what it does now ("Previously …; now …").
- brand-new code: what it does.
- no behavior effect (refactor, rename, formatting, comments, imports, docs): say "No behavior change:" and what was restructured.
` + behaviorRules + `
risk_flags — short tags ("error-handling", "concurrency", "breaking-api", "security", "data-loss"), or [].
kind — one tag: "behavior", "refactor", "rename", "test" (test files) or "config" (build/CI/docs/settings files).
confidence — 0 to 1.`

const behaviorSplitSystemPrompt = `You explain one diff hunk to a code reviewer in plain language. Answer only with JSON matching the given schema — no prose outside it, no markdown fences.

before — one sentence: what the old code did here, or "" if this is brand-new code.
after — one sentence: what the code does now.
change — one sentence: the difference a caller or user would notice. If there is none (refactor, rename, formatting, comments, imports, docs), write "No behavior change:" and what was restructured.
` + behaviorRules + `
risk_flags — short tags ("error-handling", "concurrency", "breaking-api", "security", "data-loss"), or [].
kind — one tag: "behavior", "refactor", "rename", "test" (test files) or "config" (build/CI/docs/settings files).
confidence — 0 to 1.`

func behaviorMessages(system string) func(Input) []llm.Message {
	return func(in Input) []llm.Message {
		return []llm.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: userContent(in, true)},
		}
	}
}

var behaviorProseSchema json.RawMessage = []byte(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "behavior": {"type": "string"},
    "risk_flags": {"type": "array", "items": {"type": "string"}},
    "kind": {"type": "string", "enum": ["behavior", "refactor", "rename", "test", "config"]},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1}
  },
  "required": ["behavior", "kind", "confidence"]
}`)

var behaviorSplitSchema json.RawMessage = []byte(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "before": {"type": "string"},
    "after": {"type": "string"},
    "change": {"type": "string"},
    "risk_flags": {"type": "array", "items": {"type": "string"}},
    "kind": {"type": "string", "enum": ["behavior", "refactor", "rename", "test", "config"]},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1}
  },
  "required": ["before", "after", "change", "kind", "confidence"]
}`)

func init() {
	Variants["b1-prose"] = Variant{Name: "b1-prose", Messages: behaviorMessages(behaviorProseSystemPrompt), Schema: behaviorProseSchema}
	Variants["b2-before-after"] = Variant{Name: "b2-before-after", Messages: behaviorMessages(behaviorSplitSystemPrompt), Schema: behaviorSplitSchema}
}
