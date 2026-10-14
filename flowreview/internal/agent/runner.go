package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Runner wraps an AgentProvider so every caller gets the same guarantees
// regardless of which provider is running:
//
//   - scope.Limits.Timeout bounds the run as a context deadline.
//   - scope.Limits.MaxFunctions is passed through to the provider unchanged;
//     only the provider knows how to spend a function budget.
//   - scope.Limits.MaxFindings caps how many verified findings are forwarded;
//     once reached, Run cancels the provider's context.
//   - every finding is verified (see Verify) before it reaches emit, and
//     rejected findings never do.
//   - every forwarded finding gets an ID and its Provider field set.
//   - the whole run is logged as JSON lines to runsDir.
type Runner struct {
	provider AgentProvider
	runsDir  string
	read     func(path string) ([]byte, error)
	now      func() time.Time
}

// NewRunner wraps provider. runsDir is where this run's log file is written
// — the caller's session store passes in sessions/<repo>/<session>/runs/.
func NewRunner(provider AgentProvider, runsDir string) *Runner {
	return &Runner{provider: provider, runsDir: runsDir, read: os.ReadFile, now: time.Now}
}

// Run executes one review run, verifying and forwarding findings to emit as
// the provider produces them. It returns once the provider's Review returns
// — on its own, on ctx/timeout, or after MaxFindings is reached — and always
// writes the run log first.
func (r *Runner) Run(ctx context.Context, s *model.ReviewSession, scope Scope, emit func(model.AgentFinding)) error {
	start := r.now()
	caps := r.provider.Capabilities()
	runTag := fmt.Sprintf("%s-%s", start.UTC().Format("20060102T150405Z"), caps.Name)

	runCtx, cancel := context.WithCancel(ctx)
	if scope.Limits.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, scope.Limits.Timeout)
	}
	defer cancel()

	logFile, err := r.openLog(runTag)
	if err != nil {
		return fmt.Errorf("agent: open run log: %w", err)
	}
	defer logFile.Close()

	writeLogLine(logFile, runStartEvent{Type: "start", Time: start, Provider: caps.Name, Scope: scope})

	var emitted, rejected int
	runErr := r.provider.Review(runCtx, s, scope, func(f model.AgentFinding) {
		if scope.Limits.MaxFindings > 0 && emitted >= scope.Limits.MaxFindings {
			cancel()
			return
		}

		if !Verify(&f, r.read) {
			rejected++
			writeLogLine(logFile, runFindingEvent{Type: "finding", Time: r.now(), Accepted: false, Reason: f.Verification, Finding: f})
			return
		}

		emitted++
		f.ID = fmt.Sprintf("%s-%d", runTag, emitted)
		f.Provider = caps.Name
		writeLogLine(logFile, runFindingEvent{Type: "finding", Time: r.now(), Accepted: true, Finding: f})
		emit(f)
	})

	end := runEndEvent{
		Type:       "end",
		Time:       r.now(),
		Emitted:    emitted,
		Rejected:   rejected,
		DurationMS: r.now().Sub(start).Milliseconds(),
	}
	if runErr != nil {
		end.Error = runErr.Error()
	}
	writeLogLine(logFile, end)

	return runErr
}

func (r *Runner) openLog(runTag string) (*os.File, error) {
	if err := os.MkdirAll(r.runsDir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(r.runsDir, runTag+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}

// runStartEvent, runFindingEvent and runEndEvent are the three JSON-lines
// record shapes written to a run's log file, one per line.
type runStartEvent struct {
	Type     string    `json:"type"`
	Time     time.Time `json:"time"`
	Provider string    `json:"provider"`
	Scope    Scope     `json:"scope"`
}

type runFindingEvent struct {
	Type     string             `json:"type"`
	Time     time.Time          `json:"time"`
	Accepted bool               `json:"accepted"`
	Reason   string             `json:"reason,omitempty"`
	Finding  model.AgentFinding `json:"finding"`
}

type runEndEvent struct {
	Type       string    `json:"type"`
	Time       time.Time `json:"time"`
	Emitted    int       `json:"emitted"`
	Rejected   int       `json:"rejected"`
	DurationMS int64     `json:"duration_ms"`
	Error      string    `json:"error,omitempty"`
}

// writeLogLine marshals v and appends it to f. Logging is best-effort: a
// write failure must not fail the review run itself.
func writeLogLine(f *os.File, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = f.Write(b)
}
