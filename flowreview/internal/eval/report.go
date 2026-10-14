package eval

import (
	"fmt"
	"strings"
	"time"
)

// Aggregate summarizes one model's CaseResults for the report table.
type Aggregate struct {
	ModelName         string
	NumCases          int
	MeanScore         float64
	KindAccuracy      float64
	HallucinationRate float64 // sum(hallucinated)/sum(candidates) across cases that named any symbol
	JSONValidityRate  float64
	MeanLatency       time.Duration
	TokensPerSecond   float64 // sum(completion tokens) / sum(latency), across all cases
	PeakRSSKB         int64   // 0 when it could not be sampled (see cmd/flowreview-eval)
}

// Summarize computes an Aggregate from one model's per-case results.
func Summarize(modelName string, results []CaseResult, peakRSSKB int64) Aggregate {
	a := Aggregate{ModelName: modelName, NumCases: len(results), PeakRSSKB: peakRSSKB}
	if len(results) == 0 {
		return a
	}

	var totalScore float64
	var validCount, kindMatches int
	var totalCandidates, totalHallucinated int
	var totalCompletionTokens int
	var totalSeconds float64
	for _, r := range results {
		totalScore += r.Score
		if r.JSONValid {
			validCount++
		}
		if r.KindMatch {
			kindMatches++
		}
		totalCandidates += len(r.SymbolCandidates)
		totalHallucinated += len(r.HallucinatedSymbols)
		totalCompletionTokens += r.CompletionTokens
		totalSeconds += r.Latency.Seconds()
	}

	n := float64(len(results))
	a.MeanScore = totalScore / n
	a.KindAccuracy = float64(kindMatches) / n
	a.JSONValidityRate = float64(validCount) / n
	if totalCandidates > 0 {
		a.HallucinationRate = float64(totalHallucinated) / float64(totalCandidates)
	}
	a.MeanLatency = time.Duration(totalSeconds / n * float64(time.Second))
	if totalSeconds > 0 {
		a.TokensPerSecond = float64(totalCompletionTokens) / totalSeconds
	}
	return a
}

// Report is everything cmd/flowreview-eval needs to render the markdown
// report for one run: one Aggregate and its CaseResults per model, in the
// order the models were run.
type Report struct {
	GeneratedAt time.Time
	Models      []string
	Aggregates  map[string]Aggregate
	Results     map[string][]CaseResult
	Cases       map[string]Case // by id, for the per-case detail section
}

// RenderMarkdown writes the FR-27 report: a summary table ranked by mean
// score, then one collapsible per-case detail section per model.
func (r Report) RenderMarkdown() string {
	var b strings.Builder

	fmt.Fprintf(&b, "# FlowReview eval report — %s\n\n", r.GeneratedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "%d case(s). Scoring formula: see internal/eval.Score.\n\n", len(r.Cases))

	fmt.Fprintln(&b, "| Model | Score | Kind acc. | Hallucination rate | JSON valid | Mean latency | Tokens/s | Peak RSS |")
	fmt.Fprintln(&b, "|---|---|---|---|---|---|---|---|")
	for _, m := range r.Models {
		a := r.Aggregates[m]
		fmt.Fprintf(&b, "| %s | %.3f | %.0f%% | %.1f%% | %.0f%% | %s | %.1f | %s |\n",
			m, a.MeanScore, a.KindAccuracy*100, a.HallucinationRate*100, a.JSONValidityRate*100,
			a.MeanLatency.Round(time.Millisecond), a.TokensPerSecond, formatRSS(a.PeakRSSKB))
	}
	fmt.Fprintln(&b)

	for _, m := range r.Models {
		fmt.Fprintf(&b, "<details>\n<summary>%s — per-case detail</summary>\n\n", m)
		fmt.Fprintln(&b, "| Case | Kind (want/got) | Score | JSON valid | Hallucinated symbols | Missed symbols | Latency |")
		fmt.Fprintln(&b, "|---|---|---|---|---|---|---|")
		for _, cr := range r.Results[m] {
			fmt.Fprintf(&b, "| %s | %s / %s | %.2f | %v | %s | %s | %s |\n",
				cr.CaseID, cr.KindExpected, cr.KindGot, cr.Score, cr.JSONValid,
				joinOrDash(cr.HallucinatedSymbols), joinOrDash(cr.ExpectedSymbolsMissed),
				cr.Latency.Round(time.Millisecond))
		}
		fmt.Fprint(&b, "\n</details>\n\n")
	}

	return b.String()
}

func joinOrDash(xs []string) string {
	if len(xs) == 0 {
		return "-"
	}
	return strings.Join(xs, ", ")
}

func formatRSS(kb int64) string {
	if kb <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f MB", float64(kb)/1024)
}
