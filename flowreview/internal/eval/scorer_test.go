package eval

import (
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestScoreInvalidJSONScoresZero(t *testing.T) {
	c := Case{ID: "1", Expected: Expected{Kind: "behavior"}}
	res := Score(c, model.SnippetSummary{}, false)
	if res.Score != 0 {
		t.Fatalf("want score 0 for invalid JSON, got %v", res.Score)
	}
	if res.JSONValid {
		t.Fatal("want JSONValid false")
	}
}

func TestScoreKindMatch(t *testing.T) {
	c := Case{Expected: Expected{Kind: "refactor"}}

	match := Score(c, model.SnippetSummary{Kind: "refactor", What: "tidies things up"}, true)
	if !match.KindMatch {
		t.Error("want KindMatch true when kinds agree")
	}

	mismatch := Score(c, model.SnippetSummary{Kind: "behavior", What: "tidies things up"}, true)
	if mismatch.KindMatch {
		t.Error("want KindMatch false when kinds disagree")
	}
	if mismatch.Score >= match.Score {
		t.Errorf("mismatched kind should score lower: match=%v mismatch=%v", match.Score, mismatch.Score)
	}
}

func TestScoreSymbolGroundedNotHallucinated(t *testing.T) {
	c := Case{
		Hunk:              "+func FunctionAt(path string) {}\n",
		EnclosingFunction: "",
		Expected:          Expected{Kind: "behavior"},
	}
	res := Score(c, model.SnippetSummary{Kind: "behavior", What: "Adds FunctionAt to look up the enclosing function."}, true)
	if len(res.HallucinatedSymbols) != 0 {
		t.Errorf("want no hallucinations, got %v", res.HallucinatedSymbols)
	}
	if len(res.SymbolCandidates) == 0 {
		t.Error("want FunctionAt recognized as a symbol candidate")
	}
}

func TestScoreSymbolHallucinated(t *testing.T) {
	c := Case{
		Hunk:              "+func FunctionAt(path string) {}\n",
		EnclosingFunction: "",
		Expected:          Expected{Kind: "behavior"},
	}
	grounded := Score(c, model.SnippetSummary{Kind: "behavior", What: "Adds FunctionAt."}, true)
	hallucinated := Score(c, model.SnippetSummary{Kind: "behavior", What: "Adds FunctionAt and calls ParseModuleGraph."}, true)

	found := false
	for _, s := range hallucinated.HallucinatedSymbols {
		if s == "ParseModuleGraph" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want ParseModuleGraph flagged as hallucinated, got %v", hallucinated.HallucinatedSymbols)
	}
	if hallucinated.Score >= grounded.Score {
		t.Errorf("hallucinated summary should score lower: grounded=%v hallucinated=%v", grounded.Score, hallucinated.Score)
	}
}

func TestScoreFilePathIsGrounded(t *testing.T) {
	// The prompt tells the model the file path (internal/llm/summary.Messages
	// includes "File: <path>"), so citing it back is a grounded mention, not
	// a hallucination, even though it appears in neither the hunk nor the
	// enclosing function.
	c := Case{
		Path:     "flowreview/.taskhound.yaml",
		Hunk:     "+      status: done\n",
		Expected: Expected{Kind: "config"},
	}
	res := Score(c, model.SnippetSummary{Kind: "config", What: "Flips a task's status in .taskhound.yaml to done."}, true)
	if len(res.HallucinatedSymbols) != 0 {
		t.Errorf("want the cited file path not flagged as hallucinated, got %v", res.HallucinatedSymbols)
	}
}

func TestScoreIgnoresPlainWords(t *testing.T) {
	// Ordinary English words (no internal capital, no underscore, no dot, no
	// call parens) should never be treated as symbol candidates at all, so
	// they can't be scored as hallucinations.
	c := Case{Hunk: "+ return nil\n", Expected: Expected{Kind: "behavior"}}
	res := Score(c, model.SnippetSummary{Kind: "behavior", What: "This change simply returns early when the input is empty."}, true)
	if len(res.SymbolCandidates) != 0 {
		t.Errorf("want no symbol candidates among plain words, got %v", res.SymbolCandidates)
	}
}

func TestScoreExpectedSymbolsRecall(t *testing.T) {
	// Hunk names both symbols so grounding stays perfect in both cases below
	// and the only thing moving the score is expected-symbol recall.
	c := Case{
		Hunk:     "+func FunctionAt() {}\n+func Outline() {}\n",
		Expected: Expected{Kind: "behavior", Symbols: []string{"FunctionAt", "Outline"}},
	}

	both := Score(c, model.SnippetSummary{Kind: "behavior", What: "Changes FunctionAt and Outline."}, true)
	if len(both.ExpectedSymbolsMissed) != 0 {
		t.Errorf("want both symbols found, missed %v", both.ExpectedSymbolsMissed)
	}

	one := Score(c, model.SnippetSummary{Kind: "behavior", What: "Changes FunctionAt."}, true)
	if len(one.ExpectedSymbolsMissed) != 1 || one.ExpectedSymbolsMissed[0] != "Outline" {
		t.Errorf("want Outline reported missed, got %v", one.ExpectedSymbolsMissed)
	}
	if one.Score >= both.Score {
		t.Errorf("missing an expected symbol should score lower: both=%v one=%v", both.Score, one.Score)
	}
}

func TestScoreCountsCorrect(t *testing.T) {
	c := Case{Expected: Expected{Kind: "behavior", AddedLines: 3, RemovedLines: 1}}
	res := Score(c, model.SnippetSummary{Kind: "behavior", What: "Reworks the loop (+3/-1)."}, true)
	if !res.CountsMentioned {
		t.Fatal("want CountsMentioned true")
	}
	if !res.CountsCorrect {
		t.Error("want CountsCorrect true for a matching +3/-1")
	}
}

func TestScoreCountsIncorrect(t *testing.T) {
	c := Case{Expected: Expected{Kind: "behavior", AddedLines: 3, RemovedLines: 1}}
	correct := Score(c, model.SnippetSummary{Kind: "behavior", What: "Reworks the loop (+3/-1)."}, true)
	wrong := Score(c, model.SnippetSummary{Kind: "behavior", What: "Reworks the loop (+9/-9)."}, true)
	if !wrong.CountsMentioned {
		t.Fatal("want CountsMentioned true")
	}
	if wrong.CountsCorrect {
		t.Error("want CountsCorrect false for a wrong +9/-9")
	}
	if wrong.Score >= correct.Score {
		t.Errorf("a wrong stated count should score lower: correct=%v wrong=%v", correct.Score, wrong.Score)
	}
}

func TestScoreCountsNotMentionedIsNotPenalized(t *testing.T) {
	c := Case{Expected: Expected{Kind: "behavior", AddedLines: 3, RemovedLines: 1}}
	silent := Score(c, model.SnippetSummary{Kind: "behavior", What: "Reworks the loop."}, true)
	if silent.CountsMentioned {
		t.Fatal("want CountsMentioned false")
	}
	// Silence is worth the same as a correct mention (0.10 weight either way).
	correct := Score(c, model.SnippetSummary{Kind: "behavior", What: "Reworks the loop (+3/-1)."}, true)
	if silent.Score != correct.Score {
		t.Errorf("not mentioning counts should score the same as mentioning them correctly: silent=%v correct=%v", silent.Score, correct.Score)
	}
}

func TestLooksLikeSymbol(t *testing.T) {
	cases := []struct {
		tok  string
		want bool
	}{
		{"FunctionAt", true},
		{"handleRequest", true},
		{"my_func", true},
		{"pkg.Func", true},
		{"Foo(", true},
		{"Foo()", true},
		{"config", false},
		{"Hello", false},
		{"the", false},
	}
	for _, tc := range cases {
		if got := LooksLikeSymbol(tc.tok); got != tc.want {
			t.Errorf("LooksLikeSymbol(%q) = %v, want %v", tc.tok, got, tc.want)
		}
	}
}
