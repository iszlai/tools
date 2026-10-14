package main

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/host/github"
)

func TestExitCode(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Errorf("exitCode(nil) = %d, want 0", got)
	}
	if got := exitCode(errors.New("boom")); got != 1 {
		t.Errorf("exitCode(plain error) = %d, want 1", got)
	}
	if got := exitCode(usageErr(errors.New("bad flag"))); got != 2 {
		t.Errorf("exitCode(usageErr) = %d, want 2", got)
	}
	if got := exitCode(notFoundErr(errors.New("no such thing"))); got != 3 {
		t.Errorf("exitCode(notFoundErr) = %d, want 3", got)
	}
	if got := exitCode(hostErr(errors.New("host says no"))); got != 4 {
		t.Errorf("exitCode(hostErr) = %d, want 4", got)
	}
	// wrapped: classifyOpenErr-style helpers must still unwrap to the coded
	// error through fmt.Errorf("%w", ...) chains elsewhere in the program.
	wrapped := fmt.Errorf("context: %w", usageErr(errors.New("bad")))
	if got := exitCode(wrapped); got != 2 {
		t.Errorf("exitCode(wrapped usageErr) = %d, want 2", got)
	}
}

func TestClassifyOpenErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"github not found", fmt.Errorf("review: open: %w", github.ErrNotFound), 3},
		{"github unauthorized", fmt.Errorf("review: open: %w", github.ErrUnauthorized), 4},
		{"github forbidden", fmt.Errorf("review: open: %w", github.ErrForbidden), 4},
		{"PR not supported", review.ErrPRNotSupported, 4},
		{"os not exist", fmt.Errorf("review: read patch: %w", os.ErrNotExist), 3},
		{"not a git repository text", errors.New("git: open \"/tmp/x\": fatal: not a git repository (or any of the parent directories): .git"), 3},
		{"generic github error text", errors.New("github: no token found and the gh CLI is not installed"), 4},
		{"unclassified", errors.New("something else broke"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyOpenErr(tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("classifyOpenErr(nil) = %v, want nil", got)
				}
				return
			}
			if code := exitCode(got); code != tc.want {
				t.Errorf("classifyOpenErr(%v) -> exitCode = %d, want %d", tc.err, code, tc.want)
			}
		})
	}
}
