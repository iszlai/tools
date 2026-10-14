// Command flowreview-eval is the FR-27 harness runner: for each named
// catalog model it ensures the weights are installed, starts the embedded
// llama-server sidecar against the pinned build in third_party/llama/dist/
// (run `make fetch-llama` first), runs every case in --cases through the real
// FR-28 prompt (internal/llm/summary), scores the results (internal/eval),
// and writes a markdown report.
//
// This is a dev-only command: unlike the product's model picker (FR-26), it
// auto-confirms every download disclosure so it can run unattended — the
// disclosure is still printed to stdout. Never do this in the product itself.
//
// Usage (from flowreview/, so relative paths match the Makefile's):
//
//	go run ./cmd/flowreview-eval --models qwen2.5-coder-1.5b-instruct-q4_k_m,qwen2.5-coder-3b-instruct-q4_k_m
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iszlai/tools/flowreview/internal/eval"
	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/llm"
	"github.com/iszlai/tools/flowreview/internal/llm/models"
	"github.com/iszlai/tools/flowreview/internal/llm/sidecar"
	"github.com/iszlai/tools/flowreview/internal/llm/summary"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// maxInFlight bounds concurrent requests to the sidecar, matching the
// `--parallel 2` server slots PLAN.md §3 fixes for the product itself.
const maxInFlight = 2

// requestTimeout bounds one case's completion call so one stuck request can't
// hang the whole run.
const requestTimeout = 120 * time.Second

func main() {
	modelsFlag := flag.String("models", "", "comma-separated catalog model names to evaluate (required)")
	casesDir := flag.String("cases", "eval/cases", "directory of eval.Case JSON files")
	out := flag.String("out", "", "markdown report path (default eval/reports/<date>.md)")
	promptsFlag := flag.String("prompts", "v1", "comma-separated prompt variants (internal/llm/summary.Variants); \"all\" runs every one")
	dump := flag.String("dump", "", "write raw model outputs as JSON lines to this file")
	llamaDist := flag.String("llama-dist", "third_party/llama/dist", "directory with the fetched llama-server build (run `make fetch-llama`)")
	flag.Parse()

	if *modelsFlag == "" {
		fmt.Fprintln(os.Stderr, "flowreview-eval: --models is required, e.g. --models qwen2.5-coder-1.5b-instruct-q4_k_m,qwen2.5-coder-3b-instruct-q4_k_m")
		os.Exit(2)
	}
	if _, err := os.Stat(filepath.Join(*llamaDist, "llama-server")); err != nil {
		fmt.Fprintf(os.Stderr, "flowreview-eval: no llama-server at %s; run `make fetch-llama` first\n", *llamaDist)
		os.Exit(1)
	}

	var variants []summary.Variant
	names := strings.Split(*promptsFlag, ",")
	if *promptsFlag == "all" {
		names = summary.VariantNames()
	}
	for _, n := range names {
		v, ok := summary.Variants[strings.TrimSpace(n)]
		if !ok {
			fmt.Fprintf(os.Stderr, "flowreview-eval: unknown prompt variant %q (have %s)\n", n, strings.Join(summary.VariantNames(), ", "))
			os.Exit(2)
		}
		variants = append(variants, v)
	}

	if *dump != "" {
		f, err := os.Create(*dump)
		if err != nil {
			fmt.Fprintln(os.Stderr, "flowreview-eval:", err)
			os.Exit(1)
		}
		defer f.Close()
		dumpFile = f
	}

	cases, err := eval.LoadCases(*casesDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flowreview-eval:", err)
		os.Exit(1)
	}
	fmt.Printf("flowreview-eval: %d case(s) from %s\n", len(cases), *casesDir)

	report := eval.Report{
		GeneratedAt: time.Now(),
		Aggregates:  map[string]eval.Aggregate{},
		Results:     map[string][]eval.CaseResult{},
		Cases:       map[string]eval.Case{},
	}
	for _, c := range cases {
		report.Cases[c.ID] = c
	}

	ctx := context.Background()
	for _, name := range strings.Split(*modelsFlag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := models.Lookup(name); !ok {
			fmt.Fprintf(os.Stderr, "flowreview-eval: skipping unknown model %q (see internal/llm/models.Catalog)\n", name)
			continue
		}

		dumpModel = name
		runs, err := runModel(ctx, name, cases, variants, *llamaDist)
		if err != nil {
			fmt.Fprintf(os.Stderr, "flowreview-eval: %s: %v\n", name, err)
			continue
		}
		for _, r := range runs {
			report.Models = append(report.Models, r.agg.ModelName)
			report.Aggregates[r.agg.ModelName] = r.agg
			report.Results[r.agg.ModelName] = r.results
		}
	}

	if len(report.Models) == 0 {
		fmt.Fprintln(os.Stderr, "flowreview-eval: no model produced a result; nothing to report")
		os.Exit(1)
	}

	outPath := *out
	if outPath == "" {
		outPath = filepath.Join("eval", "reports", time.Now().Format("2006-01-02")+".md")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "flowreview-eval:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, []byte(report.RenderMarkdown()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "flowreview-eval:", err)
		os.Exit(1)
	}
	fmt.Println("flowreview-eval: wrote", outPath)
}

// variantRun is one (model, prompt variant) pair's results. agg.ModelName
// is the model name, suffixed " @ <variant>" when more than one variant ran.
type variantRun struct {
	agg     eval.Aggregate
	results []eval.CaseResult
}

// runModel installs name (if needed), runs every case against it once per
// prompt variant on one sidecar, and returns each run's aggregate and
// per-case results. Peak RSS is sampled over the whole sidecar lifetime, so
// every variant reports the same figure. It always stops the sidecar before
// returning, even on error.
func runModel(ctx context.Context, name string, cases []eval.Case, variants []summary.Variant, llamaDist string) ([]variantRun, error) {
	dataRoot := models.DefaultDataRoot()
	mgr := &models.Manager{Root: filepath.Join(dataRoot, "models")}
	mgr.Confirm = func(d models.Disclosure) bool {
		fmt.Printf("== %s: disclosure ==\n", d.Name)
		fmt.Printf("   size:    %.1f GB\n", float64(d.Size)/(1<<30))
		fmt.Printf("   licence: %s (%s)\n", d.Licence, d.LicenceURL)
		fmt.Printf("   needs:   %d GB RAM\n", d.MinRAMGB)
		fmt.Printf("   source:  %s\n", d.URL)
		fmt.Println("   auto-confirmed: flowreview-eval is a dev-only command (see its doc comment)")
		return true
	}

	fmt.Printf("== %s: ensuring installed ==\n", name)
	if err := mgr.Install(ctx, name, progressPrinter(name)); err != nil {
		return nil, fmt.Errorf("install: %w", err)
	}
	modelPath := filepath.Join(mgr.Root, name+".gguf")

	fmt.Printf("== %s: starting sidecar ==\n", name)
	sc, err := sidecar.Start(ctx, sidecar.Options{
		DataRoot:  dataRoot,
		ModelPath: modelPath,
		BinDir:    llamaDist,
	})
	if err != nil {
		return nil, fmt.Errorf("start sidecar: %w", err)
	}

	stopRSS := make(chan struct{})
	peakRSS := make(chan int64, 1)
	go sampleRSS(dataRoot, stopRSS, peakRSS)

	provider := sc.Provider(name)
	allResults := make([][]eval.CaseResult, len(variants))
	for vi, v := range variants {
		label := name
		if len(variants) > 1 {
			label = name + " @ " + v.Name
		}
		results := make([]eval.CaseResult, len(cases))
		sem := make(chan struct{}, maxInFlight)
		var wg sync.WaitGroup
		for i, c := range cases {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, c eval.Case) {
				defer wg.Done()
				defer func() { <-sem }()
				results[i] = runCase(ctx, provider, v, c)
				fmt.Printf("== %s: %s score=%.2f kind=%s/%s latency=%s\n",
					label, c.ID, results[i].Score, results[i].KindExpected, results[i].KindGot, results[i].Latency.Round(time.Millisecond))
			}(i, c)
		}
		wg.Wait()
		allResults[vi] = results
	}

	close(stopRSS)
	rss := <-peakRSS
	if err := sc.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "flowreview-eval: %s: stop: %v\n", name, err)
	}

	runs := make([]variantRun, len(variants))
	for vi, v := range variants {
		label := name
		if len(variants) > 1 {
			label = name + " @ " + v.Name
		}
		runs[vi] = variantRun{agg: eval.Summarize(label, allResults[vi], rss), results: allResults[vi]}
	}
	return runs, nil
}

// runCase sends one case through the real FR-28 prompt and schema and scores
// the response. A request error (including a timeout) scores as invalid
// JSON, same as a malformed response — both mean no usable summary came
// back.
func runCase(ctx context.Context, provider *sidecar.Provider, v summary.Variant, c eval.Case) eval.CaseResult {
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	msgs := v.Messages(summary.Input{
		Path:              c.Path,
		Language:          lang.Detect(c.Path),
		Hunk:              c.Hunk,
		EnclosingFunction: c.EnclosingFunction,
	})
	resp, err := provider.CompleteJSON(reqCtx, llm.Request{Messages: msgs, MaxTokens: 400, Temperature: 0.2, Slot: -1}, v.Schema)
	if err != nil {
		return eval.Score(c, model.SnippetSummary{}, false)
	}

	raw := cleanJSON(resp.Content)
	var sum model.SnippetSummary
	validJSON := json.Unmarshal([]byte(raw), &sum) == nil
	if validJSON {
		// Behavior-first variants (summary.Variants "b*") have no "what";
		// score their prose in its place so grounding and recall still apply.
		var b struct{ Behavior, Before, After, Change string }
		_ = json.Unmarshal([]byte(raw), &b)
		if prose := strings.TrimSpace(strings.Join([]string{b.Behavior, b.Before, b.After, b.Change}, " ")); prose != "" {
			sum.What = prose
		}
	}
	dumpRaw(v.Name, c.ID, raw)

	res := eval.Score(c, sum, validJSON)
	res.Latency = resp.Duration
	res.PromptTokens = resp.PromptTokens
	res.CompletionTokens = resp.CompletionTokens
	if resp.Duration > 0 {
		res.TokensPerSecond = float64(resp.CompletionTokens) / resp.Duration.Seconds()
	}
	return res
}

// dump, when --dump is set, receives one JSON line per case with the raw
// model output, for judging summary quality by reading rather than by
// eval.Score's heuristics.
var (
	dumpMu    sync.Mutex
	dumpFile  *os.File
	dumpModel string
)

func dumpRaw(variant, caseID, raw string) {
	if dumpFile == nil {
		return
	}
	line, _ := json.Marshal(map[string]string{"model": dumpModel, "variant": variant, "case_id": caseID, "output": raw})
	dumpMu.Lock()
	defer dumpMu.Unlock()
	dumpFile.Write(append(line, '\n'))
}

// cleanJSON strips a markdown code fence around resp.Content, in case a model
// wraps its JSON in one despite the prompt's instruction not to — CompleteJSON
// constrains the grammar, not whether the server's chat template still adds
// fences around it.
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func progressPrinter(name string) func(done, total int64) {
	last := -1
	return func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct == last {
			return
		}
		last = pct
		fmt.Printf("\r%s: downloading %3d%%", name, pct)
		if pct == 100 {
			fmt.Println()
		}
	}
}

// sampleRSS polls llama-server's resident set size via its documented pidfile
// (internal/llm/sidecar writes DataRoot/llama-server.pid while running; there
// is no exported way to get the PID directly, and reading the pidfile is less
// invasive than adding one) until stop closes, then sends the peak in KB.
func sampleRSS(dataRoot string, stop <-chan struct{}, result chan<- int64) {
	pidPath := filepath.Join(dataRoot, "llama-server.pid")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var peak int64
	for {
		select {
		case <-stop:
			result <- peak
			return
		case <-ticker.C:
			if rss := readRSSKB(pidPath); rss > peak {
				peak = rss
			}
		}
	}
}

func readRSSKB(pidPath string) int64 {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strings.TrimSpace(string(data))).Output()
	if err != nil {
		return 0
	}
	kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return kb
}
