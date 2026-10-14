package eval

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// CaseResult is the scorer's verdict for one Case against one model's
// structured summary.
type CaseResult struct {
	CaseID string `json:"case_id"`

	JSONValid    bool   `json:"json_valid"`
	KindExpected string `json:"kind_expected"`
	KindGot      string `json:"kind_got"`
	KindMatch    bool   `json:"kind_match"`

	// SymbolCandidates are the identifier-looking tokens LooksLikeSymbol
	// found in the summary's What field (deduplicated). HallucinatedSymbols
	// is the subset of those absent from the hunk and enclosing function.
	SymbolCandidates    []string `json:"symbol_candidates,omitempty"`
	HallucinatedSymbols []string `json:"hallucinated_symbols,omitempty"`

	ExpectedSymbolsFound  []string `json:"expected_symbols_found,omitempty"`
	ExpectedSymbolsMissed []string `json:"expected_symbols_missed,omitempty"`

	CountsMentioned bool `json:"counts_mentioned"`
	CountsCorrect   bool `json:"counts_correct"`

	Latency          time.Duration `json:"latency"`
	PromptTokens     int           `json:"prompt_tokens"`
	CompletionTokens int           `json:"completion_tokens"`
	TokensPerSecond  float64       `json:"tokens_per_second"`

	Score float64 `json:"score"`
}

// symbolTokenRe finds identifier-looking words in free text: a bare
// identifier, a dotted path (pkg.Func), and an optional trailing "(" or "()"
// for a call-style mention. The parenthesis suffix is entirely optional so a
// plain "FunctionAt" still matches.
var symbolTokenRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*(?:\(\)?)?`)

// countsRe matches a "+a/-b" (or "+a/−b", with a Unicode minus) line-count
// mention — the shape of flowreview's own deterministic fallback text
// ("Modified <func>: +a/-b lines") and what the summary prompt asks the model
// to use when it states counts at all (internal/llm/summary.systemPrompt).
var countsRe = regexp.MustCompile(`\+(\d+)\s*/\s*[-−](\d+)`)

// Score grades a model's summary of c.
//
// raw was unmarshaled into summary with validJSON reporting whether that
// succeeded against the schema (internal/llm/summary.Schema); the caller
// measured latency and token counts from the provider's response.
//
// Formula (documented, not tuned): invalid JSON scores 0 outright — the other
// axes cannot be judged on an empty or malformed summary. Otherwise:
//
//	0.30 * kind match            (summary.Kind == c.Expected.Kind)
//	0.35 * symbol grounding      (1 - hallucination rate among identifier-
//	                              looking tokens in What; a summary that names
//	                              no symbols at all grounds perfectly by this
//	                              measure — the penalty is for naming
//	                              something that is not there, not for saying
//	                              less)
//	0.25 * expected-symbol recall (fraction of c.Expected.Symbols mentioned
//	                              anywhere in What/InferredWhy/Impact;
//	                              vacuously 1.0 when the case names none)
//	0.10 * counts                (1.0 if the summary states no "+a/-b" count,
//	                              1.0 if it states one and it is right, 0.0 if
//	                              it states one and it is wrong — silence is
//	                              not penalized, a wrong claim is)
func Score(c Case, summary model.SnippetSummary, validJSON bool) CaseResult {
	res := CaseResult{CaseID: c.ID, JSONValid: validJSON, KindExpected: c.Expected.Kind, KindGot: summary.Kind}
	if !validJSON {
		return res
	}

	res.KindMatch = summary.Kind == c.Expected.Kind

	haystack := c.Hunk + "\n" + c.EnclosingFunction
	seen := map[string]bool{}
	for _, tok := range symbolTokenRe.FindAllString(summary.What, -1) {
		if !LooksLikeSymbol(tok) {
			continue
		}
		bare := bareToken(tok)
		if bare == "" || seen[bare] {
			continue
		}
		seen[bare] = true
		res.SymbolCandidates = append(res.SymbolCandidates, bare)
		if !strings.Contains(haystack, bare) {
			res.HallucinatedSymbols = append(res.HallucinatedSymbols, bare)
		}
	}
	grounding := 1.0
	if n := len(res.SymbolCandidates); n > 0 {
		grounding = 1 - float64(len(res.HallucinatedSymbols))/float64(n)
	}

	mentions := summary.What + "\n" + summary.InferredWhy + "\n" + summary.Impact
	recall := 1.0
	if n := len(c.Expected.Symbols); n > 0 {
		var found int
		for _, sym := range c.Expected.Symbols {
			if strings.Contains(mentions, sym) {
				found++
				res.ExpectedSymbolsFound = append(res.ExpectedSymbolsFound, sym)
			} else {
				res.ExpectedSymbolsMissed = append(res.ExpectedSymbolsMissed, sym)
			}
		}
		recall = float64(found) / float64(n)
	}

	countsComponent := 1.0
	if m := countsRe.FindStringSubmatch(mentions); m != nil {
		res.CountsMentioned = true
		want := fmt.Sprintf("%d/%d", c.Expected.AddedLines, c.Expected.RemovedLines)
		got := m[1] + "/" + m[2]
		res.CountsCorrect = got == want
		if !res.CountsCorrect {
			countsComponent = 0
		}
	}

	res.Score = 0.30*boolScore(res.KindMatch) + 0.35*grounding + 0.25*recall + 0.10*countsComponent
	return res
}

func boolScore(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// LooksLikeSymbol reports whether tok (a symbolTokenRe match) plausibly names
// a real code symbol rather than an ordinary English word: camelCase or
// PascalCase (an uppercase letter after the first rune), snake_case, a dotted
// path, or a call-style "Foo(" / "Foo()". This is a heuristic, not a parser —
// it can both miss real symbols (a single capitalized word like "Section")
// and flag non-symbols (a dotted abbreviation like "e.g."); it is applied
// identically to every model, so it is a fair relative measure even where it
// is not a perfect absolute one.
func LooksLikeSymbol(tok string) bool {
	bare := bareToken(tok)
	if bare == "" {
		return false
	}
	if strings.Contains(bare, "_") || strings.Contains(bare, ".") {
		return true
	}
	if strings.HasSuffix(tok, "(") || strings.HasSuffix(tok, "()") {
		return true
	}
	for i, r := range bare {
		if i > 0 && unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// bareToken strips one optional trailing ")" then one optional trailing "("
// off a symbolTokenRe match, so "Foo()", "Foo(" and "Foo" all compare equal.
func bareToken(tok string) string {
	return strings.TrimSuffix(strings.TrimSuffix(tok, ")"), "(")
}
