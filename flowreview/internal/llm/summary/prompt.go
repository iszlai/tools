// Package summary is the prompt and output schema for the embedded model's
// structured hunk summaries (PLAN.md §2 "Structured summaries", §5 FR-28).
// It is prompt construction only — FR-28 builds the cache (model.SummaryKey),
// the priority queue and the worker around it; FR-27's eval harness calls
// Messages and Schema directly so it measures the exact prompt a real run
// would send, not a stand-in.
package summary

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/llm"
)

// PromptVersion identifies the prompt and schema the pipeline sends
// (Variants[Current]). It is one input to model.SummaryKey: bump it whenever
// a change would make a previously cached summary's wording or shape stale.
const PromptVersion = "v2-behavior"

// Input is what one hunk's prompt needs. EnclosingFunction is best effort —
// empty when no LanguageProvider found one (no grammar for the file, or no
// enclosing function at that line); the prompt still works without it.
type Input struct {
	Path              string
	Language          string // internal/lang.Detect(Path); "" renders as "unknown"
	Hunk              string // unified-diff body: one line per diff line, "+"/"-"/" " prefix, no "@@" header (model.HunkBody)
	EnclosingFunction string
}

// systemPrompt fixes the model's vocabulary to model.SnippetSummary's fields
// so CompleteJSON's schema constrains syntax and this constrains meaning. The
// "+A/-B" instruction for counts is deliberate: internal/eval's scorer looks
// for exactly that shape when it checks a stated count against the hunk's
// real one.
const systemPrompt = `You are reviewing one small diff hunk from a code review tool. Answer only with JSON matching the given schema — no prose outside it, no markdown fences.

Fields:
- kind: the single best label for what this hunk does — "behavior" (changes what the code does), "refactor" (same behavior, different structure), "rename" (renames an identifier with no other change), "test" (test code only), or "config" (build/deploy/config files, no application logic).
- what: one or two plain sentences describing the concrete change. Name the function(s) or symbol(s) actually touched, exactly as they appear in the hunk or the enclosing function below — never invent a name that is not there. If you state how many lines were added or removed, use exactly the form "+A/-B" (e.g. "+3/-1").
- inferred_why: your best guess at the motivation, one sentence. Say "unclear" if you cannot tell.
- impact: one sentence on what could break or who is affected, or "" if none.
- risk_flags: short tags for anything risky (e.g. "error-handling", "concurrency", "breaking-api"); an empty array if nothing stands out.
- confidence: your confidence in this summary, 0 to 1.`

// Messages builds the chat messages for in. The caller passes them, together
// with Schema, to llm.ModelProvider.CompleteJSON.
func Messages(in Input) []llm.Message {
	language := in.Language
	if language == "" {
		language = "unknown"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "File: %s (%s)\n\n", in.Path, language)
	if in.EnclosingFunction != "" {
		fmt.Fprintf(&b, "Enclosing function:\n```\n%s\n```\n\n", in.EnclosingFunction)
	}
	fmt.Fprintf(&b, "Hunk (unified diff body; '+' added, '-' removed, ' ' context):\n```\n%s\n```\n", in.Hunk)

	return []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: b.String()},
	}
}

// Schema is the JSON Schema (draft-07) constraining CompleteJSON's output to
// the model-filled fields of model.SnippetSummary: kind, what, inferred_why,
// impact, risk_flags and confidence. FR-28's pipeline fills the rest (Key,
// HunkID, ModelID, PromptVersion, Fallback, CreatedAt) itself — the model is
// never asked for them.
var Schema json.RawMessage = []byte(`{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "kind": {"type": "string", "enum": ["behavior", "refactor", "rename", "test", "config"]},
    "what": {"type": "string"},
    "inferred_why": {"type": "string"},
    "impact": {"type": "string"},
    "risk_flags": {"type": "array", "items": {"type": "string"}},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1}
  },
  "required": ["kind", "what", "confidence"]
}`)
