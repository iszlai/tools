package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// fakeProvider emits a scripted list of findings, waiting delay before each
// one and honoring ctx cancellation in between — enough to test timeouts and
// MaxFindings without a real model.
type fakeProvider struct {
	name     string
	findings []model.AgentFinding
	delay    time.Duration
}

func (p *fakeProvider) Capabilities() Capabilities {
	return Capabilities{Name: p.name, Local: true}
}

func (p *fakeProvider) Review(ctx context.Context, _ *model.ReviewSession, _ Scope, emit func(model.AgentFinding)) error {
	for _, f := range p.findings {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.delay):
		}
		emit(f)
	}
	return nil
}

func testSession() *model.ReviewSession {
	return &model.ReviewSession{ID: "sess-1", RepoKey: "repo"}
}

func readLogLines(t *testing.T, dir string) []map[string]any {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read runs dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files in runs dir, want 1", len(entries))
	}

	f, err := os.Open(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer f.Close()

	var lines []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("unmarshal log line %q: %v", scanner.Text(), err)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestRunnerVerifiesAndForwards(t *testing.T) {
	read := fileReader(map[string]string{
		"pkg/foo.go": "package pkg\nfunc Foo() {}\n",
	})

	verifiable := model.AgentFinding{
		Title:      "real finding",
		Severity:   "low",
		Confidence: 0.5,
		Evidence:   []model.Evidence{{Path: "pkg/foo.go", Line: 2, Excerpt: "func Foo() {}"}},
	}
	hallucinated := model.AgentFinding{
		Title:      "hallucinated finding",
		Severity:   "high",
		Confidence: 0.9,
		Evidence:   []model.Evidence{{Path: "pkg/foo.go", Line: 2, Excerpt: "this line was never written"}},
	}

	provider := &fakeProvider{name: "fake", findings: []model.AgentFinding{verifiable, hallucinated}}
	runsDir := t.TempDir()
	runner := NewRunner(provider, runsDir)
	runner.read = read

	var got []model.AgentFinding
	err := runner.Run(context.Background(), testSession(), Scope{}, func(f model.AgentFinding) {
		got = append(got, f)
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("forwarded %d findings, want 1 (hallucinated one must be dropped)", len(got))
	}
	if got[0].Title != "real finding" {
		t.Errorf("forwarded finding = %q, want %q", got[0].Title, "real finding")
	}
	if got[0].ID == "" {
		t.Error("forwarded finding has no ID")
	}
	if got[0].Provider != "fake" {
		t.Errorf("forwarded finding Provider = %q, want %q", got[0].Provider, "fake")
	}

	lines := readLogLines(t, runsDir)
	if len(lines) != 4 { // start + 2 findings + end
		t.Fatalf("got %d log lines, want 4: %v", len(lines), lines)
	}
	if lines[0]["type"] != "start" {
		t.Errorf("first log line type = %v, want start", lines[0]["type"])
	}
	if lines[1]["type"] != "finding" || lines[1]["accepted"] != true {
		t.Errorf("second log line = %v, want accepted finding", lines[1])
	}
	if lines[2]["type"] != "finding" || lines[2]["accepted"] != false {
		t.Errorf("third log line = %v, want rejected finding", lines[2])
	}
	if lines[2]["reason"] == "" || lines[2]["reason"] == nil {
		t.Errorf("rejected finding log line has no reason: %v", lines[2])
	}
	end := lines[3]
	if end["type"] != "end" {
		t.Errorf("last log line type = %v, want end", end["type"])
	}
	if end["emitted"] != float64(1) || end["rejected"] != float64(1) {
		t.Errorf("end counts = %v, want emitted=1 rejected=1", end)
	}
}

func TestRunnerTimeoutHonoured(t *testing.T) {
	provider := &fakeProvider{
		name:  "slow",
		delay: 50 * time.Millisecond,
		findings: []model.AgentFinding{
			{Title: "one", Severity: "low", Confidence: 0.1},
			{Title: "two", Severity: "low", Confidence: 0.1},
			{Title: "three", Severity: "low", Confidence: 0.1},
		},
	}
	runsDir := t.TempDir()
	runner := NewRunner(provider, runsDir)

	start := time.Now()
	err := runner.Run(context.Background(), testSession(), Scope{Limits: Limits{Timeout: 10 * time.Millisecond}}, func(model.AgentFinding) {})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("Run() took %v, want well under the 3x50ms it would take without the timeout", elapsed)
	}

	lines := readLogLines(t, runsDir)
	end := lines[len(lines)-1]
	if end["type"] != "end" {
		t.Fatalf("last log line type = %v, want end", end["type"])
	}
	if end["error"] == "" || end["error"] == nil {
		t.Errorf("end log line has no error: %v", end)
	}
}

func TestRunnerMaxFindingsCapsForwarding(t *testing.T) {
	findings := []model.AgentFinding{
		{Title: "one", Severity: "low", Confidence: 0.1},
		{Title: "two", Severity: "low", Confidence: 0.1},
		{Title: "three", Severity: "low", Confidence: 0.1},
	}
	// Give each finding one trivially verifiable piece of evidence, so
	// MaxFindings — not Verify — is what's under test here.
	for i := range findings {
		findings[i].Evidence = []model.Evidence{{Path: "f.go", Line: 1, Excerpt: "x"}}
	}
	provider := &fakeProvider{name: "fake", findings: findings}
	runsDir := t.TempDir()
	runner := NewRunner(provider, runsDir)
	runner.read = fileReader(map[string]string{"f.go": "x\n"})

	var got []model.AgentFinding
	err := runner.Run(context.Background(), testSession(), Scope{Limits: Limits{MaxFindings: 1}}, func(f model.AgentFinding) {
		got = append(got, f)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want nil or context.Canceled", err)
	}
	if len(got) != 1 {
		t.Fatalf("forwarded %d findings, want 1 (MaxFindings cap)", len(got))
	}
}
