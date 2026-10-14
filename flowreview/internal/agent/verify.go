package agent

import (
	"fmt"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// verifyWindow is how many lines on either side of a cited line Verify will
// still accept a match at — models are usually right about the line but
// sometimes off by a line or two once the surrounding context shifts.
const verifyWindow = 2

// Verify checks a finding's evidence against the real files it cites, using
// read to fetch file contents (so callers can read from a worktree, a patch,
// or a revision — whatever the session is reviewing). An evidence excerpt
// must appear, after trimming whitespace from both sides, on the cited line
// or within verifyWindow lines of it; when it is found on a different line,
// that evidence's Line is corrected to where it actually matched.
//
// Verify sets f.Verification to a short description of what it found (on
// success) or why it rejected the finding (on failure). A finding with no
// evidence at all, or none of whose evidence verifies, is rejected: Verify
// returns false and the finding must not be shown to the user.
func Verify(f *model.AgentFinding, read func(path string) ([]byte, error)) bool {
	if len(f.Evidence) == 0 {
		f.Verification = "rejected: no evidence"
		return false
	}

	fileLines := map[string][]string{}
	linesOf := func(path string) ([]string, error) {
		if ls, ok := fileLines[path]; ok {
			return ls, nil
		}
		content, err := read(path)
		if err != nil {
			return nil, err
		}
		ls := strings.Split(string(content), "\n")
		fileLines[path] = ls
		return ls, nil
	}

	var verified int
	var reasons []string
	for i := range f.Evidence {
		ev := &f.Evidence[i]
		want := strings.TrimSpace(ev.Excerpt)
		if want == "" {
			reasons = append(reasons, fmt.Sprintf("%s:%d: empty excerpt", ev.Path, ev.Line))
			continue
		}

		lines, err := linesOf(ev.Path)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%s: %v", ev.Path, err))
			continue
		}

		n, ok := findNear(lines, ev.Line, want)
		if !ok {
			reasons = append(reasons, fmt.Sprintf("%s:%d: excerpt not found within +/-%d lines", ev.Path, ev.Line, verifyWindow))
			continue
		}
		ev.Line = n
		verified++
	}

	if verified == 0 {
		f.Verification = "rejected: " + strings.Join(reasons, "; ")
		return false
	}
	f.Verification = fmt.Sprintf("verified %d/%d evidence line(s)", verified, len(f.Evidence))
	return true
}

// findNear looks for want as a substring of lines[cited-1], trimmed, and
// then at increasing distance up to verifyWindow, closest first. It returns
// the 1-based line number it matched at.
func findNear(lines []string, cited int, want string) (int, bool) {
	for d := 0; d <= verifyWindow; d++ {
		for _, sign := range []int{1, -1} {
			if d == 0 && sign == -1 {
				continue // tried d==0 already
			}
			n := cited + d*sign
			if n < 1 || n > len(lines) {
				continue
			}
			if strings.Contains(strings.TrimSpace(lines[n-1]), want) {
				return n, true
			}
		}
	}
	return 0, false
}
